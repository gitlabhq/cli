//go:build !integration

package get

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_GetGPGKey(t *testing.T) {
	type testCase struct {
		Name        string
		ExpectedMsg []string
		wantErr     bool
		cli         string
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	testKey := &gitlab.GPGKey{
		ID:        123,
		Key:       "-----BEGIN PGP PUBLIC KEY BLOCK-----\n\nmQINBF...\n-----END PGP PUBLIC KEY BLOCK-----",
		CreatedAt: new(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
	}

	testCases := []testCase{
		{
			Name:        "Get GPG key by ID",
			ExpectedMsg: []string{"Showing GPG key with ID 123"},
			cli:         "123",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().GetGPGKey(int64(123)).Return(testKey, nil, nil)
			},
		},
		{
			Name:       "Get GPG key with invalid ID",
			cli:        "abc",
			wantErr:    true,
			wantStderr: "404",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().GetGPGKey(int64(0)).Return(nil, nil, errors.New("404 Not Found"))
			},
		},
		{
			Name:       "Get GPG key with API error",
			cli:        "123",
			wantErr:    true,
			wantStderr: "404 Not Found",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().GetGPGKey(int64(123)).Return(nil, nil, errors.New("404 Not Found"))
			},
		},
		{
			Name:       "Get GPG key without ID",
			cli:        "",
			wantErr:    true,
			wantStderr: "accepts 1 arg(s), received 0",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdGet,
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

func TestGpgKeyGet_JSON(t *testing.T) {
	t.Parallel()

	testKey := &gitlab.GPGKey{
		ID:        123,
		Key:       "-----BEGIN PGP PUBLIC KEY BLOCK-----\n\nmQINBF...\n-----END PGP PUBLIC KEY BLOCK-----",
		CreatedAt: new(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
	}

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockUsers.EXPECT().
		GetGPGKey(int64(123)).
		Return(testKey, nil, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdGet,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	out, err := exec("123 --output json")
	require.NoError(t, err)

	assert.Contains(t, out.String(), `"id":123`)
	assert.Contains(t, out.String(), `"key":"-----BEGIN PGP PUBLIC KEY BLOCK-----\n\nmQINBF...\n-----END PGP PUBLIC KEY BLOCK-----"`)
	assert.Empty(t, out.Stderr())
}
