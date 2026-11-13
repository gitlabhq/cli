package oauth2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	return fmt.Errorf("%w on %s: the Application ID is unknown, or the application is confidential (glab needs a non-confidential application); update it with `glab config set client_id <client_id> -g --host %s`, clear it with `glab config set client_id \"\" -g --host %s`, or update or unset GITLAB_CLIENT_ID", ErrClientIDRejected, hostname, hostname, hostname)
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

	// GitLab.com always uses the built-in ID, so there is nothing to check.
	if glinstance.IsSelfHosted(hostname) {
		if err := preflightCheckClientID(ctx, httpClient, baseURL, clientID, hostname); err != nil {
			return "", err
		}
	}

	token, err := gitlaboauth2.AuthorizationFlow(ctx, baseURL, clientID, redirectURL, scopes, callbackServerListenAddr, func(url string) error {
		browser, _ := cfg.Get(hostname, "browser")
		if err := utils.OpenInBrowser(url, browser); err != nil {
			io.LogErrorf("Failed opening a browser at %s\n", url)
			io.LogErrorf("Encountered error: %s\n", err)
			io.LogError("Try entering the URL in your browser manually.")
		}
		// A wrong redirect URI or missing scope only shows up in the browser.
		if glinstance.IsSelfHosted(hostname) {
			io.LogErrorf("If the browser shows an error instead of returning here, press Ctrl+C and check that the OAuth application on %s has redirect URI %s and the scopes %s.\n", hostname, redirectURL, strings.Join(scopes, " "))
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

// preflightCode is a deliberately invalid authorization code. Workhorse sends
// /oauth/token to the IAM Auth service only for codes with a gliam prefix, so
// this one always reaches Rails, which knows every application glab can use.
const preflightCode = "glab-preflight"

// preflightCheckClientID fails fast on a client ID the instance doesn't know,
// before the browser opens and the callback wait can hang. It uses the token
// endpoint because, without the IAM Auth service, /oauth/authorize redirects
// to sign-in before it checks the client.
func preflightCheckClientID(ctx context.Context, httpClient *http.Client, baseURL, clientID, hostname string) error {
	tokenURL := gitlaboauth2.NewOAuth2Config(baseURL, clientID, redirectURL, scopes).Endpoint.TokenURL
	form := url.Values{
		"grant_type":   {"authorization_code"},
		"client_id":    {clientID},
		"code":         {preflightCode},
		"redirect_uri": {redirectURL},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		dbg.Debugf("oauth2: preflight request build failed for %s: %v", hostname, err)
		return nil
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

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

	// RFC 6749 section 5.2 allows invalid_client as 400 or 401.
	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusUnauthorized {
		dbg.Debugf("oauth2: preflight check received %d on %s, continuing to browser flow", resp.StatusCode, hostname)
		return nil
	}

	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&body); err != nil {
		dbg.Debugf("oauth2: preflight check could not decode %d response on %s: %v", resp.StatusCode, hostname, err)
		return nil
	}
	if body.Error == "invalid_client" {
		return preflightRejectedClientIDErr(hostname)
	}

	dbg.Debugf("oauth2: preflight check received %q on %s, continuing to browser flow", body.Error, hostname)
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
