package api

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	xoauth2 "golang.org/x/oauth2"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
)

type staticTokenSource struct {
	token *xoauth2.Token
}

func (s staticTokenSource) Token() (*xoauth2.Token, error) {
	return s.token, nil
}

// Credential is the single place that maps an auth source onto a credential
// kind, so pin every source NewClientFromConfig can produce.
func TestClient_Credential(t *testing.T) {
	expiry := time.Now().Add(time.Hour).Truncate(time.Second)

	tests := []struct {
		name       string
		authSource gitlab.AuthSource
		expected   Credential
	}{
		{
			name:       "personal access token",
			authSource: gitlab.AccessTokenAuthSource{Token: "a-pat"},
			expected:   Credential{Kind: CredentialPAT, Token: "a-pat"},
		},
		{
			name:       "job token",
			authSource: gitlab.JobTokenAuthSource{Token: "a-job-token"},
			expected:   Credential{Kind: CredentialJobToken, Token: "a-job-token"},
		},
		{
			// A refreshable session reports both the expiry and the refresh token,
			// which the Git credential protocol passes on to Git.
			name: "refreshable OAuth2 session",
			authSource: gitlab.OAuthTokenSource{TokenSource: staticTokenSource{token: &xoauth2.Token{
				AccessToken:  "an-access-token",
				RefreshToken: "a-refresh-token",
				Expiry:       expiry,
			}}},
			expected: Credential{
				Kind:         CredentialOAuth2,
				Token:        "an-access-token",
				Expiry:       expiry,
				RefreshToken: "a-refresh-token",
			},
		},
		{
			// Reported as OAuth2 even though it cannot be renewed: the kind names
			// the scheme the caller must use, not whether a refresh is possible.
			name:       "OAuth2 access token with no refresh token",
			authSource: oauth2AccessTokenOnlyAuthSource{token: "an-access-token"},
			expected:   Credential{Kind: CredentialOAuth2, Token: "an-access-token"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := clientWithAuthSource(t, tt.authSource)

			cred, err := client.Credential(t.Context())
			require.NoError(t, err)
			assert.Equal(t, tt.expected, cred)
		})
	}
}

// NewConfigTokenSource hands back an x/oauth2 reuseTokenSource, so exercise
// that concrete type rather than only a hand-written TokenSource.
func TestClient_Credential_reuseTokenSource(t *testing.T) {
	tests := []struct {
		name          string
		expiresIn     time.Duration
		expectedToken string
	}{
		{
			name:          "valid cached token is reused",
			expiresIn:     30 * time.Minute,
			expectedToken: "cached-token",
		},
		{
			name:          "expired cached token is renewed",
			expiresIn:     -time.Minute,
			expectedToken: "fresh-token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reusable := xoauth2.ReuseTokenSource(
				&xoauth2.Token{AccessToken: "cached-token", Expiry: time.Now().Add(tt.expiresIn)},
				staticTokenSource{token: &xoauth2.Token{
					AccessToken: "fresh-token",
					Expiry:      time.Now().Add(time.Hour),
				}},
			)
			client := clientWithAuthSource(t, gitlab.OAuthTokenSource{TokenSource: reusable})

			cred, err := client.Credential(t.Context())
			require.NoError(t, err)
			assert.Equal(t, tt.expectedToken, cred.Token)
		})
	}
}

// CredentialKind must agree with Credential on every source, since a caller
// that refuses on kind alone never reaches Credential to find out otherwise.
func TestClient_CredentialKind_matchesCredential(t *testing.T) {
	sources := []gitlab.AuthSource{
		gitlab.AccessTokenAuthSource{Token: "a-pat"},
		gitlab.JobTokenAuthSource{Token: "a-job-token"},
		oauth2AccessTokenOnlyAuthSource{token: "an-access-token"},
		gitlab.OAuthTokenSource{TokenSource: staticTokenSource{token: &xoauth2.Token{
			AccessToken: "an-access-token",
			Expiry:      time.Now().Add(time.Hour),
		}}},
	}

	for _, as := range sources {
		t.Run(fmt.Sprintf("%T", as), func(t *testing.T) {
			client := clientWithAuthSource(t, as)

			kind, err := client.CredentialKind()
			require.NoError(t, err)

			cred, err := client.Credential(t.Context())
			require.NoError(t, err)
			assert.Equal(t, cred.Kind, kind)
		})
	}
}

func TestClient_CredentialKind_errors(t *testing.T) {
	_, err := clientWithAuthSource(t, gitlab.Unauthenticated{}).CredentialKind()
	require.ErrorIs(t, err, ErrUnauthenticated)

	_, err = clientWithAuthSource(t, &gitlab.PasswordCredentialsAuthSource{Password: "p"}).CredentialKind()
	require.ErrorIs(t, err, ErrUnsupportedAuthSource)
}

func TestClient_Credential_unauthenticated(t *testing.T) {
	client := clientWithAuthSource(t, gitlab.Unauthenticated{})

	_, err := client.Credential(t.Context())
	assert.ErrorIs(t, err, ErrUnauthenticated)
}

func TestClient_Credential_unsupportedAuthSource(t *testing.T) {
	client := clientWithAuthSource(t, &gitlab.PasswordCredentialsAuthSource{Password: "a-password"})

	_, err := client.Credential(t.Context())
	require.ErrorIs(t, err, ErrUnsupportedAuthSource)
	// The Go type stays in the message for whoever has to debug it.
	assert.ErrorContains(t, err, "*gitlab.PasswordCredentialsAuthSource")
}

func clientWithAuthSource(t *testing.T, as gitlab.AuthSource) *Client {
	t.Helper()

	client, err := NewClient(
		func(*http.Client) (gitlab.AuthSource, error) { return as, nil },
		WithBaseURL("https://example.com/api/v4/"),
	)
	require.NoError(t, err)
	return client
}
