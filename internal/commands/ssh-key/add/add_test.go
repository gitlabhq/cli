//go:build !integration

package add

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_AddSSHKey(t *testing.T) {
	type testCase struct {
		Name        string
		ExpectedMsg []string
		wantErr     bool
		cli         string
		stdin       string
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	validSSHKey := &gitlab.SSHKey{
		ID:        1,
		Key:       "ssh-ed25519 example",
		Title:     "mysshkey",
		CreatedAt: new(time.Time{}),
	}

	testCases := []testCase{
		// Success cases
		{
			Name:        "Add SSH key from file",
			ExpectedMsg: []string{"New SSH public key added to your account.\n"},
			cli:         "testdata/testkey.key --title mysshkey --usage-type 'auth'",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().AddSSHKey(gomock.Any()).Return(validSSHKey, nil, nil)
			},
		},
		{
			Name:        "Add SSH key from stdin",
			ExpectedMsg: []string{"New SSH public key added to your account.\n\n"},
			cli:         "--title mysshkey",
			stdin:       "ssh-ed25519 example",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().AddSSHKey(gomock.Any()).Return(validSSHKey, nil, nil)
			},
		},

		// // File errors
		{
			Name:       "Add SSH key with missing file",
			cli:        "nonexistent.key --title somekey",
			wantErr:    true,
			wantStderr: "nonexistent.key",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
		},
		{
			Name:       "Add SSH key with long invalid expiry",
			cli:        "testdata/testkey.key --title somekey --expires-at not-a-valid-timestamp-that-is-long",
			wantErr:    true,
			wantStderr: "cannot parse",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
		},

		// // API errors
		{
			Name:       "Add SSH key with invalid key format",
			cli:        "testdata/testkey.key --title somekey",
			wantErr:    true,
			wantStderr: "Key is invalid",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().AddSSHKey(gomock.Any()).Return(nil, nil, errors.New("Key is invalid"))
			},
		},
		{
			Name:       "Add SSH key with duplicate key error",
			cli:        "testdata/testkey.key --title somekey",
			wantErr:    true,
			wantStderr: "Key has already been taken",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().AddSSHKey(gomock.Any()).Return(nil, nil, errors.New("Key has already been taken"))
			},
		},
		{
			Name:       "Add SSH key with unauthorized error",
			cli:        "testdata/testkey.key --title somekey",
			wantErr:    true,
			wantStderr: "401",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().AddSSHKey(gomock.Any()).Return(nil, nil, errors.New("401 Unauthorized"))
			},
		},
		{
			Name:       "Add SSH key with server error",
			cli:        "testdata/testkey.key --title somekey",
			wantErr:    true,
			wantStderr: "",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().AddSSHKey(gomock.Any()).Return(nil, nil, errors.New("500 Internal Server Error"))
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
				NewCmdAdd,
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
