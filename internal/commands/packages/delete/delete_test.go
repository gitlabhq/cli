//go:build !integration

package delete

import (
	"fmt"
	"testing"

	"git.sr.ht/~timofurrer/ugh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_PackagesDelete(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		cli         string
		expectedMsg []string
		wantErr     bool
		wantErrMsg  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}{
		{
			name:        "Delete package",
			cli:         "1 -y",
			expectedMsg: []string{"Deleting package repo=OWNER/REPO packageID=1", "Package 1 deleted."},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockPackages.EXPECT().
					DeleteProjectPackage("OWNER/REPO", int64(1), gomock.Any()).
					Return(nil, nil)
			},
		},
		{
			name: "Delete package but API errors",
			cli:  "1 -y",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockPackages.EXPECT().
					DeleteProjectPackage("OWNER/REPO", int64(1), gomock.Any()).
					Return(nil, fmt.Errorf("DELETE https://gitlab.com/api/v4/projects/OWNER%%2FREPO/packages/1: 403"))
			},
			wantErr:    true,
			wantErrMsg: "failed to delete package: DELETE https://gitlab.com/api/v4/projects/OWNER%2FREPO/packages/1: 403",
		},
		{
			name:       "Delete package with non-integer ID",
			cli:        "abc -y",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
			wantErr:    true,
			wantErrMsg: "package ID must be an integer: abc",
		},
		{
			name:       "Delete package non-interactively without -y",
			cli:        "1",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
			wantErr:    true,
			wantErrMsg: "--yes or -y flag is required when not running interactively",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmd,
				false,
				cmdtest.WithGitLabClient(testClient.Client),
			)

			out, err := exec(tc.cli)

			if tc.wantErr {
				require.Error(t, err)
				assert.Equal(t, tc.wantErrMsg, err.Error())
				return
			}
			require.NoError(t, err)
			for _, msg := range tc.expectedMsg {
				assert.Contains(t, out.String(), msg)
			}
		})
	}
}

func Test_PackagesDelete_CancelPrompt(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)
	// The package must never be deleted when the user declines the prompt.
	testClient.MockPackages.EXPECT().
		DeleteProjectPackage(gomock.Any(), gomock.Any(), gomock.Any()).
		Times(0)

	c := ugh.New(t)
	c.Expect(ugh.Confirm("Are you ABSOLUTELY SURE you wish to delete this package 1?")).
		Do(ugh.Reject)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		true,
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithConsole(t, c),
	)

	_, err := exec("1")
	require.ErrorIs(t, err, iostreams.ErrUserCancelled)
}
