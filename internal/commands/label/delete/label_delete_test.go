//go:build !integration

package delete

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_LabelDelete(t *testing.T) {
	type testCase struct {
		name        string
		cli         string
		expectedMsg []string
		wantErr     bool
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	testCases := []testCase{
		{
			name:        "Label delete",
			cli:         "foo",
			expectedMsg: []string{"Label deleted"},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockLabels.EXPECT().
					DeleteLabel("OWNER/REPO", "foo", gomock.Any()).
					Return(nil, nil)
			},
		},
		{
			name:       "Label delete error",
			cli:        "nonexistent",
			wantErr:    true,
			wantStderr: "404 Not Found",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockLabels.EXPECT().
					DeleteLabel("OWNER/REPO", "nonexistent", gomock.Any()).
					Return(nil, errors.New("404 Not Found"))
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
				NewCmdDelete,
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
			for _, msg := range tc.expectedMsg {
				assert.Contains(t, out.OutBuf.String(), msg)
			}
		})
	}
}
