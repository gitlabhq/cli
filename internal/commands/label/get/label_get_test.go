//go:build !integration

package get

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_GetLabel(t *testing.T) {
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
		Color:       "#A1B2C3D4",
	}

	testCases := []testCase{
		{
			Name:        "Get label by ID",
			ExpectedMsg: []string{"Label ID\t123\nName\tExample label\nDescription\tExample Description\nColor\t#A1B2C3D4\nPriority\t5\n\n"},
			cli:         "123",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockLabels.EXPECT().GetLabel("OWNER/REPO", 123).Return(testLabel, nil, nil)
			},
		},
		{
			Name:       "Get label with API error",
			cli:        "12",
			wantErr:    true,
			wantStderr: "404 Not Found",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockLabels.EXPECT().GetLabel("OWNER/REPO", 12).Return(nil, nil, errors.New("404 Not Found"))
			},
		},
		{
			Name:       "Get label without ID",
			cli:        "",
			wantErr:    true,
			wantStderr: "accepts 1 arg(s), received 0",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdGet,
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

func TestLabelGet_JSON(t *testing.T) {
	t.Parallel()

	testLabel := &gitlab.Label{
		ID:          123,
		Name:        "Example label",
		Description: "Example Description",
		Priority:    gitlab.NewNullableWithValue(int64(5)),
		Color:       "#A1B2C3D4",
	}

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockLabels.EXPECT().
		GetLabel("OWNER/REPO", 123).
		Return(testLabel, nil, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdGet,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	out, err := exec("123 --output json")
	require.NoError(t, err)

	assert.Contains(t, out.String(), `"id":123`)
	assert.Contains(t, out.String(), `"name":"Example label"`)
	assert.Contains(t, out.String(), `"description":"Example Description"`)
	assert.Contains(t, out.String(), `"color":"#A1B2C3D4"`)
	assert.Empty(t, out.Stderr())
}
