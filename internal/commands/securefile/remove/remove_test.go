//go:build !integration

package remove

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

const repoName = "OWNER/REPO"

func Test_SecurefileRemove(t *testing.T) {
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
			name:       "Conflicting id argument and --id flag is rejected",
			cli:        "1 --id 2 -y",
			wantErr:    true,
			wantStderr: `the secure file ID argument "1" cannot be combined with --id or --name`,
			// No API call: the request used to go out for --id (2), silently
			// ignoring the positional argument (1).
			setupMock: func(tc *gitlabtesting.TestClient) {},
		},
		{
			name:       "Conflicting id argument and --name flag is rejected",
			cli:        "1 --name file.txt -y",
			wantErr:    true,
			wantStderr: `the secure file ID argument "1" cannot be combined with --id or --name`,
			setupMock:  func(tc *gitlabtesting.TestClient) {},
		},
		{
			name:        "Remove a secure file via id arg",
			cli:         "1 -y",
			expectedMsg: []string{"• Deleting secure file repo=OWNER/REPO fileID=1", "✓ Secure file 1 deleted."},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockSecureFiles.EXPECT().
					RemoveSecureFile(repoName, int64(1)).
					Return(nil, nil)
			},
		},
		{
			name:        "Remove a secure file via id flag",
			cli:         "--id 1 -y",
			expectedMsg: []string{"• Deleting secure file repo=OWNER/REPO fileID=1", "✓ Secure file 1 deleted."},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockSecureFiles.EXPECT().
					RemoveSecureFile(repoName, int64(1)).
					Return(nil, nil)
			},
		},
		{
			name:        "Remove a secure file via name flag",
			cli:         "--name file2.txt -y",
			expectedMsg: []string{"• Deleting secure file repo=OWNER/REPO fileID=2", "✓ Secure file 2 deleted."},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockSecureFiles.EXPECT().
					ListProjectSecureFiles(repoName, &gitlab.ListProjectSecureFilesOptions{
						ListOptions: gitlab.ListOptions{Page: 1, PerPage: 100},
					}, nil).
					Return([]*gitlab.SecureFile{
						{ID: 1, Name: "file1.txt"},
						{ID: 2, Name: "file2.txt"},
					}, &gitlab.Response{}, nil)
				tc.MockSecureFiles.EXPECT().
					RemoveSecureFile(repoName, int64(2)).
					Return(nil, nil)
			},
		},
		{
			name: "Remove a secure file but API errors via id arg",
			cli:  "1 -y",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockSecureFiles.EXPECT().
					RemoveSecureFile("OWNER/REPO", int64(1)).
					Return(nil, fmt.Errorf("DELETE https://gitlab.com/api/v4/projects/OWNER%%2FREPO/secure_files/1: 400"))
			},
			wantErr:    true,
			wantStderr: "error removing secure file: DELETE https://gitlab.com/api/v4/projects/OWNER%2FREPO/secure_files/1: 400",
		},
		{
			name:       "Remove a secure file with invalid file ID via id arg",
			cli:        "abc -y",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
			wantErr:    true,
			wantStderr: "secure file ID must be an integer: abc",
		},
		{
			name:       "Remove a secure file without force delete when not running interactively",
			cli:        "1",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
			wantErr:    true,
			wantStderr: "--yes or -y flag is required when not running interactively",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdRemove,
				false,
				cmdtest.WithGitLabClient(testClient.Client),
			)

			// WHEN
			out, err := exec(tc.cli)

			// THEN
			if tc.wantErr {
				require.Error(t, err)
				assert.Equal(t, tc.wantStderr, err.Error())
				return
			}
			require.NoError(t, err)
			for _, msg := range tc.expectedMsg {
				assert.Contains(t, out.String(), msg)
			}
		})
	}
}
