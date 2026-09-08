//go:build !integration

package create

import (
	"encoding/json"
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

func parseTime(s string) *time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return &t
}

func TestCreateOwnPersonalAccessToken(t *testing.T) {
	type testCase struct {
		name        string
		cli         string
		expectedOut string
		wantJSON    bool
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
			name:        "create own PAT as text",
			cli:         "--user @me --scope k8s_proxy my-pat",
			expectedOut: "sometoken\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(testUser, nil, nil)
				tc.MockPersonalAccessTokens.EXPECT().
					ListPersonalAccessTokens(gomock.Any(), gomock.Any()).
					Return([]*gitlab.PersonalAccessToken{}, noMorePages(), nil)
				tc.MockUsers.EXPECT().
					CreatePersonalAccessTokenForCurrentUser(gomock.Any()).
					Return(testPAT, nil, nil)
			},
		},
		{
			name:     "create own PAT as json",
			cli:      "--user @me --scope k8s_proxy --output json my-pat",
			wantJSON: true,
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(testUser, nil, nil)
				tc.MockPersonalAccessTokens.EXPECT().
					ListPersonalAccessTokens(gomock.Any(), gomock.Any()).
					Return([]*gitlab.PersonalAccessToken{}, noMorePages(), nil)
				tc.MockUsers.EXPECT().
					CreatePersonalAccessTokenForCurrentUser(gomock.Any()).
					Return(testPAT, nil, nil)
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
				NewCmdCreate,
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
			if tc.wantJSON {
				var result map[string]any
				err := json.Unmarshal(out.OutBuf.Bytes(), &result)
				require.NoError(t, err)
				assert.Equal(t, "my-pat", result["name"])
			} else if tc.expectedOut != "" {
				assert.Equal(t, tc.expectedOut, out.OutBuf.String())
			}
			assert.Empty(t, out.ErrBuf.String())
		})
	}
}

func TestCreateOtherUserPersonalAccessToken(t *testing.T) {
	type testCase struct {
		name        string
		cli         string
		expectedOut string
		wantJSON    bool
		wantErr     bool
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	otherUser := &gitlab.User{
		ID:       2,
		Username: "janedoe",
		Name:     "Jane Doe",
		Email:    "jane.doe@acme.com",
	}

	testPAT := &gitlab.PersonalAccessToken{
		ID:        10183862,
		Name:      "your-pat",
		Revoked:   false,
		CreatedAt: parseTime("2024-07-08T01:23:04.311Z"),
		Scopes:    []string{"read_registry"},
		UserID:    2,
		Active:    true,
		ExpiresAt: new(gitlab.ISOTime(*parseTime("2024-08-07T00:00:00Z"))),
		Token:     "sometoken",
	}

	testCases := []testCase{
		{
			name:        "create other user PAT as text",
			cli:         "--user janedoe --scope read_registry your-pat",
			expectedOut: "sometoken\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().
					ListUsers(gomock.Any()).
					Return([]*gitlab.User{otherUser}, nil, nil)
				tc.MockPersonalAccessTokens.EXPECT().
					ListPersonalAccessTokens(gomock.Any(), gomock.Any()).
					Return([]*gitlab.PersonalAccessToken{}, noMorePages(), nil)
				tc.MockUsers.EXPECT().
					CreatePersonalAccessToken(int64(2), gomock.Any()).
					Return(testPAT, nil, nil)
			},
		},
		{
			name:     "create other user PAT as json",
			cli:      "--user janedoe --scope read_registry --output json your-pat",
			wantJSON: true,
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().
					ListUsers(gomock.Any()).
					Return([]*gitlab.User{otherUser}, nil, nil)
				tc.MockPersonalAccessTokens.EXPECT().
					ListPersonalAccessTokens(gomock.Any(), gomock.Any()).
					Return([]*gitlab.PersonalAccessToken{}, noMorePages(), nil)
				tc.MockUsers.EXPECT().
					CreatePersonalAccessToken(int64(2), gomock.Any()).
					Return(testPAT, nil, nil)
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
				NewCmdCreate,
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
			if tc.wantJSON {
				var result map[string]any
				err := json.Unmarshal(out.OutBuf.Bytes(), &result)
				require.NoError(t, err)
				assert.Equal(t, "your-pat", result["name"])
			} else if tc.expectedOut != "" {
				assert.Equal(t, tc.expectedOut, out.OutBuf.String())
			}
			assert.Empty(t, out.ErrBuf.String())
		})
	}
}

func TestCreateGroupAccessToken(t *testing.T) {
	type testCase struct {
		name        string
		cli         string
		expectedOut string
		wantJSON    bool
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
			name:        "create group token as text",
			cli:         "--group GROUP --output text --access-level developer --scope read_registry --scope read_repository my-group-token",
			expectedOut: "glpat-yz2791KMU-xxxxxxxxx\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroupAccessTokens.EXPECT().
					ListGroupAccessTokens("GROUP", gomock.Any(), gomock.Any()).
					Return([]*gitlab.GroupAccessToken{}, noMorePages(), nil)
				tc.MockGroupAccessTokens.EXPECT().
					CreateGroupAccessToken("GROUP", gomock.Any()).
					Return(testGroupToken, nil, nil)
			},
		},
		{
			name:     "create group token as json",
			cli:      "--group GROUP --output json --access-level developer --scope read_registry --scope read_repository my-group-token",
			wantJSON: true,
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroupAccessTokens.EXPECT().
					ListGroupAccessTokens("GROUP", gomock.Any(), gomock.Any()).
					Return([]*gitlab.GroupAccessToken{}, noMorePages(), nil)
				tc.MockGroupAccessTokens.EXPECT().
					CreateGroupAccessToken("GROUP", gomock.Any()).
					Return(testGroupToken, nil, nil)
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
				NewCmdCreate,
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
			if tc.wantJSON {
				var result map[string]any
				err := json.Unmarshal(out.OutBuf.Bytes(), &result)
				require.NoError(t, err)
				assert.Equal(t, "my-group-token", result["name"])
			} else if tc.expectedOut != "" {
				assert.Equal(t, tc.expectedOut, out.OutBuf.String())
			}
			assert.Empty(t, out.ErrBuf.String())
		})
	}
}

func TestCreateProjectAccessToken(t *testing.T) {
	type testCase struct {
		name        string
		cli         string
		expectedOut string
		wantJSON    bool
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
			name:        "create project token as text",
			cli:         "--output text --access-level developer --scope read_repository --scope api my-project-token",
			expectedOut: "glpat-dfsdfjksjdfslkdfjsd\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjectAccessTokens.EXPECT().
					ListProjectAccessTokens("OWNER/REPO", gomock.Any(), gomock.Any()).
					Return([]*gitlab.ProjectAccessToken{}, noMorePages(), nil)
				tc.MockProjectAccessTokens.EXPECT().
					CreateProjectAccessToken("OWNER/REPO", gomock.Any()).
					Return(testProjectToken, nil, nil)
			},
		},
		{
			name:     "create project token as json",
			cli:      "--output json --access-level developer --scope read_repository --scope api my-project-token",
			wantJSON: true,
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjectAccessTokens.EXPECT().
					ListProjectAccessTokens("OWNER/REPO", gomock.Any(), gomock.Any()).
					Return([]*gitlab.ProjectAccessToken{}, noMorePages(), nil)
				tc.MockProjectAccessTokens.EXPECT().
					CreateProjectAccessToken("OWNER/REPO", gomock.Any()).
					Return(testProjectToken, nil, nil)
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
				NewCmdCreate,
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
			if tc.wantJSON {
				var result map[string]any
				err := json.Unmarshal(out.OutBuf.Bytes(), &result)
				require.NoError(t, err)
				assert.Equal(t, "my-project-token", result["name"])
			} else if tc.expectedOut != "" {
				assert.Equal(t, tc.expectedOut, out.OutBuf.String())
			}
			assert.Empty(t, out.ErrBuf.String())
		})
	}
}
