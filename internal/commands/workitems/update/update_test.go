//go:build !integration

package update

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestWorkItemsUpdate(t *testing.T) {
	t.Run("success cases", func(t *testing.T) {
		tests := []struct {
			name       string
			args       string
			workItem   *gitlab.WorkItem
			wantOutput string
		}{
			{
				name: "updates work item title in project scope",
				args: "1 --title \"Test Issue\"",
				workItem: &gitlab.WorkItem{
					IID:    1,
					Title:  "Test Issue",
					WebURL: "https://gitlab.com/OWNER/REPO/-/work_items/1",
				},
				wantOutput: "https://gitlab.com/OWNER/REPO/-/work_items/1",
			},
			{
				name: "updates work item title in group scope",
				args: "2 --group my-group --title \"Test Epic\"",
				workItem: &gitlab.WorkItem{
					IID:    2,
					Title:  "Test Epic",
					WebURL: "https://gitlab.com/groups/my-group/-/work_items/2",
				},
				wantOutput: "https://gitlab.com/groups/my-group/-/work_items/2",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				tc := gitlabtesting.NewTestClient(t)
				tc.MockWorkItems.EXPECT().
					UpdateWorkItem(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(tt.workItem, &gitlab.Response{}, nil)

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
}

func TestWorkItemsUpdate_DescriptionFile(t *testing.T) {
	const body = "# Heading\n\nMulti-line body with \"quotes\", $VARS and `backticks`.\n"

	tests := []struct {
		name     string
		useStdin bool
	}{
		{name: "reads the description from a file"},
		{name: "reads the description from standard input", useStdin: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := gitlabtesting.NewTestClient(t)

			var gotDescription *string
			tc.MockWorkItems.EXPECT().
				UpdateWorkItem(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ string, _ int64, opts *gitlab.UpdateWorkItemOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.WorkItem, *gitlab.Response, error) {
					gotDescription = opts.Description
					return &gitlab.WorkItem{IID: 1, WebURL: "https://gitlab.com/OWNER/REPO/-/work_items/1"}, &gitlab.Response{}, nil
				})

			opts := []cmdtest.FactoryOption{
				cmdtest.WithGitLabClient(tc.Client),
				cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
			}

			path := "-"
			if tt.useStdin {
				opts = append(opts, cmdtest.WithStdin(body))
			} else {
				path = filepath.Join(t.TempDir(), "description.md")
				require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
			}

			exec := cmdtest.SetupCmdForTest(t, NewCmd, false, opts...)

			_, err := exec("1 --description-file " + path)
			require.NoError(t, err)

			require.NotNil(t, gotDescription)
			assert.Equal(t, body, *gotDescription)
		})
	}
}

func TestWorkItemsUpdate_DescriptionFileConflictsWithDescription(t *testing.T) {
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
	)

	_, err := exec(`1 --description inline --description-file some.md`)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "[description description-file]")
}

func TestWorkItemsUpdate_FlagValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    string
		wantErr string
	}{
		{
			name:    "invalid <iid> arg",
			args:    "abc",
			wantErr: "invalid work item ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmd,
				false,
				cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
			)

			_, err := exec(tt.args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
