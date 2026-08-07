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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
)

// tokenExchangePath is the API path (relative to the client's base URL) for
// exchanging a session token for an Artifact Registry access token.
const tokenExchangePath = "token_exchange"

// tokenExchangeAudience identifies the Artifact Registry as the intended
// recipient of the exchanged token.
const tokenExchangeAudience = "gitlab-artifact-registry"

// tokenExchangeRequest is the JSON body sent to tokenExchangePath. ExpiresIn
// is a pointer so that a nil value omits expires_in from the request body
// entirely, letting the server apply its own default lifetime rather than
// sending a CLI-chosen one.
type tokenExchangeRequest struct {
	Audience  string `json:"audience"`
	ExpiresIn *int   `json:"expires_in,omitempty"`
}

// tokenExchangeResponse is the JSON body returned by tokenExchangePath.
type tokenExchangeResponse struct {
	Token string `json:"token"`
}

const (
	// MinDuration is the shortest lifetime a caller may request through
	// ExchangeToken. This is a CLI-side choice, not a server contract: the
	// server's actual floor is 1 second, and omitting expires_in (see
	// ExchangeDefaultToken) gets the server's 5 minute default. AppSec is
	// pushing for shorter-lived tokens (gitlab#601725, artifact-registry#229),
	// so keep this a CLI policy that can move independently of the server.
	MinDuration = 15 * time.Minute
	// MaxDuration is the longest lifetime a caller may request through
	// ExchangeToken. A CLI-side choice, not a server contract; see MinDuration.
	MaxDuration = 12 * time.Hour
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

// ExchangeResult is the decoded result of a successful token exchange. The
// claim-derived fields are populated here so callers never have to decode the
// token a second time.
type ExchangeResult struct {
	// Token is the raw, encoded access token returned by the server. Nothing
	// here verifies its signature; it is trustworthy because of the TLS
	// connection to the issuing host. Treat it as a bearer credential for
	// tokenExchangeAudience and hand it to nothing else.
	Token string `json:"token"`
	// ExpiresAt is the token's expiry, taken from its exp claim.
	ExpiresAt time.Time `json:"expires_at"`
	// Issuer is the token's iss claim.
	Issuer string `json:"issuer"`
	// Subject is the token's sub claim.
	Subject string `json:"subject"`
	// Audience is the token's aud claim: what it is scoped to. Printing this
	// is what distinguishes an Artifact-Registry-scoped token from just "a
	// token GitLab issued".
	Audience string `json:"audience"`
}

// String redacts Token so logging or formatting an ExchangeResult with %v or
// %+v never leaks the bearer token.
func (r ExchangeResult) String() string {
	return fmt.Sprintf("ExchangeResult{Token:REDACTED, ExpiresAt:%s, Issuer:%s, Subject:%s, Audience:%s}",
		r.ExpiresAt, r.Issuer, r.Subject, r.Audience)
}

// MarshalJSON redacts Token so json.Marshal(ExchangeResult) never leaks the
// bearer token, protecting callers that marshal the whole struct instead of a
// printer-local subset of its fields.
func (r ExchangeResult) MarshalJSON() ([]byte, error) {
	type alias ExchangeResult
	//nolint:forbidigo // implementing MarshalJSON itself, not producing stdout output
	return json.Marshal(struct {
		alias
		Token string `json:"token"`
	}{
		alias: alias(r),
		Token: "REDACTED",
	})
}

// ExchangeToken exchanges the caller's GitLab credential for a short-lived
// Artifact Registry access token valid for duration. duration must be within
// [MinDuration, MaxDuration].
func (c *Client) ExchangeToken(ctx context.Context, duration time.Duration) (*ExchangeResult, error) {
	if err := ValidateDuration(duration); err != nil {
		return nil, err
	}
	seconds := int(duration.Seconds())
	return c.exchange(ctx, &seconds)
}

// ExchangeDefaultToken exchanges the caller's GitLab credential for a
// short-lived Artifact Registry access token, omitting expires_in so the
// server applies its own default lifetime. Callers that only read the
// token's claims and then discard it, like `status`, want the server default
// rather than a CLI-chosen duration.
func (c *Client) ExchangeDefaultToken(ctx context.Context) (*ExchangeResult, error) {
	return c.exchange(ctx, nil)
}

// exchange performs the token-exchange request. expiresIn nil omits
// expires_in from the request body.
func (c *Client) exchange(ctx context.Context, expiresIn *int) (*ExchangeResult, error) {
	body := tokenExchangeRequest{
		Audience:  tokenExchangeAudience,
		ExpiresIn: expiresIn,
	}

	req, err := c.gl.NewRequest(http.MethodPost, tokenExchangePath, body, []gitlab.RequestOptionFunc{gitlab.WithContext(ctx)})
	if err != nil {
		return nil, fmt.Errorf("failed to create token exchange request: %w", err)
	}

	var resp tokenExchangeResponse
	if _, err := c.gl.Do(req, &resp); err != nil {
		if errors.Is(err, gitlab.ErrNotFound) {
			return nil, fmt.Errorf("token exchange is not enabled on this instance: %w", err)
		}
		return nil, fmt.Errorf("failed to exchange token: %w", err)
	}

	if resp.Token == "" {
		return nil, fmt.Errorf("server returned an empty token")
	}

	claims, err := decodeUnverifiedClaims(resp.Token)
	if err != nil {
		return nil, fmt.Errorf("failed to decode exchanged token: %w", err)
	}

	if claims.ExpiresAt == nil {
		return nil, fmt.Errorf("exchanged token has no expiry claim")
	}
	if claims.Issuer == "" {
		return nil, fmt.Errorf("exchanged token has no issuer claim")
	}
	if claims.Subject == "" {
		return nil, fmt.Errorf("exchanged token has no subject claim")
	}

	return &ExchangeResult{
		Token:     resp.Token,
		ExpiresAt: claims.ExpiresAt.Time,
		Issuer:    claims.Issuer,
		Subject:   claims.Subject,
		Audience:  strings.Join(claims.Audience, ","),
	}, nil
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

// decodeUnverifiedClaims decodes the registered claims of token without
// verifying its signature: the trust boundary is the TLS connection to the
// issuing GitLab host. Unexported because ExchangeResult already carries every
// claim callers need.
func decodeUnverifiedClaims(token string) (*jwt.RegisteredClaims, error) {
	claims := &jwt.RegisteredClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(token, claims); err != nil {
		return nil, fmt.Errorf("failed to decode token claims: %w", err)
	}
	return claims, nil
}
