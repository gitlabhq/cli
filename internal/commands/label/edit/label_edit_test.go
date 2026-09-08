//go:build !integration

package edit

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_UpdateLabel(t *testing.T) {
	type testCase struct {
		Name        string
		ExpectedMsg []string
		wantErr     bool
		cli         string
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	testLabel := &gitlab.Label{
		ID:          123,
		Name:        "Example label",
		Description: "Example Description",
		Priority:    gitlab.NewNullableWithValue(int64(5)),
		Color:       "#FFFFFF",
	}

	testCases := []testCase{
		{
			Name:        "Update label color and priority",
			ExpectedMsg: []string{"Updating \"Example label\" label\nUpdated color: #FFFFFF\nUpdated priority: 5\n"},
			cli:         "--label-id=123 --color='#FFFFFF' --priority=5",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockLabels.EXPECT().UpdateLabel("OWNER/REPO", 123, gomock.Any()).Return(testLabel, nil, nil)
			},
		},
		{
			Name:       "Get label without ID",
			cli:        "",
			wantErr:    true,
			wantStderr: "required flag(s) \"label-id\" not set",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
		},
		{
			Name:        "Update both name and color",
			ExpectedMsg: []string{"Updating \"Example label\" label\nUpdated name: Example label\nUpdated color: #FFFFFF\n"},
			cli:         "--label-id=123 --color='#FFFFFF' --new-name='Example label'",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockLabels.EXPECT().UpdateLabel("OWNER/REPO", 123, gomock.Any()).Return(testLabel, nil, nil)
			},
		},
		{
			Name:        "Require at least new-name or color",
			cli:         "--label-id=123",
			ExpectedMsg: []string{"at least one of the flags in the group [new-name color] is required"},
			wantErr:     true,
			setupMock:   func(tc *gitlabtesting.TestClient) {},
		},
		{
			Name:        "Returns error when priority is not a number",
			cli:         "--label-id=123 --priority='high'",
			ExpectedMsg: []string{"invalid argument \"high\" for \"-p, --priority\" flag: strconv.ParseInt: parsing \"high\": invalid syntax"},
			wantErr:     true,
			setupMock:   func(tc *gitlabtesting.TestClient) {},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdEdit,
				false,
				cmdtest.WithGitLabClient(testClient.Client),
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
			for _, msg := range tc.ExpectedMsg {
				assert.Contains(t, out.OutBuf.String(), msg)
			}
		})
	}
}
