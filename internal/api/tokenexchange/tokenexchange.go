// Package tokenexchange exchanges a GitLab session token for a short-lived
// token scoped to one GitLab service through the POST /api/v4/token_exchange
// endpoint.
package tokenexchange

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
)

const path = "token_exchange"

// request is the JSON body sent to path. ExpiresIn is a pointer so that a nil
// value omits expires_in from the request body entirely, letting the server
// apply its own default lifetime rather than sending a CLI-chosen one.
type request struct {
	Audience  string `json:"audience"`
	ExpiresIn *int   `json:"expires_in,omitempty"`
}

type response struct {
	Token string `json:"token"`
}

// jwtCompactRe matches the JWS compact serialization: three base64url segments,
// with a possibly-empty signature for alg=none. Nothing but [A-Za-z0-9_-] and
// the two dots may appear.
//
// Enforced because callers write this token verbatim into line-oriented
// credential files (~/.m2/settings.xml, .npmrc and friends) or request
// metadata, and decodeUnverifiedClaims alone does not keep line breaks out
// of it. Go's base64 decoder skips "\r" and "\n" as insignificant whitespace,
// so a token whose signature or payload segment carries them decodes cleanly,
// claims and all, and reaches the writer with the breaks intact. Bytes like
// "=", ":", '"' and " " are rejected by the base64 decode already; the line
// breaks are the gap.
var jwtCompactRe = regexp.MustCompile(`^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*$`)

// Result is the decoded result of a successful token exchange. The
// claim-derived fields are populated here so callers never have to decode the
// token a second time.
type Result struct {
	// Token is the raw, encoded access token returned by the server. Nothing
	// here verifies its signature; it is trustworthy because of the TLS
	// connection to the issuing host. Treat it as a bearer credential for the
	// requested audience and hand it to nothing else.
	Token string `json:"token"`
	// ExpiresAt is the token's expiry, taken from its exp claim.
	ExpiresAt time.Time `json:"expires_at"`
	// Issuer is the token's iss claim.
	Issuer string `json:"issuer"`
	// Subject is the token's sub claim.
	Subject string `json:"subject"`
	// Audience is the token's aud claim: what it is scoped to. Printing this
	// is what distinguishes a service-scoped token from just "a token GitLab
	// issued".
	Audience string `json:"audience"`
}

// String redacts Token so logging or formatting a Result with %v or %+v never
// leaks the bearer token.
func (r Result) String() string {
	return fmt.Sprintf("ExchangeResult{Token:REDACTED, ExpiresAt:%s, Issuer:%s, Subject:%s, Audience:%s}",
		r.ExpiresAt, r.Issuer, r.Subject, r.Audience)
}

// MarshalJSON redacts Token so json.Marshal(Result) never leaks the bearer
// token, protecting callers that marshal the whole struct instead of a
// printer-local subset of its fields.
func (r Result) MarshalJSON() ([]byte, error) {
	type alias Result
	//nolint:forbidigo // implementing MarshalJSON itself, not producing stdout output
	return json.Marshal(struct {
		alias
		Token string `json:"token"`
	}{
		alias: alias(r),
		Token: "REDACTED",
	})
}

// Exchange exchanges the caller's GitLab credential for a short-lived token
// for audience, valid for expiresIn. A zero expiresIn omits expires_in from
// the request so the server applies its own default lifetime. Bounds on
// expiresIn are the caller's to enforce; the server's 400 is the
// authoritative answer.
func Exchange(ctx context.Context, gl *gitlab.Client, audience string, expiresIn time.Duration) (*Result, error) {
	body := request{Audience: audience}
	if expiresIn != 0 {
		// Rounded rather than truncated: the server's floor is one second, so
		// truncating could silently discard up to just under a full second of
		// a short, precisely-chosen request.
		seconds := int(expiresIn.Round(time.Second).Seconds())
		body.ExpiresIn = &seconds
	}

	req, err := gl.NewRequest(http.MethodPost, path, body, []gitlab.RequestOptionFunc{gitlab.WithContext(ctx), api.WithSafeRetries()})
	if err != nil {
		return nil, fmt.Errorf("failed to create token exchange request: %w", err)
	}

	var resp response
	if _, err := gl.Do(req, &resp); err != nil {
		if errors.Is(err, gitlab.ErrNotFound) {
			return nil, fmt.Errorf("token exchange is not available on this instance (requires GitLab EE 19.4 or later): %w", err)
		}
		return nil, fmt.Errorf("failed to exchange token: %w", err)
	}

	if resp.Token == "" {
		return nil, fmt.Errorf("server returned an empty token")
	}

	// Checked before the claims decode, so a token that cannot safely be
	// written to a credential file is rejected on its shape rather than on
	// whichever claim happens to be missing.
	if !jwtCompactRe.MatchString(resp.Token) {
		return nil, fmt.Errorf("server returned a token that is not in JWS compact form")
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

	return &Result{
		Token:     resp.Token,
		ExpiresAt: claims.ExpiresAt.Time,
		Issuer:    claims.Issuer,
		Subject:   claims.Subject,
		Audience:  strings.Join(claims.Audience, ","),
	}, nil
}

// decodeUnverifiedClaims decodes the registered claims of token without
// verifying its signature: the trust boundary is the TLS connection to the
// issuing GitLab host.
func decodeUnverifiedClaims(token string) (*jwt.RegisteredClaims, error) {
	claims := &jwt.RegisteredClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(token, claims); err != nil {
		return nil, fmt.Errorf("failed to decode token claims: %w", err)
	}
	return claims, nil
}
