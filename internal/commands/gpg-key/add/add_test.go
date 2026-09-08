//go:build !integration

package add

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_AddGPGKey(t *testing.T) {
	type testCase struct {
		Name        string
		ExpectedMsg []string
		wantErr     bool
		cli         string
		stdin       string
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	validGPGKey := &gitlab.GPGKey{
		ID:        1,
		Key:       "-----BEGIN PGP PUBLIC KEY BLOCK-----\n\nmQENBFnZ...\n-----END PGP PUBLIC KEY BLOCK-----",
		CreatedAt: new(time.Time{}),
	}

	testCases := []testCase{
		// Success cases
		{
			Name:        "Add GPG key from file",
			ExpectedMsg: []string{"New GPG key added to your account."},
			cli:         "testdata/testkey.gpg",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().AddGPGKey(gomock.Any()).Return(validGPGKey, nil, nil)
			},
		},
		{
			Name:        "Add GPG key from stdin",
			ExpectedMsg: []string{"New GPG key added to your account."},
			cli:         "",
			stdin:       "-----BEGIN PGP PUBLIC KEY BLOCK-----\n\nmQENBFnZ...\n-----END PGP PUBLIC KEY BLOCK-----",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().AddGPGKey(gomock.Any()).Return(validGPGKey, nil, nil)
			},
		},

		// File errors
		{
			Name:       "Add GPG key with missing file",
			cli:        "nonexistent.gpg",
			wantErr:    true,
			wantStderr: "nonexistent.gpg",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
		},

		// API errors
		{
			Name:       "Add GPG key with invalid key format",
			cli:        "testdata/testkey.gpg",
			wantErr:    true,
			wantStderr: "Key is invalid",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().AddGPGKey(gomock.Any()).Return(nil, nil, errors.New("Key is invalid"))
			},
		},
		{
			Name:       "Add GPG key with duplicate key error",
			cli:        "testdata/testkey.gpg",
			wantErr:    true,
			wantStderr: "Key has already been taken",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().AddGPGKey(gomock.Any()).Return(nil, nil, errors.New("Key has already been taken"))
			},
		},
		{
			Name:       "Add GPG key with unauthorized error",
			cli:        "testdata/testkey.gpg",
			wantErr:    true,
			wantStderr: "401",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().AddGPGKey(gomock.Any()).Return(nil, nil, errors.New("401 Unauthorized"))
			},
		},
		{
			Name:       "Add GPG key with server error",
			cli:        "testdata/testkey.gpg",
			wantErr:    true,
			wantStderr: "",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().AddGPGKey(gomock.Any()).Return(nil, nil, errors.New("500 Internal Server Error"))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)

			exec := cmdtest.SetupCmdForTest(t, NewCmdAdd, false,
				cmdtest.WithStdin(tc.stdin),
				cmdtest.WithGitLabClient(testClient.Client),
			)
			out, err := exec(tc.cli)

			// THEN
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantStderr)
				return
			}
			require.NoError(t, err)
			for _, msg := range tc.ExpectedMsg {
				assert.Contains(t, out.String(), msg)
			}
		})
	}
}
