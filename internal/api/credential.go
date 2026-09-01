package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
)

// CredentialKind names the scheme a caller must use to present a credential.
type CredentialKind string

const (
	CredentialPAT      CredentialKind = "pat"
	CredentialOAuth2   CredentialKind = "oauth2"
	CredentialJobToken CredentialKind = "job-token"
)

var (
	ErrUnauthenticated = errors.New("glab is not authenticated; run `glab auth login` to authenticate")

	// Callers that need a particular kind should report this as "not that kind"
	// in their own words rather than surfacing the wrapped Go type name.
	ErrUnsupportedAuthSource = errors.New("unsupported authentication method")
)

// Credential is the resolved credential a client authenticates with. Callers
// that only need to set a request header should use AuthSource().Header().
type Credential struct {
	Kind  CredentialKind
	Token string

	// Zero and empty respectively when the credential does not expire and when
	// the session behind it cannot be renewed.
	Expiry       time.Time
	RefreshToken string
}

// CredentialKind reports the kind of credential the client holds without
// resolving it, so a caller that accepts only some kinds can refuse before
// Credential renews an OAuth2 token it is not going to use.
func (c *Client) CredentialKind() (CredentialKind, error) {
	return credentialKind(c.AuthSource())
}

// Credential resolves the credential the client authenticates with, renewing an
// expired OAuth2 access token on the way. ctx does not reach that renewal:
// x/oauth2's TokenSource has no context-aware Token method.
func (c *Client) Credential(ctx context.Context) (Credential, error) {
	as := c.AuthSource()

	kind, err := credentialKind(as)
	if err != nil {
		return Credential{}, err
	}

	cred := Credential{Kind: kind}
	switch as := as.(type) {
	case gitlab.OAuthTokenSource:
		token, err := as.TokenSource.Token()
		if err != nil {
			return Credential{}, fmt.Errorf("failed to refresh the OAuth2 token: %w", err)
		}
		cred.Token = token.AccessToken
		cred.Expiry = token.Expiry
		cred.RefreshToken = token.RefreshToken
	case oauth2AccessTokenOnlyAuthSource:
		// The stored expiry is all there is: with no refresh token, nothing can
		// renew this token, and a consumer still needs to know when it dies.
		cred.Token = as.token
		cred.Expiry = as.expiry
	case gitlab.AccessTokenAuthSource:
		cred.Token = as.Token
	case gitlab.JobTokenAuthSource:
		cred.Token = as.Token
	}

	if cred.Token == "" {
		return Credential{}, fmt.Errorf("%w: %T holds no token", ErrUnsupportedAuthSource, as)
	}
	return cred, nil
}

// credentialKind is the single place that maps an auth source onto a kind.
func credentialKind(as gitlab.AuthSource) (CredentialKind, error) {
	switch as.(type) {
	case gitlab.OAuthTokenSource, oauth2AccessTokenOnlyAuthSource:
		return CredentialOAuth2, nil
	case gitlab.AccessTokenAuthSource:
		return CredentialPAT, nil
	case gitlab.JobTokenAuthSource:
		return CredentialJobToken, nil
	case gitlab.Unauthenticated:
		return "", ErrUnauthenticated
	default:
		return "", fmt.Errorf("%w: %T", ErrUnsupportedAuthSource, as)
	}
}
