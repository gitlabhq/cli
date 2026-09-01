//go:build !integration

package graph

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	xoauth2 "golang.org/x/oauth2"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// KAS accepts only the "pat:" authorization scheme, so anything that is not a
// personal or project access token has to be refused before the server starts.
func TestGraph_refusesNonAccessTokenCredentials(t *testing.T) {
	tests := []struct {
		name        string
		authSource  gitlab.AuthSource
		expectedErr string
	}{
		{
			name:        "job token",
			authSource:  gitlab.JobTokenAuthSource{Token: "a-job-token"},
			expectedErr: "supports authentication with only personal and project access tokens",
		},
		{
			// The constraint this command exists to enforce: KAS would reject an
			// OAuth token sent under the "pat:" scheme, so refuse it here instead.
			// Credential maps both OAuth2 auth sources onto the same kind, which
			// TestClient_Credential pins, so one shape covers the branch.
			name: "OAuth2 session",
			authSource: gitlab.OAuthTokenSource{TokenSource: &countingTokenSource{token: &xoauth2.Token{
				AccessToken: "an-access-token",
				Expiry:      time.Now().Add(time.Hour),
			}}},
			expectedErr: "supports authentication with only personal and project access tokens",
		},
		{
			// Holds no token at all, so it cannot be classified. The message names
			// the requirement rather than a Go type.
			name:        "password credentials",
			authSource:  &gitlab.PasswordCredentialsAuthSource{Password: "a-password"},
			expectedErr: "supports authentication with only personal and project access tokens",
		},
		{
			// A host with no credentials gets the more actionable message instead.
			name:        "unauthenticated",
			authSource:  gitlab.Unauthenticated{},
			expectedErr: "glab is not authenticated",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdGraph,
				false,
				cmdtest.WithApiClient(cmdtest.NewTestAuthSourceApiClient(t, nil, tt.authSource, "gitlab.example.com")),
				cmdtest.WithBaseRepo("OWNER", "REPO", "gitlab.example.com"),
			)

			_, err := exec("--agent 123")
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.expectedErr)
		})
	}
}

// countingTokenSource records whether the command tried to read the token.
type countingTokenSource struct {
	token *xoauth2.Token
	calls int
}

func (s *countingTokenSource) Token() (*xoauth2.Token, error) {
	s.calls++
	return s.token, nil
}

// An OAuth2 token is refused on kind alone, so the command must not renew a
// token it is about to reject.
func TestGraph_refusesOAuth2WithoutReadingTheToken(t *testing.T) {
	ts := &countingTokenSource{token: &xoauth2.Token{
		AccessToken: "an-access-token",
		Expiry:      time.Now().Add(time.Hour),
	}}

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdGraph,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestAuthSourceApiClient(
			t, nil, gitlab.OAuthTokenSource{TokenSource: ts}, "gitlab.example.com",
		)),
		cmdtest.WithBaseRepo("OWNER", "REPO", "gitlab.example.com"),
	)

	_, err := exec("--agent 123")

	require.ErrorIs(t, err, errUnsupportedToken)
	assert.Zero(t, ts.calls, "the token was read for a credential the command refuses")
}

// The refusal cases above all fail before the command does any work, so cover
// the other side: an access token gets past the check and the command proceeds
// to talk to the API.
func TestGraph_acceptsAccessToken(t *testing.T) {
	ctrl := gomock.NewController(t)
	tc := gitlabtesting.NewTestClientWithCtrl(ctrl, gitlab.WithBaseURL("https://gitlab.example.com"))
	tc.MockMetadata.EXPECT().GetMetadata(gomock.Any()).Return(nil, nil, errors.New("metadata unavailable"))

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdGraph,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestAuthSourceApiClient(
			t, nil, gitlab.AccessTokenAuthSource{Token: "a-pat"}, "gitlab.example.com",
			api.WithGitLabClient(tc.Client),
		)),
		cmdtest.WithBaseRepo("OWNER", "REPO", "gitlab.example.com"),
	)

	_, err := exec("--agent 123")

	// Reaching the metadata call is the assertion: the credential was accepted.
	require.Error(t, err)
	require.NotErrorIs(t, err, errUnsupportedToken)
	assert.ErrorContains(t, err, "GitLab metadata")
}
