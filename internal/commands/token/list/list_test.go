//go:build !integration

package list

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

func TestListProjectAccessToken(t *testing.T) {
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
			ID:          10179584,
			UserID:      21973696,
			Name:        "sadfsdfsdf",
			Description: "example description",
			Scopes:      []string{"api", "read_api"},
			CreatedAt:   parseTime("2024-07-07T07:59:35.767Z"),
			ExpiresAt:   new(gitlab.ISOTime(*parseTime("2024-08-06T00:00:00Z"))),
			Active:      true,
			Revoked:     false,
		},
		AccessLevel: gitlab.GuestPermissions,
	}

	testCases := []testCase{
		{
			name:        "list project access token as text",
			cli:         "",
			expectedOut: "ID       NAME       DESCRIPTION         ACCESS_LEVEL ACTIVE  REVOKED  CREATED_AT           EXPIRES_AT LAST_USED_AT SCOPES      \n10179584 sadfsdfsdf example description guest        true    false    2024-07-07T07:59:35Z 2024-08-06 -           api,read_api\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjectAccessTokens.EXPECT().
					ListProjectAccessTokens("OWNER/REPO", gomock.Any(), gomock.Any()).
					Return([]*gitlab.ProjectAccessToken{testProjectToken}, noMorePages(), nil)
			},
		},
		{
			name: "list project access token as json",
			cli:  "--output json",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjectAccessTokens.EXPECT().
					ListProjectAccessTokens("OWNER/REPO", gomock.Any(), gomock.Any()).
					Return([]*gitlab.ProjectAccessToken{testProjectToken}, noMorePages(), nil)
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
				NewCmdList,
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
			if tc.expectedOut != "" {
				assert.Equal(t, tc.expectedOut, out.OutBuf.String())
			}
			if tc.cli == "--output json" {
				// For JSON output, verify it's valid JSON
				var result []map[string]any
				err := json.Unmarshal(out.OutBuf.Bytes(), &result)
				require.NoError(t, err)
				assert.Len(t, result, 1)
			}
			assert.Empty(t, out.ErrBuf.String())
		})
	}
}

func TestListGroupAccessToken(t *testing.T) {
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
			ID:          10179685,
			UserID:      21973881,
			Name:        "sadfsdfsdf",
			Description: "example description",
			Scopes:      []string{"read_api"},
			CreatedAt:   parseTime("2024-07-07T08:41:16.287Z"),
			ExpiresAt:   new(gitlab.ISOTime(*parseTime("2024-08-06T00:00:00Z"))),
			Active:      true,
			Revoked:     false,
		},
		AccessLevel: gitlab.GuestPermissions,
	}

	testCases := []testCase{
		{
			name:        "list group access token as text",
			cli:         "--group GROUP",
			expectedOut: "ID       NAME       DESCRIPTION         ACCESS_LEVEL ACTIVE  REVOKED  CREATED_AT           EXPIRES_AT LAST_USED_AT SCOPES  \n10179685 sadfsdfsdf example description guest        true    false    2024-07-07T08:41:16Z 2024-08-06 -           read_api\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroupAccessTokens.EXPECT().
					ListGroupAccessTokens("GROUP", gomock.Any(), gomock.Any()).
					Return([]*gitlab.GroupAccessToken{testGroupToken}, noMorePages(), nil)
			},
		},
		{
			name: "list group access token as json",
			cli:  "--group GROUP --output json",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroupAccessTokens.EXPECT().
					ListGroupAccessTokens("GROUP", gomock.Any(), gomock.Any()).
					Return([]*gitlab.GroupAccessToken{testGroupToken}, noMorePages(), nil)
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
				NewCmdList,
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
			if tc.expectedOut != "" {
				assert.Equal(t, tc.expectedOut, out.OutBuf.String())
			}
			if tc.cli == "--group GROUP --output json" {
				// For JSON output, verify it's valid JSON
				var result []map[string]any
				err := json.Unmarshal(out.OutBuf.Bytes(), &result)
				require.NoError(t, err)
				assert.Len(t, result, 1)
			}
			assert.Empty(t, out.ErrBuf.String())
		})
	}
}

func TestListPersonalAccessToken(t *testing.T) {
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

	testPATs := []*gitlab.PersonalAccessToken{
		{
			ID:          9860015,
			Name:        "awsssm",
			Description: "example description 1",
			Scopes:      []string{"api"},
			CreatedAt:   parseTime("2024-05-29T07:25:56.846Z"),
			ExpiresAt:   new(gitlab.ISOTime(*parseTime("2024-06-28T00:00:00Z"))),
			UserID:      926857,
			Active:      false,
			Revoked:     false,
		},
		{
			ID:          9860076,
			Name:        "glab",
			Description: "example description 2",
			Scopes:      []string{"api"},
			CreatedAt:   parseTime("2024-05-29T07:34:14.044Z"),
			ExpiresAt:   new(gitlab.ISOTime(*parseTime("2024-06-28T00:00:00Z"))),
			UserID:      926857,
			LastUsedAt:  parseTime("2024-06-05T17:32:34.466Z"),
			Active:      false,
			Revoked:     false,
		},
		{
			ID:          10171440,
			Name:        "api",
			Description: "example description 3",
			Scopes:      []string{"api"},
			CreatedAt:   parseTime("2024-07-05T10:02:37.182Z"),
			ExpiresAt:   new(gitlab.ISOTime(*parseTime("2024-08-04T00:00:00Z"))),
			UserID:      926857,
			LastUsedAt:  parseTime("2024-07-07T20:02:49.595Z"),
			Active:      true,
			Revoked:     false,
		},
	}

	testCases := []testCase{
		{
			name:        "list personal access tokens as text",
			cli:         "--user @me",
			expectedOut: "ID       NAME   DESCRIPTION           ACCESS_LEVEL ACTIVE  REVOKED  CREATED_AT           EXPIRES_AT LAST_USED_AT         SCOPES \n9860015  awsssm example description 1 -            false   false    2024-05-29T07:25:56Z 2024-06-28 -                    api    \n9860076  glab   example description 2 -            false   false    2024-05-29T07:34:14Z 2024-06-28 2024-06-05T17:32:34Z api    \n10171440 api    example description 3 -            true    false    2024-07-05T10:02:37Z 2024-08-04 2024-07-07T20:02:49Z api    \n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(testUser, nil, nil)
				tc.MockPersonalAccessTokens.EXPECT().
					ListPersonalAccessTokens(gomock.Any(), gomock.Any()).
					Return(testPATs, noMorePages(), nil)
			},
		},
		{
			name:        "list active personal access tokens only",
			cli:         "--user @me --active",
			expectedOut: "ID       NAME  DESCRIPTION           ACCESS_LEVEL ACTIVE  REVOKED  CREATED_AT           EXPIRES_AT LAST_USED_AT         SCOPES \n10171440 api   example description 3 -            true    false    2024-07-05T10:02:37Z 2024-08-04 2024-07-07T20:02:49Z api    \n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(testUser, nil, nil)
				tc.MockPersonalAccessTokens.EXPECT().
					ListPersonalAccessTokens(gomock.Any(), gomock.Any()).
					Return(testPATs, noMorePages(), nil)
			},
		},
		{
			name: "list personal access tokens as json",
			cli:  "--user @me --output json",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(testUser, nil, nil)
				tc.MockPersonalAccessTokens.EXPECT().
					ListPersonalAccessTokens(gomock.Any(), gomock.Any()).
					Return(testPATs, noMorePages(), nil)
			},
		},
		{
			name: "list active personal access tokens as json",
			cli:  "--user @me --active --output json",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(testUser, nil, nil)
				tc.MockPersonalAccessTokens.EXPECT().
					ListPersonalAccessTokens(gomock.Any(), gomock.Any()).
					Return(testPATs, noMorePages(), nil)
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
				NewCmdList,
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
			if tc.expectedOut != "" {
				assert.Equal(t, tc.expectedOut, out.OutBuf.String())
			}
			if tc.cli == "--user @me --output json" {
				// For JSON output, verify it's valid JSON
				var result []map[string]any
				err := json.Unmarshal(out.OutBuf.Bytes(), &result)
				require.NoError(t, err)
				assert.Len(t, result, 3)
			}
			if tc.cli == "--user @me --active --output json" {
				// For JSON output with --active flag, verify only active tokens are returned
				var result []map[string]any
				err := json.Unmarshal(out.OutBuf.Bytes(), &result)
				require.NoError(t, err)
				assert.Len(t, result, 1, "should only return 1 active token")
				// Verify the returned token is the active one
				assert.Equal(t, "10171440", result[0]["id"])
				assert.Equal(t, "true", result[0]["active"])
			}
			assert.Empty(t, out.ErrBuf.String())
		})
	}
}

func TestListPersonalAccessTokenWithoutExpiration(t *testing.T) {
	testUser := &gitlab.User{
		ID:       1,
		Username: "johndoe",
		Name:     "John Doe",
		Email:    "john.doe@acme.com",
	}

	testPATs := []*gitlab.PersonalAccessToken{
		{
			ID:        1,
			Name:      "awsssm",
			Scopes:    []string{"api"},
			CreatedAt: parseTime("2024-05-29T07:25:56.846Z"),
			ExpiresAt: nil, // no expiration
			UserID:    926857,
			Active:    false,
			Revoked:   false,
		},
		{
			ID:         2,
			Name:       "glab",
			Scopes:     []string{"api"},
			CreatedAt:  parseTime("2024-05-29T07:34:14.044Z"),
			ExpiresAt:  new(gitlab.ISOTime(*parseTime("2024-06-28T00:00:00Z"))),
			UserID:     926857,
			LastUsedAt: parseTime("2024-06-05T17:32:34.466Z"),
			Active:     false,
			Revoked:    false,
		},
		{
			ID:         3,
			Name:       "api",
			Scopes:     []string{"api"},
			CreatedAt:  parseTime("2024-07-05T10:02:37.182Z"),
			ExpiresAt:  new(gitlab.ISOTime(*parseTime("2024-08-04T00:00:00Z"))),
			UserID:     926857,
			LastUsedAt: parseTime("2024-07-07T20:02:49.595Z"),
			Active:     true,
			Revoked:    false,
		},
	}

	// GIVEN
	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockUsers.EXPECT().
		CurrentUser(gomock.Any()).
		Return(testUser, nil, nil)
	testClient.MockPersonalAccessTokens.EXPECT().
		ListPersonalAccessTokens(gomock.Any(), gomock.Any()).
		Return(testPATs, noMorePages(), nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdList,
		true,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
	)

	// WHEN
	out, err := exec("--user @me")

	// THEN
	require.NoError(t, err)
	assert.Equal(t, "ID  NAME   DESCRIPTION  ACCESS_LEVEL ACTIVE  REVOKED  CREATED_AT           EXPIRES_AT LAST_USED_AT         SCOPES \n1   awsssm -            -            false   false    2024-05-29T07:25:56Z -          -                    api    \n2   glab   -            -            false   false    2024-05-29T07:34:14Z 2024-06-28 2024-06-05T17:32:34Z api    \n3   api    -            -            true    false    2024-07-05T10:02:37Z 2024-08-04 2024-07-07T20:02:49Z api    \n", out.OutBuf.String())
	assert.Empty(t, out.ErrBuf.String())
}
