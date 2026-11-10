package oauth2

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"gitlab.com/gitlab-org/api/client-go/v3/gitlaboauth2"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

// ErrClientIDRejected indicates the OAuth client ID was rejected by the GitLab instance.
var ErrClientIDRejected = errors.New("OAuth client ID was rejected by the GitLab instance")

func preflightRejectedClientIDErr(hostname string) error {
	return fmt.Errorf("%w on %s: check the Application ID, update it with `glab config set client_id <client_id> -g --host %s`, or clear it with `glab config set client_id \"\" -g --host %s`, or update or unset GITLAB_CLIENT_ID", ErrClientIDRejected, hostname, hostname, hostname)
}

// StartFlow performs the OAuth 2.0 Authorization Code flow: it opens a
// browser (or prints the URL if that fails) and waits for the local
// callback.
func StartFlow(ctx context.Context, cfg config.Config, io *iostreams.IOStreams, httpClient *http.Client, hostname string) (string, error) {
	clientID, persistClientID, err := resolveClientID(ctx, cfg, io, hostname)
	if err != nil {
		return "", err
	}

	ctx = context.WithValue(ctx, oauth2.HTTPClient, httpClient)
	baseURL, err := oauthBaseURL(cfg, hostname, glinstance.DefaultProtocol)
	if err != nil {
		return "", err
	}

	if err := preflightCheckClientID(ctx, httpClient, baseURL, clientID, hostname); err != nil {
		return "", err
	}

	token, err := gitlaboauth2.AuthorizationFlow(ctx, baseURL, clientID, redirectURL, scopes, callbackServerListenAddr, func(url string) error {
		browser, _ := cfg.Get(hostname, "browser")
		if err := utils.OpenInBrowser(url, browser); err != nil {
			io.LogErrorf("Failed opening a browser at %s\n", url)
			io.LogErrorf("Encountered error: %s\n", err)
			io.LogError("Try entering the URL in your browser manually.")
		}

		return nil
	})
	if err != nil {
		return "", err
	}

	// clientID just completed a real OAuth round trip, so it's proven now:
	// only commit it to config at this point, not on the paste itself.
	if err := persistClientID(); err != nil {
		return "", err
	}

	err = marshal(hostname, cfg, token)
	if err != nil {
		return "", err
	}

	return token.AccessToken, nil
}

// preflightCheckClientID verifies that the OAuth application ID is recognized
// by the instance before starting the local server and launching the browser.
// If the instance rejects the client ID with HTTP 400 Bad Request or HTTP 401
// Unauthorized with invalid_client, it fails fast instead of leaving the OAuth
// callback server hanging indefinitely.
func preflightCheckClientID(ctx context.Context, httpClient *http.Client, baseURL, clientID, hostname string) error {
	oauthCfg := gitlaboauth2.NewOAuth2Config(baseURL, clientID, redirectURL, scopes)
	authURL := oauthCfg.AuthCodeURL("preflight")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, authURL, http.NoBody)
	if err != nil {
		dbg.Debugf("oauth2: preflight request build failed for %s: %v", hostname, err)
		return nil
	}

	checkClient := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 5 * time.Second,
	}
	if httpClient != nil && httpClient.Transport != nil {
		checkClient.Transport = httpClient.Transport
	}

	resp, err := checkClient.Do(req)
	if err != nil {
		dbg.Debugf("oauth2: preflight check request failed for %s: %v", hostname, err)
		return nil
	}
	defer resp.Body.Close()

	// Note: Doorkeeper returns 400 for a malformed client_id, while
	// GitLab returns 401 for an unrecognized client_id. Proxies or SSO
	// layers returning 400 or 401 without invalid_client defer to the
	// browser flow.
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized {
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
		if err != nil {
			dbg.Debugf("oauth2: preflight body read error: %v", err)
			return nil
		}
		if strings.Contains(string(body), "invalid_client") {
			return preflightRejectedClientIDErr(hostname)
		}
		dbg.Debugf("oauth2: preflight check received %d without invalid_client on %s, falling back to browser flow", resp.StatusCode, hostname)
		return nil
	}

	return nil
}

// oauthBaseURL is the single place an OAuth base URL is built, so every flow that
// talks to the instance -- authorization, device, and token refresh -- picks up a
// configured subfolder. AuthEndpoint falls back to the default protocol when
// protocol is empty.
func oauthBaseURL(cfg config.Config, hostname, protocol string) (string, error) {
	subfolder, err := cfg.Get(hostname, "subfolder")
	if err != nil {
		return "", err
	}

	return glinstance.AuthEndpoint(hostname, protocol, subfolder), nil
}
