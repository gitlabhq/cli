package oauth2

import (
	"context"
	"net/http"

	"golang.org/x/oauth2"

	"gitlab.com/gitlab-org/api/client-go/v3/gitlaboauth2"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

// StartFlow performs the OAuth 2.0 Authorization Code flow: it opens a
// browser (or prints the URL if that fails) and waits for the local
// callback.
func StartFlow(ctx context.Context, cfg config.Config, io *iostreams.IOStreams, httpClient *http.Client, hostname string) (string, error) {
	clientID, persistClientID, err := resolveClientID(ctx, cfg, io, hostname)
	if err != nil {
		return "", err
	}

	ctx = context.WithValue(ctx, oauth2.HTTPClient, httpClient)
	baseURL, err := oauthBaseURL(cfg, hostname)
	if err != nil {
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

func oauthBaseURL(cfg config.Config, hostname string) (string, error) {
	subfolder, err := cfg.Get(hostname, "subfolder")
	if err != nil {
		return "", err
	}

	return glinstance.AuthEndpoint(hostname, glinstance.DefaultProtocol, subfolder), nil
}
