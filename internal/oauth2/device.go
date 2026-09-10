package oauth2

import (
	"context"
	"fmt"
	"net/http"

	"golang.org/x/oauth2"

	"gitlab.com/gitlab-org/api/client-go/v3/gitlaboauth2"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

// StartDeviceFlow performs the OAuth 2.0 Device Authorization Grant (RFC 8628).
// It displays a one-time user code and verification URL, polls the token endpoint
// until the user completes authorization on a separate device, then persists the
// resulting token using the same on-disk shape as StartFlow.
func StartDeviceFlow(ctx context.Context, cfg config.Config, io *iostreams.IOStreams, httpClient *http.Client, hostname string) (string, error) {
	clientID, persistClientID, err := resolveClientID(ctx, cfg, io, hostname)
	if err != nil {
		return "", err
	}

	ctx = context.WithValue(ctx, oauth2.HTTPClient, httpClient)
	baseURL, err := oauthBaseURL(cfg, hostname)
	if err != nil {
		return "", err
	}

	// RFC 8628 has no redirect; pass "" for redirectURL.
	oauthCfg := gitlaboauth2.NewOAuth2Config(baseURL, clientID, "", scopes)

	da, err := oauthCfg.DeviceAuth(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to start device authorization: %w", err)
	}

	io.LogErrorf("\nFirst copy your one-time code: %s\n", da.UserCode)
	io.LogErrorf("Then open this URL on any device to authorize: %s\n\n", da.VerificationURI)
	io.LogError("Waiting for authorization...")

	token, err := oauthCfg.DeviceAccessToken(ctx, da)
	if err != nil {
		return "", fmt.Errorf("device authorization failed: %w", err)
	}

	// clientID just completed a real OAuth round trip, so it's proven now:
	// only commit it to config at this point, not on the paste itself.
	if err := persistClientID(); err != nil {
		return "", err
	}

	if err := marshal(hostname, cfg, token); err != nil {
		return "", err
	}

	return token.AccessToken, nil
}
