//go:build !integration

package delete

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_DeleteGPGKey(t *testing.T) {
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
			Name:        "Delete GPG key by ID",
			ExpectedMsg: []string{"GPG key deleted."},
			cli:         "123",
			wantErr:     false,
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().DeleteGPGKey(int64(123)).Return(nil, nil)
			},
		},
		{
			Name:       "Delete GPG key with non numeric ID",
			cli:        "abc",
			wantErr:    true,
			wantStderr: "404",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().DeleteGPGKey(int64(0)).Return(nil, errors.New("404 Not Found"))
			},
		},
		{
			Name:       "Delete non existent GPG key",
			cli:        "999",
			wantErr:    true,
			wantStderr: "404",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().DeleteGPGKey(int64(999)).Return(nil, errors.New("404 Not found"))
			},
		},
		{
			Name:       "Delete GPG key without ID",
			cli:        "",
			wantErr:    true,
			wantStderr: "accepts 1 arg(s), received 0",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
		},
		{
			Name:       "Delete GPG key with unauthorized error",
			cli:        "123",
			wantErr:    true,
			wantStderr: "401",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().DeleteGPGKey(int64(123)).Return(nil, errors.New("401 Unauthorized"))
			},
		},
		{
			Name:       "Explicit zero ID returns not found",
			cli:        "0",
			wantErr:    true,
			wantStderr: "404 Not found",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().DeleteGPGKey(int64(0)).Return(nil, errors.New("404 Not found"))
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
			for _, msg := range tc.ExpectedMsg {
				assert.Contains(t, out.OutBuf.String(), msg)
			}
		})
	}
}
