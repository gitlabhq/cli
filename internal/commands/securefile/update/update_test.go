//go:build !integration

package update

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

const (
	repoName             = "OWNER/REPO"
	fileContentsChecksum = "185f8db32271fe25f561a6fc938b2e264306ec304eda518007d1764826381969"
)

func Test_SecurefileUpdate(t *testing.T) {
	type testCase struct {
		name        string
		cli         string
		expectedMsg []string
		wantErr     bool
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	createdAt, _ := time.Parse(time.RFC3339, "2022-02-22T22:22:22Z")

	testCases := []testCase{
		{
			name: "Update securefile",
			cli:  "existing.txt testdata/localfile.txt -y",
			expectedMsg: []string{
				"• Updating secure file repo=OWNER/REPO fileName=existing.txt",
				"✓ Secure file existing.txt updated.",
				"The secure file ID changed to 2. Any scripts using `securefile download --id` must be updated, or switch to `securefile download --name existing.txt`.",
			},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockSecureFiles.EXPECT().
					ListProjectSecureFiles(repoName, &gitlab.ListProjectSecureFilesOptions{
						ListOptions: gitlab.ListOptions{
							Page:    1,
							PerPage: 100,
						},
					}, nil).
					Return([]*gitlab.SecureFile{
						{ID: 1, Name: "existing.txt", Checksum: fileContentsChecksum},
					}, nil, nil)
				tc.MockSecureFiles.EXPECT().
					RemoveSecureFile(repoName, int64(1)).
					Return(nil, nil)
				tc.MockSecureFiles.EXPECT().
					CreateSecureFile(repoName, gomock.Any(), gomock.Any()).
					Return(&gitlab.SecureFile{
						ID:                2,
						Name:              "existing.txt",
						Checksum:          fileContentsChecksum,
						ChecksumAlgorithm: "sha256",
						CreatedAt:         &createdAt,
						ExpiresAt:         nil,
						Metadata:          nil,
					}, nil, nil)
			},
		},
		{
			name:        "Skip update when checksum matches",
			cli:         "existing.txt testdata/localfile.txt -y",
			expectedMsg: []string{"✓ Secure file existing.txt is already up to date."},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockSecureFiles.EXPECT().
					ListProjectSecureFiles(repoName, &gitlab.ListProjectSecureFilesOptions{
						ListOptions: gitlab.ListOptions{
							Page:    1,
							PerPage: 100,
						},
					}, nil).
					Return([]*gitlab.SecureFile{
						{ID: 1, Name: "existing.txt", Checksum: fileContentsChecksum, ChecksumAlgorithm: "sha256"},
					}, nil, nil)
			},
		},
		{
			name:       "Update a securefile with invalid file path",
			cli:        "newfile.txt testdata/missingfile.txt -y",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
			wantErr:    true,
			wantStderr: "unable to read file at testdata/missingfile.txt: open testdata/missingfile.txt: no such file or directory",
		},
		{
			name:       "Attempt to update non-existent file",
			cli:        "newfile.txt testdata/localfile.txt -y",
			wantErr:    true,
			wantStderr: "couldn't locate secure file with name newfile.txt",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockSecureFiles.EXPECT().
					ListProjectSecureFiles(repoName, &gitlab.ListProjectSecureFilesOptions{
						ListOptions: gitlab.ListOptions{
							Page:    1,
							PerPage: 100,
						},
					}, nil).
					Return([]*gitlab.SecureFile{
						{ID: 1, Name: "otherfile.txt", Checksum: fileContentsChecksum},
					}, &gitlab.Response{}, nil)
			},
		},
		{
			name:       "Error when removing existing securefile",
			cli:        "existing.txt testdata/localfile.txt -y",
			wantErr:    true,
			wantStderr: "error removing secure file: DELETE https://gitlab.com/api/v4/projects/OWNER%2FREPO/secure_files/1: 400",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockSecureFiles.EXPECT().
					ListProjectSecureFiles(repoName, &gitlab.ListProjectSecureFilesOptions{
						ListOptions: gitlab.ListOptions{
							Page:    1,
							PerPage: 100,
						},
					}, nil).
					Return([]*gitlab.SecureFile{
						{ID: 1, Name: "existing.txt", Checksum: fileContentsChecksum},
					}, nil, nil)
				tc.MockSecureFiles.EXPECT().
					RemoveSecureFile(repoName, int64(1)).
					Return(nil, fmt.Errorf("DELETE https://gitlab.com/api/v4/projects/OWNER%%2FREPO/secure_files/1: 400"))
			},
		},
		{
			name:       "Error when creating securefile",
			cli:        "existing.txt testdata/localfile.txt -y",
			wantErr:    true,
			wantStderr: `the existing secure file "existing.txt" was removed but the new version could not be uploaded, so it must be re-created manually: POST https://gitlab.com/api/v4/projects/OWNER%2FREPO/secure_files: 400`,
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockSecureFiles.EXPECT().
					ListProjectSecureFiles(repoName, &gitlab.ListProjectSecureFilesOptions{
						ListOptions: gitlab.ListOptions{
							Page:    1,
							PerPage: 100,
						},
					}, nil).
					Return([]*gitlab.SecureFile{
						{ID: 1, Name: "existing.txt", Checksum: fileContentsChecksum},
					}, nil, nil)
				tc.MockSecureFiles.EXPECT().
					RemoveSecureFile(repoName, int64(1)).
					Return(nil, nil)
				tc.MockSecureFiles.EXPECT().
					CreateSecureFile(repoName, gomock.Any(), gomock.Any()).
					Return(nil, nil, fmt.Errorf("POST https://gitlab.com/api/v4/projects/OWNER%%2FREPO/secure_files: 400"))
			},
		},
		{
			name:       "Update a secure file without force update when not running interactively",
			cli:        "file.txt testdata/localfile.txt",
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
				NewCmdUpdate,
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
