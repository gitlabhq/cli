//go:build !integration

package get

import (
	"errors"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_GetSSHKey(t *testing.T) {
	type testCase struct {
		Name        string
		ExpectedMsg []string
		wantErr     bool
		cli         string
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	testKey := &gitlab.SSHKey{
		ID:        123,
		Key:       "ssh-ed25519 example",
		CreatedAt: new(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
		UsageType: "auth_and_signing",
		Title:     "mysshkey",
	}

	testCases := []testCase{
		{
			Name:        "Get SSH key by ID",
			ExpectedMsg: []string{"ssh-ed25519 example"},
			cli:         "123",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().GetSSHKey(int64(123)).Return(testKey, nil, nil)
			},
		},
		{
			Name:       "Get SSH key without ID",
			cli:        "",
			wantErr:    true,
			wantStderr: "the <key-id> argument is required when prompts are disabled",
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

func TestSshKeyGet_JSON(t *testing.T) {
	t.Parallel()

	testKey := &gitlab.SSHKey{
		ID:        123,
		Key:       "ssh-ed25519 example",
		CreatedAt: new(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
		UsageType: "auth_and_signing",
		Title:     "mysshkey",
	}

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockUsers.EXPECT().
		GetSSHKey(int64(123)).
		Return(testKey, nil, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdGet,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
	)

	out, err := exec("123 --output json")
	require.NoError(t, err)

	assert.Contains(t, out.String(), `"id":123`)
	assert.Contains(t, out.String(), `"title":"mysshkey"`)
	assert.Contains(t, out.String(), `"key":"ssh-ed25519 example"`)
	assert.Contains(t, out.String(), `"usage_type":"auth_and_signing"`)
	assert.Empty(t, out.Stderr())
}

func TestSshKeyGet_Completions(t *testing.T) {
	t.Parallel()

	testKey := &gitlab.SSHKey{ID: 123, Title: "mysshkey"}

	testCases := []struct {
		Name          string
		Args          []string
		setupMock     func(tc *gitlabtesting.TestClient)
		wantComps     []cobra.Completion
		wantDirective cobra.ShellCompDirective
	}{
		{
			Name: "Completes key IDs as ID and title",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().ListSSHKeys(gomock.Any(), gomock.Any()).
					Return([]*gitlab.SSHKey{testKey}, &gitlab.Response{}, nil)
			},
			wantComps:     []cobra.Completion{"123\tmysshkey"},
			wantDirective: cobra.ShellCompDirectiveNoFileComp,
		},
		{
			Name:          "No completions when the second argument is completed",
			Args:          []string{"123", "456"},
			setupMock:     func(tc *gitlabtesting.TestClient) {},
			wantDirective: cobra.ShellCompDirectiveNoFileComp,
		},
		{
			Name: "API error returns no completions",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().ListSSHKeys(gomock.Any(), gomock.Any()).
					Return(nil, nil, errors.New("500 Internal Server Error"))
			},
			wantDirective: cobra.ShellCompDirectiveNoFileComp,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()

			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			f := cmdtest.NewTestFactory(nil,
				cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
			)

			cmd := NewCmdGet(f)
			cmd.SetContext(t.Context())
			completions, directive := cmd.ValidArgsFunction(cmd, tc.Args, "")

			assert.Equal(t, tc.wantComps, completions)
			assert.Equal(t, tc.wantDirective, directive)
		})
	}
}
