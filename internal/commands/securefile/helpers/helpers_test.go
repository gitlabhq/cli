//go:build !integration

package helpers

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"
)

const repoName = "OWNER/REPO"

func Test_GetSecureFileByName(t *testing.T) {
	type testCase struct {
		name       string
		fileName   string
		wantID     int64
		wantErr    bool
		wantErrMsg string
		setupMock  func(tc *gitlabtesting.TestClient)
	}

	testCases := []testCase{
		{
			name:     "secure file found",
			fileName: "file2.txt",
			wantID:   2,
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockSecureFiles.EXPECT().
					ListProjectSecureFiles(repoName, &gitlab.ListProjectSecureFilesOptions{
						ListOptions: gitlab.ListOptions{
							Page:    1,
							PerPage: 100,
						},
					}, nil).
					Return([]*gitlab.SecureFile{
						{ID: 1, Name: "file1.txt"},
						{ID: 2, Name: "file2.txt"},
					}, &gitlab.Response{}, nil)
			},
		},
		{
			name:       "secure file not found",
			fileName:   "missing.txt",
			wantErr:    true,
			wantErrMsg: "couldn't locate secure file with name missing.txt",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockSecureFiles.EXPECT().
					ListProjectSecureFiles(repoName, &gitlab.ListProjectSecureFilesOptions{
						ListOptions: gitlab.ListOptions{
							Page:    1,
							PerPage: 100,
						},
					}, nil).
					Return([]*gitlab.SecureFile{
						{ID: 1, Name: "file1.txt"},
						{ID: 2, Name: "file2.txt"},
					}, &gitlab.Response{}, nil)
			},
		},
		{
			name:     "gitlab API error",
			fileName: "file1.txt",
			wantErr:  true,
			wantErrMsg: "error fetching secure files: " +
				"GET https://gitlab.com/api/v4/projects/OWNER%2FREPO/secure_files: 500",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockSecureFiles.EXPECT().
					ListProjectSecureFiles(repoName, &gitlab.ListProjectSecureFilesOptions{
						ListOptions: gitlab.ListOptions{
							Page:    1,
							PerPage: 100,
						},
					}, nil).
					Return(nil, &gitlab.Response{}, fmt.Errorf(
						"GET https://gitlab.com/api/v4/projects/OWNER%%2FREPO/secure_files: 500",
					))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)

			// WHEN
			secureFile, err := GetSecureFileByName(testClient.Client, tc.fileName, repoName)

			// THEN
			if tc.wantErr {
				require.Error(t, err)
				assert.Equal(t, tc.wantErrMsg, err.Error())
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.wantID, secureFile.ID)
		})
	}
}
