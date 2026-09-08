//go:build !integration

package revoke

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// noMorePages creates a response that indicates no more pages are available
func noMorePages() *gitlab.Response {
	return &gitlab.Response{NextPage: 0}
}

func TestRevokePersonalAccessToken(t *testing.T) {
	type testCase struct {
		name        string
		cli         string
		expectedOut string
		wantErr     bool
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	testUser := &gitlab.User{
		ID:       1,
		Username: "johndoe",
		Name:     "John Doe",
		Email:    "john.doe@acme.com",
	}

	testPAT := &gitlab.PersonalAccessToken{
		ID:        10183862,
		Name:      "my-pat",
		Revoked:   false,
		CreatedAt: parseTime("2024-07-08T01:23:04.311Z"),
		Scopes:    []string{"k8s_proxy"},
		UserID:    926857,
		Active:    true,
		ExpiresAt: new(gitlab.ISOTime(*parseTime("2024-08-07T00:00:00Z"))),
		Token:     "sometoken",
	}

	testCases := []testCase{
		{
			name:        "revoke personal access token as text",
			cli:         "--user @me my-pat",
			expectedOut: "revoked @me my-pat 10183862",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(testUser, nil, nil)
				tc.MockPersonalAccessTokens.EXPECT().
					ListPersonalAccessTokens(gomock.Any(), gomock.Any()).
					Return([]*gitlab.PersonalAccessToken{testPAT}, noMorePages(), nil)
				tc.MockPersonalAccessTokens.EXPECT().
					RevokePersonalAccessTokenByID(int64(10183862)).
					Return(nil, nil)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdRevoke,
				true,
				cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
			)

			// WHEN
			out, err := exec(tc.cli)

			// THEN
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantStderr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.expectedOut, out.OutBuf.String())
			assert.Empty(t, out.ErrBuf.String())
		})
	}
}

func TestRevokeGroupAccessToken(t *testing.T) {
	type testCase struct {
		name        string
		cli         string
		expectedOut string
		wantErr     bool
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	testGroupToken := &gitlab.GroupAccessToken{
		PersonalAccessToken: gitlab.PersonalAccessToken{
			ID:        10190772,
			UserID:    21989300,
			Name:      "my-group-token",
			Scopes:    []string{"read_registry", "read_repository"},
			CreatedAt: parseTime("2024-07-08T17:33:34.829Z"),
			ExpiresAt: new(gitlab.ISOTime(*parseTime("2024-08-07T00:00:00Z"))),
			Active:    true,
			Revoked:   false,
			Token:     "glpat-yz2791KMU-xxxxxxxxx",
		},
		AccessLevel: gitlab.DeveloperPermissions,
	}

	testCases := []testCase{
		{
			name:        "revoke group access token as text",
			cli:         "--group GROUP my-group-token",
			expectedOut: "revoked my-group-token 10190772",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroupAccessTokens.EXPECT().
					ListGroupAccessTokens("GROUP", gomock.Any(), gomock.Any()).
					Return([]*gitlab.GroupAccessToken{testGroupToken}, noMorePages(), nil)
				tc.MockGroupAccessTokens.EXPECT().
					RevokeGroupAccessToken("GROUP", int64(10190772)).
					Return(nil, nil)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdRevoke,
				true,
				cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
			)

			// WHEN
			out, err := exec(tc.cli)

			// THEN
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantStderr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.expectedOut, out.OutBuf.String())
			assert.Empty(t, out.ErrBuf.String())
		})
	}
}

func TestRevokeProjectAccessToken(t *testing.T) {
	type testCase struct {
		name        string
		cli         string
		expectedOut string
		wantErr     bool
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	testProjectToken := &gitlab.ProjectAccessToken{
		PersonalAccessToken: gitlab.PersonalAccessToken{
			ID:        10191548,
			UserID:    21990679,
			Name:      "my-project-token",
			Scopes:    []string{"api", "read_repository"},
			CreatedAt: parseTime("2024-07-08T19:47:14.727Z"),
			ExpiresAt: new(gitlab.ISOTime(*parseTime("2024-08-07T00:00:00Z"))),
			Active:    true,
			Revoked:   false,
			Token:     "glpat-dfsdfjksjdfslkdfjsd",
		},
		AccessLevel: gitlab.DeveloperPermissions,
	}

	testCases := []testCase{
		{
			name:        "revoke project access token as text",
			cli:         "my-project-token",
			expectedOut: "revoked my-project-token 10191548",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjectAccessTokens.EXPECT().
					ListProjectAccessTokens("OWNER/REPO", gomock.Any(), gomock.Any()).
					Return([]*gitlab.ProjectAccessToken{testProjectToken}, noMorePages(), nil)
				tc.MockProjectAccessTokens.EXPECT().
					RevokeProjectAccessToken("OWNER/REPO", int64(10191548)).
					Return(nil, nil)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdRevoke,
				true,
				cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
			)

			// WHEN
			out, err := exec(tc.cli)

			// THEN
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantStderr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.expectedOut, out.OutBuf.String())
			assert.Empty(t, out.ErrBuf.String())
		})
	}
}

func parseTime(s string) *time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return &t
}
