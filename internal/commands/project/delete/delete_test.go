//go:build !integration

package delete

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_ProjectDelete(t *testing.T) {
	type testCase struct {
		name           string
		cli            string
		expectedOutput string
		wantErr        bool
		wantStderr     string
		setupMock      func(tc *gitlabtesting.TestClient)
	}

	testCases := []testCase{
		{
			name:           "delete my project",
			cli:            "--yes",
			expectedOutput: "- Deleting project OWNER/REPO\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					DeleteProject("OWNER/REPO", nil).
					Return(&gitlab.Response{Response: &http.Response{StatusCode: http.StatusAccepted}}, nil)
			},
		},
		{
			name:           "delete project",
			cli:            "foo/bar --yes",
			expectedOutput: "- Deleting project foo/bar\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					DeleteProject("foo/bar", nil).
					Return(&gitlab.Response{Response: &http.Response{StatusCode: http.StatusAccepted}}, nil)
			},
		},
		{
			name:           "delete group's project",
			cli:            "group/foo/bar --yes",
			expectedOutput: "- Deleting project group/foo/bar\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					DeleteProject("group/foo/bar", nil).
					Return(&gitlab.Response{Response: &http.Response{StatusCode: http.StatusAccepted}}, nil)
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
				true,
				cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", glinstance.DefaultHostname, api.WithGitLabClient(testClient.Client))),
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
			assert.Equal(t, tc.expectedOutput, out.Stderr())
			assert.Empty(t, out.String())
		})
	}
}
