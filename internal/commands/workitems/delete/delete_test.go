//go:build !integration

package delete

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestWorkItemDelete(t *testing.T) {
	t.Run("success cases", func(t *testing.T) {
		tests := []struct {
			name       string
			args       string
			wantOutput string
		}{
			{
				name:       "delete work item in project scope",
				args:       "1",
				wantOutput: "- Deleting work item in OWNER/REPO\n1\n",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				tc := gitlabtesting.NewTestClient(t)
				tc.MockWorkItems.EXPECT().
					DeleteWorkItem("OWNER/REPO", int64(1), gomock.Any()).
					Return(&gitlab.Response{}, nil)

				exec := cmdtest.SetupCmdForTest(
					t,
					NewCmd,
					false,
					cmdtest.WithGitLabClient(tc.Client),
					cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
				)

				out, err := exec(tt.args)
				require.NoError(t, err)
				assert.Contains(t, out.OutBuf.String(), tt.wantOutput)
			})
		}
	})

	t.Run("API error", func(t *testing.T) {
		tc := gitlabtesting.NewTestClient(t)
		tc.MockWorkItems.EXPECT().
			DeleteWorkItem(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil, assert.AnError)

		exec := cmdtest.SetupCmdForTest(
			t,
			NewCmd,
			false,
			cmdtest.WithGitLabClient(tc.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
		)

		_, err := exec("1")
		require.Error(t, err)
	})
}
