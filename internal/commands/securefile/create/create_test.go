//go:build !integration

package create

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

func Test_SecurefileCreate(t *testing.T) {
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
			name:        "Create securefile",
			cli:         "newfile.txt testdata/localfile.txt",
			expectedMsg: []string{"• Creating secure file repo=OWNER/REPO fileName=newfile.txt", "✓ Secure file newfile.txt created."},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockSecureFiles.EXPECT().
					CreateSecureFile("OWNER/REPO", gomock.Any(), gomock.Any()).
					Return(&gitlab.SecureFile{
						ID:                1,
						Name:              "newfile.txt",
						Checksum:          "16630b189ab34b2e3504f4758e1054d2e478deda510b2b08cc0ef38d12e80aac",
						ChecksumAlgorithm: "sha256",
						CreatedAt:         &createdAt,
						ExpiresAt:         nil,
						Metadata:          nil,
					}, nil, nil)
			},
		},
		{
			name: "Create securefile but API errors",
			cli:  "newfile.txt testdata/localfile.txt",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockSecureFiles.EXPECT().
					CreateSecureFile("OWNER/REPO", gomock.Any(), gomock.Any()).
					Return(nil, nil, fmt.Errorf("POST https://gitlab.com/api/v4/projects/OWNER%%2FREPO/secure_files: 400"))
			},
			wantErr:    true,
			wantStderr: "error creating secure file: POST https://gitlab.com/api/v4/projects/OWNER%2FREPO/secure_files: 400",
		},
		{
			name:       "Get a securefile with invalid file path",
			cli:        "newfile.txt testdata/missingfile.txt",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
			wantErr:    true,
			wantStderr: "unable to read file at testdata/missingfile.txt: open testdata/missingfile.txt: no such file or directory",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdCreate,
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
