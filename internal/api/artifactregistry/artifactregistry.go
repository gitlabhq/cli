// Package artifactregistry provides a client for exchanging a GitLab session
// token for a short-lived GitLab Artifact Registry access token via the
// POST /api/v4/token_exchange endpoint.
//
// It lives under internal/api rather than beside the command that uses it so
// that the glab auth docker credential helper can consume it too once it
// learns about artifact registries: command packages must not import each
// other.
package artifactregistry

import (
	"context"
	"fmt"
	"time"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api/tokenexchange"
)

// tokenExchangeAudience identifies the Artifact Registry as the intended
// recipient of the exchanged token.
const tokenExchangeAudience = "gitlab-artifact-registry"

const (
	// MinDuration is the shortest lifetime a caller may request through
	// ExchangeToken: the server's actual floor. These tokens cannot be
	// revoked once minted, and AppSec is pushing for shorter-lived tokens
	// (gitlab#601725, artifact-registry#229), so the CLI does not add a
	// floor of its own above the server's: a caller asking for less
	// exposure should never be blocked from getting it.
	MinDuration = 1 * time.Second
	// MaxDuration is the longest lifetime a caller may request through
	// ExchangeToken. A CLI-side choice, not a server contract; see
	// MinDuration.
	MaxDuration = 12 * time.Hour
	// DefaultDuration is what callers get when they do not choose a
	// duration explicitly, e.g. `glab artifact-registry get-token` with no
	// --duration. A CLI-side choice, independent of MinDuration and of the
	// server's own 5-minute default, which applies only when expires_in is
	// omitted entirely (see ExchangeDefaultToken).
	DefaultDuration = 15 * time.Minute
	// DockerHelperDuration is the token lifetime the Docker credential helper
	// requests for every artifact-registry exchange it makes. Docker invokes
	// the helper with no way to pass a duration, so this is the only lever;
	// it is entirely independent of `get-token`, which always validates and
	// uses its own --duration flag (defaulting to DefaultDuration, not
	// MinDuration). It is deliberately longer than MinDuration: a docker
	// pull/push holds the credential for the whole operation, not just the
	// initial handshake, and a token expiring mid-pull hard-fails it.
	// Changing it changes how long every docker pull/push's token stays valid.
	DockerHelperDuration = time.Hour
)

// Client exchanges a GitLab session token for a short-lived Artifact
// Registry access token.
type Client struct {
	gl *gitlab.Client
}

// NewClient returns a Client that issues token-exchange requests through gl.
func NewClient(gl *gitlab.Client) *Client {
	return &Client{gl: gl}
}

// ExchangeResult is the decoded result of a successful token exchange.
type ExchangeResult = tokenexchange.Result

// ExchangeToken exchanges the caller's GitLab credential for a short-lived
// Artifact Registry access token valid for duration. duration must be within
// [MinDuration, MaxDuration].
func (c *Client) ExchangeToken(ctx context.Context, duration time.Duration) (*ExchangeResult, error) {
	if err := ValidateDuration(duration); err != nil {
		return nil, err
	}
	return tokenexchange.Exchange(ctx, c.gl, tokenExchangeAudience, duration)
}

// ExchangeDefaultToken exchanges the caller's GitLab credential for a
// short-lived Artifact Registry access token, omitting expires_in so the
// server applies its own default lifetime. Callers that only read the
// token's claims and then discard it, like `status`, want the server default
// rather than a CLI-chosen duration.
func (c *Client) ExchangeDefaultToken(ctx context.Context) (*ExchangeResult, error) {
	return tokenexchange.Exchange(ctx, c.gl, tokenExchangeAudience, 0)
}

// ValidateDuration reports whether d is within [MinDuration, MaxDuration].
// This is an advisory, CLI-side guard so flag parsing fails fast with a clear
// message; it is not a mirror of server policy, which can change
// independently (artifact-registry#229) and whose 400 response is always the
// authoritative answer.
func ValidateDuration(d time.Duration) error {
	if d < MinDuration || d > MaxDuration {
		return fmt.Errorf("duration must be between %s and %s", MinDuration, MaxDuration)
	}
	return nil
}
