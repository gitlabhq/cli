//go:build !integration

package delete

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_DeleteSSHKey(t *testing.T) {
	type testCase struct {
		Name        string
		ExpectedMsg []string
		wantErr     bool
		cli         string
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	testCases := []testCase{
		{
			Name:        "Delete SSH key by ID",
			ExpectedMsg: []string{"SSH key deleted.\n"},
			cli:         "123",
			wantErr:     false,
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().DeleteSSHKey(int64(123)).Return(nil, nil)
			},
		},
		{
			Name:       "Delete SSH key with non numeric ID",
			cli:        "abc",
			wantErr:    true,
			wantStderr: "404",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().DeleteSSHKey(int64(0)).Return(nil, errors.New("404 Not Found"))
			},
		},
		{
			Name:       "Delete non existent SSH key",
			cli:        "999",
			wantErr:    true,
			wantStderr: "404",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().DeleteSSHKey(int64(999)).Return(nil, errors.New("404 Not found"))
			},
		},
		{
			Name:       "Delete SSH key without ID",
			cli:        "",
			wantErr:    true,
			wantStderr: "the <key-id> argument is required when prompts are disabled",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
		},
		{
			Name:       "Delete SSH key with unauthorized error",
			cli:        "123",
			wantErr:    true,
			wantStderr: "401",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().DeleteSSHKey(int64(123)).Return(nil, errors.New("401 Unauthorized"))
			},
		},
		{
			Name:       "Explicit zero ID returns not found",
			cli:        "0",
			wantErr:    true,
			wantStderr: "404 Not found",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().DeleteSSHKey(int64(0)).Return(nil, errors.New("404 Not found"))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdDelete,
				false,
				cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
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
