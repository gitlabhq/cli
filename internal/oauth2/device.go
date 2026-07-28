package oauth2

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"golang.org/x/oauth2"

	"gitlab.com/gitlab-org/api/client-go/v2/gitlaboauth2"

	"gitlab.com/gitlab-org/cli/internal/config"
)

// StartDeviceFlow performs the OAuth 2.0 Device Authorization Grant (RFC 8628).
// It displays a one-time user code and verification URL, polls the token endpoint
// until the user completes authorization on a separate device, then persists the
// resulting token using the same on-disk shape as StartFlow.
func StartDeviceFlow(ctx context.Context, cfg config.Config, out io.Writer, httpClient *http.Client, hostname string) (string, error) {
	clientID, err := oauthClientID(cfg, hostname)
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

	fmt.Fprintf(out, "\nFirst copy your one-time code: %s\n", da.UserCode)
	fmt.Fprintf(out, "Then open this URL on any device to authorize: %s\n\n", da.VerificationURI)
	fmt.Fprintln(out, "Waiting for authorization...")

	token, err := oauthCfg.DeviceAccessToken(ctx, da)
	if err != nil {
		return "", fmt.Errorf("device authorization failed: %w", err)
	}

	if err := marshal(hostname, cfg, token); err != nil {
		return "", err
	}

	return token.AccessToken, nil
}
