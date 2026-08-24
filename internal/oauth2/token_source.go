package oauth2

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"gitlab.com/gitlab-org/api/client-go/v2/gitlaboauth2"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/dbg"
)

// adoptAttempts bounds how many times we re-read the freshest credentials after
// a refresh fails with invalid_grant before giving up. A concurrent process that
// won the refresh race writes the rotated token to the keyring and then to the
// config file; a small number of retries covers the brief window between those
// writes and our failed request returning.
const adoptAttempts = 3

// adoptBackoff is the pause between adopt attempts after the first. It is a var
// so tests can set it to zero and stay fast.
var adoptBackoff = 50 * time.Millisecond

type configTokenSource struct {
	cfg        config.Config
	httpClient *http.Client
	hostname   string

	// searchEnvForIdentity controls whether the initial and freshly re-read
	// access token may come from GITLAB_TOKEN/GITLAB_ACCESS_TOKEN/OAUTH_TOKEN
	// in addition to config. See WithoutTokenFromEnvironment's doc comment in
	// unmarshal for why this must be false for a caller like the Docker
	// credential helper.
	searchEnvForIdentity bool

	oauth2Config *oauth2.Config

	// Token is not thread-safe
	mu sync.Mutex
}

// NewConfigTokenSource returns an oauth2.TokenSource backed by cfg's stored
// OAuth2 credentials for hostname, refreshing and persisting a rotated token
// as needed. searchEnvForIdentity controls whether the access token may also
// come from GITLAB_TOKEN/GITLAB_ACCESS_TOKEN/OAUTH_TOKEN; pass false wherever
// glab acts on behalf of another process that inherits the user's shell
// environment, such as the Docker credential helper, so a stray environment
// variable cannot substitute a different identity's access token.
func NewConfigTokenSource(cfg config.Config, httpClient *http.Client, protocol, hostname string, searchEnvForIdentity bool) (oauth2.TokenSource, error) {
	clientID, err := oauthClientID(cfg, hostname)
	if err != nil {
		return nil, err
	}

	oauth2Config := gitlaboauth2.NewOAuth2Config(fmt.Sprintf("%s://%s", protocol, hostname), clientID, redirectURL, scopes)

	token, err := unmarshal(hostname, cfg, searchEnvForIdentity)
	if err != nil {
		return nil, err
	}

	src := &configTokenSource{
		cfg:                  cfg,
		oauth2Config:         oauth2Config,
		httpClient:           httpClient,
		hostname:             hostname,
		searchEnvForIdentity: searchEnvForIdentity,
	}

	return oauth2.ReuseTokenSource(token, src), nil
}

func (c *configTokenSource) Token() (*oauth2.Token, error) {
	token, err := c.refreshLocked()
	if err == nil {
		return token, nil
	}

	// A concurrent process may have won the refresh race and consumed the
	// single-use refresh token out from under us. Re-read the freshest copy and
	// adopt the winner's rotated token rather than forcing the user to
	// re-authenticate. A genuinely revoked or expired session yields no valid
	// token, so the original error is surfaced.
	//
	// adopt() only waits for the winner, which is a different process, to commit,
	// so it runs outside refreshLocked's lock: holding the per-process lock during
	// its polling would just block this process's other Token() callers, which
	// would skip-if-valid once the winner has committed.
	if isInvalidGrant(err) {
		if adopted, ok := c.adopt(); ok {
			return adopted, nil
		}
	}
	return nil, err
}

// refreshLocked runs the read-refresh-write cycle while holding the per-process
// lock (scoped to this method via defer, so adopt() in Token stays outside it).
// It returns the already-valid or newly refreshed token, or the refresh error
// for Token to inspect for a lost race.
func (c *configTokenSource) refreshLocked() (*oauth2.Token, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Read the freshest view of the credentials. GitLab OAuth refresh tokens are
	// single-use, so a long-lived process (for example, `glab mcp serve`) that
	// acted on its startup-time in-memory copy could present a token another
	// process has already rotated, or launch a redundant refresh. Re-reading from
	// disk and the keyring keeps concurrent processes from racing on a stale
	// token. Writes go back through the same source so the config file is not
	// rewritten from a stale in-memory document.
	src, token, err := c.freshest()
	if err != nil {
		return nil, err
	}

	// Another process may have rotated the token while we were reading it. If the
	// freshest copy is already valid, use it and skip the network refresh (and the
	// redundant credential write that would follow).
	if token.Valid() {
		return token, nil
	}

	// Everything below is irreversible: GitLab spends the single-use refresh
	// token the moment it answers, so a write that fails afterwards loses the
	// session, and the invalid_grant that follows surfaces on a later command
	// with nothing pointing back at the write.
	if err := config.CredentialWriteProbe(src, c.hostname); err != nil {
		return nil, fmt.Errorf("not refreshing the OAuth token for %q, because the refreshed credentials could not be saved: %w", c.hostname, err)
	}

	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, c.httpClient)
	refreshedToken, err := c.oauth2Config.TokenSource(ctx, token).Token()
	if err != nil {
		return nil, err
	}

	err = marshal(c.hostname, src, refreshedToken)
	if err != nil {
		return nil, err
	}

	err = src.Write()
	if err != nil {
		return nil, err
	}

	return refreshedToken, nil
}

// freshest returns the config to write back through and the freshest token for
// the host. It prefers a copy re-read from disk and the keyring so a stale
// in-memory document is neither acted on nor written back; it falls back to the
// in-memory config when it cannot be re-read.
func (c *configTokenSource) freshest() (config.Config, *oauth2.Token, error) {
	src := c.cfg
	token, err := unmarshal(c.hostname, c.cfg, c.searchEnvForIdentity)

	fresh, reloadErr := c.cfg.Reload()
	if reloadErr != nil {
		dbg.Debugf("oauth2: could not re-read config for %q, using in-memory copy: %v", c.hostname, reloadErr)
		return src, token, err
	}

	freshToken, freshErr := unmarshal(c.hostname, fresh, c.searchEnvForIdentity)
	if freshErr != nil {
		dbg.Debugf("oauth2: could not parse re-read config for %q, using in-memory copy: %v", c.hostname, freshErr)
		return src, token, err
	}

	return fresh, freshToken, nil
}

// adopt re-reads the freshest credentials after a failed refresh and returns the
// token if a concurrent process has rotated it to a valid one. It retries a
// bounded number of times to cover the brief window between the winning process
// writing the rotated token to the keyring and to the config file.
func (c *configTokenSource) adopt() (*oauth2.Token, bool) {
	for attempt := range adoptAttempts {
		if attempt > 0 {
			time.Sleep(adoptBackoff)
		}

		fresh, err := c.cfg.Reload()
		if err != nil {
			dbg.Debugf("oauth2: adopt: could not re-read config for %q: %v", c.hostname, err)
			return nil, false
		}

		token, err := unmarshal(c.hostname, fresh, c.searchEnvForIdentity)
		if err != nil {
			dbg.Debugf("oauth2: adopt: could not parse re-read config for %q (attempt %d): %v", c.hostname, attempt+1, err)
			continue
		}
		if token.Valid() {
			return token, true
		}
	}

	return nil, false
}

// isInvalidGrant reports whether err is an OAuth token endpoint error with the
// RFC 6749 "invalid_grant" code, which GitLab returns when a single-use refresh
// token has already been consumed (for example, by a concurrent process).
func isInvalidGrant(err error) bool {
	if retrieveErr, ok := errors.AsType[*oauth2.RetrieveError](err); ok {
		return retrieveErr.ErrorCode == "invalid_grant"
	}
	return false
}
