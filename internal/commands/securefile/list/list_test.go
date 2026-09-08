//go:build !integration

package list

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_SecurefileList(t *testing.T) {
	type testCase struct {
		name        string
		cli         string
		expectedMsg []string
		wantErr     bool
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	createdAt, _ := time.Parse(time.RFC3339, "2022-02-22T22:22:22Z")

	testSecureFile := &gitlab.SecureFile{
		ID:                1,
		Name:              "myfile.jks",
		Checksum:          "16630b189ab34b2e3504f4758e1054d2e478deda510b2b08cc0ef38d12e80aac",
		ChecksumAlgorithm: "sha256",
		CreatedAt:         &createdAt,
		ExpiresAt:         nil,
		Metadata:          nil,
	}

	testCases := []testCase{
		{
			name:        "List securefiles",
			cli:         "",
			expectedMsg: []string{`[{"id":1,"name":"myfile.jks","checksum":"16630b189ab34b2e3504f4758e1054d2e478deda510b2b08cc0ef38d12e80aac","checksum_algorithm":"sha256","created_at":"2022-02-22T22:22:22Z","expires_at":null,"metadata":null}]`},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockSecureFiles.EXPECT().
					ListProjectSecureFiles("OWNER/REPO", gomock.Any()).
					Return([]*gitlab.SecureFile{testSecureFile}, nil, nil)
			},
		},
		{
			name:        "Get a securefile with custom pagination values",
			cli:         "--page 2 --per-page 10",
			expectedMsg: []string{`[{"id":1,"name":"myfile.jks","checksum":"16630b189ab34b2e3504f4758e1054d2e478deda510b2b08cc0ef38d12e80aac","checksum_algorithm":"sha256","created_at":"2022-02-22T22:22:22Z","expires_at":null,"metadata":null}]`},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockSecureFiles.EXPECT().
					ListProjectSecureFiles("OWNER/REPO", gomock.Any()).
					Return([]*gitlab.SecureFile{testSecureFile}, nil, nil)
			},
		},
		{
			name:        "Get a securefile with page defaults per page number",
			cli:         "--page 2",
			expectedMsg: []string{`[{"id":1,"name":"myfile.jks","checksum":"16630b189ab34b2e3504f4758e1054d2e478deda510b2b08cc0ef38d12e80aac","checksum_algorithm":"sha256","created_at":"2022-02-22T22:22:22Z","expires_at":null,"metadata":null}]`},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockSecureFiles.EXPECT().
					ListProjectSecureFiles("OWNER/REPO", gomock.Any()).
					Return([]*gitlab.SecureFile{testSecureFile}, nil, nil)
			},
		},
		{
			name:        "Get a securefile with per page defaults page number",
			cli:         "--per-page 10",
			expectedMsg: []string{`[{"id":1,"name":"myfile.jks","checksum":"16630b189ab34b2e3504f4758e1054d2e478deda510b2b08cc0ef38d12e80aac","checksum_algorithm":"sha256","created_at":"2022-02-22T22:22:22Z","expires_at":null,"metadata":null}]`},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockSecureFiles.EXPECT().
					ListProjectSecureFiles("OWNER/REPO", gomock.Any()).
					Return([]*gitlab.SecureFile{testSecureFile}, nil, nil)
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
				assert.Contains(t, out.OutBuf.String(), msg)
			}
		})
	}
}
