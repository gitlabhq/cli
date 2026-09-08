//go:build !integration

package list

import (
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestLabelList(t *testing.T) {
	type testCase struct {
		name        string
		cli         string
		expectedOut string
		wantErr     bool
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	testLabels := []*gitlab.Label{
		{
			ID:          1,
			Name:        "bug",
			Description: "",
			Color:       "#6699cc",
		},
		{
			ID:          2,
			Name:        "ux",
			Description: "User Experience",
			Color:       "#3cb371",
		},
	}

	testCases := []testCase{
		{
			name: "List project labels",
			cli:  "",
			expectedOut: heredoc.Doc(`Showing label 2 of 2 on OWNER/REPO.

			ID	Name	Description	Color
			1	bug		#6699cc
			2	ux	User Experience	#3cb371

			`),
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockLabels.EXPECT().
					ListLabels("OWNER/REPO", gomock.Any()).
					Return(testLabels, nil, nil)
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
			assert.Equal(t, tc.expectedOut, out.OutBuf.String())
			assert.Empty(t, out.ErrBuf.String())
		})
	}
}

func TestLabelListJSON(t *testing.T) {
	testLabels := []*gitlab.Label{
		{
			ID:                     29739671,
			Name:                   "my label",
			Color:                  "#00b140",
			TextColor:              "#FFFFFF",
			Description:            "Simple label",
			OpenIssuesCount:        0,
			ClosedIssuesCount:      0,
			OpenMergeRequestsCount: 0,
			Subscribed:             false,
			Priority:               gitlab.NewNullableWithValue(int64(0)),
			IsProjectLabel:         true,
		},
	}

	expectedBody := `[
    {
        "id": 29739671,
        "name": "my label",
        "color": "#00b140",
        "text_color": "#FFFFFF",
        "description": "Simple label",
        "open_issues_count": 0,
        "closed_issues_count": 0,
        "open_merge_requests_count": 0,
        "subscribed": false,
        "priority": 0,
        "is_project_label": true,
        "archived": false
    }
]`

	// GIVEN
	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockLabels.EXPECT().
		ListLabels("OWNER/REPO", gomock.Any()).
		Return(testLabels, nil, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdList,
		true,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
	)

	// WHEN
	output, err := exec("-F json")

	// THEN
	require.NoError(t, err)
	assert.JSONEq(t, expectedBody, output.OutBuf.String())
	assert.Empty(t, output.ErrBuf.String())
}

func TestGroupLabelList(t *testing.T) {
	testLabels := []*gitlab.GroupLabel{
		{
			ID:          1,
			Name:        "groupbug",
			Description: "",
			Color:       "#6699cc",
		},
		{
			ID:          2,
			Name:        "groupux",
			Description: "User Experience",
			Color:       "#3cb371",
		},
	}

	expectedOut := heredoc.Doc(`Showing label 2 of 2 for group foo.

	ID	Name	Description	Color
	1	groupbug		#6699cc
	2	groupux	User Experience	#3cb371

	`)

	// GIVEN
	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockGroupLabels.EXPECT().
		ListGroupLabels("foo", gomock.Any()).
		Return(testLabels, nil, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdList,
		true,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
	)

	// WHEN
	output, err := exec("--group foo")

	// THEN
	require.NoError(t, err)
	assert.Equal(t, expectedOut, output.OutBuf.String())
	assert.Empty(t, output.ErrBuf.String())
}
