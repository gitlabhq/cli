//go:build !integration

package upload

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_PackagesUpload(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		cli         string
		expectedMsg []string
		wantErr     bool
		wantErrMsg  string
		errContains string
		setupMock   func(tc *gitlabtesting.TestClient)
	}{
		{
			name: "Upload package file",
			cli:  "testdata/localfile.txt --name my-package --version 1.0.0",
			expectedMsg: []string{
				"Uploading package file repo=OWNER/REPO package=my-package version=1.0.0 file=localfile.txt",
				"Package file localfile.txt uploaded.",
				"url https://gitlab.com/api/v4/projects/OWNER%2FREPO/packages/generic/my-package/1.0.0/localfile.txt",
				"sha256 abc123",
			},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGenericPackages.EXPECT().
					PublishPackageFile("OWNER/REPO", "my-package", "1.0.0", "localfile.txt", gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&gitlab.GenericPackagesFile{ID: 1, FileName: "localfile.txt", FileSHA256: "abc123"}, nil, nil)
				tc.MockGenericPackages.EXPECT().
					FormatPackageURL("OWNER/REPO", "my-package", "1.0.0", "localfile.txt").
					Return("projects/OWNER%2FREPO/packages/generic/my-package/1.0.0/localfile.txt", nil)
			},
		},
		{
			name: "Upload package file with custom filename",
			cli:  "testdata/localfile.txt --name my-package --version 1.0.0 --filename renamed.txt",
			expectedMsg: []string{
				"Package file renamed.txt uploaded.",
				"url https://gitlab.com/api/v4/projects/OWNER%2FREPO/packages/generic/my-package/1.0.0/renamed.txt",
				"sha256 abc123",
			},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGenericPackages.EXPECT().
					PublishPackageFile("OWNER/REPO", "my-package", "1.0.0", "renamed.txt", gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&gitlab.GenericPackagesFile{ID: 1, FileName: "renamed.txt", FileSHA256: "abc123"}, nil, nil)
				tc.MockGenericPackages.EXPECT().
					FormatPackageURL("OWNER/REPO", "my-package", "1.0.0", "renamed.txt").
					Return("projects/OWNER%2FREPO/packages/generic/my-package/1.0.0/renamed.txt", nil)
			},
		},
		{
			name: "Upload package file but API errors",
			cli:  "testdata/localfile.txt --name my-package --version 1.0.0",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGenericPackages.EXPECT().
					PublishPackageFile("OWNER/REPO", "my-package", "1.0.0", "localfile.txt", gomock.Any(), gomock.Any(), gomock.Any()).
					Return(nil, nil, fmt.Errorf("PUT https://gitlab.com/api/v4/projects/OWNER%%2FREPO/packages/generic: 400"))
			},
			wantErr:    true,
			wantErrMsg: "failed to upload package file: PUT https://gitlab.com/api/v4/projects/OWNER%2FREPO/packages/generic: 400",
		},
		{
			name:       "Upload with invalid file path",
			cli:        "testdata/missingfile.txt --name my-package --version 1.0.0",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
			wantErr:    true,
			wantErrMsg: "unable to read file at testdata/missingfile.txt: open testdata/missingfile.txt: no such file or directory",
		},
		{
			name:        "Upload without required flags",
			cli:         "testdata/localfile.txt",
			setupMock:   func(tc *gitlabtesting.TestClient) {},
			wantErr:     true,
			errContains: "not set",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			testClient := gitlabtesting.NewTestClient(t, gitlab.WithBaseURL("https://"+glinstance.DefaultHostname))
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
				if tc.errContains != "" {
					assert.Contains(t, err.Error(), tc.errContains)
				} else {
					assert.Equal(t, tc.wantErrMsg, err.Error())
				}
				return
			}
			require.NoError(t, err)
			for _, msg := range tc.expectedMsg {
				assert.Contains(t, out.String(), msg)
			}
		})
	}
}
