//go:build !integration

package create

import (
	"fmt"
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

func TestWorkItemsCreate(t *testing.T) {
	t.Run("success cases", func(t *testing.T) {
		tests := []struct {
			name       string
			args       string
			workItem   *gitlab.WorkItem
			wantOutput string
		}{
			{
				name: "creates work item in project scope",
				args: "--type issue --title \"Test Issue\"",
				workItem: &gitlab.WorkItem{
					IID:    1,
					Title:  "Test Issue",
					WebURL: "https://gitlab.com/OWNER/REPO/-/work_items/1",
				},
				wantOutput: "https://gitlab.com/OWNER/REPO/-/work_items/1",
			},
			{
				name: "creates work item in group scope",
				args: "--group my-group --type epic --title \"Test Epic\"",
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
					CreateWorkItem(gomock.Any(), gomock.Any(), gomock.Any()).
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

	t.Run("API error", func(t *testing.T) {
		tc := gitlabtesting.NewTestClient(t)
		tc.MockWorkItems.EXPECT().
			CreateWorkItem(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil, nil, assert.AnError)

		exec := cmdtest.SetupCmdForTest(
			t,
			NewCmd,
			false,
			cmdtest.WithGitLabClient(tc.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
		)

		_, err := exec("--type issue --title \"Test Issue\"")
		require.Error(t, err)
	})
}

func TestWorkItemsCreate_DescriptionFile(t *testing.T) {
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
				CreateWorkItem(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ string, _ gitlab.WorkItemTypeID, opts *gitlab.CreateWorkItemOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.WorkItem, *gitlab.Response, error) {
					gotDescription = opts.Description
					return &gitlab.WorkItem{IID: 1, WebURL: "https://gitlab.com/OWNER/REPO/-/work_items/1"}, &gitlab.Response{}, nil
				})

			factoryOpts := []cmdtest.FactoryOption{
				cmdtest.WithGitLabClient(tc.Client),
				cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
			}

			path := "-"
			if tt.useStdin {
				factoryOpts = append(factoryOpts, cmdtest.WithStdin(body))
			} else {
				path = filepath.Join(t.TempDir(), "description.md")
				require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
			}

			exec := cmdtest.SetupCmdForTest(t, NewCmd, false, factoryOpts...)

			_, err := exec(fmt.Sprintf("--type issue --title Test --description-file %q", path))
			require.NoError(t, err)

			require.NotNil(t, gotDescription)
			assert.Equal(t, body, *gotDescription)
		})
	}
}

func TestWorkItemsCreate_DescriptionFileConflictsWithDescription(t *testing.T) {
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
	)

	_, err := exec(`--type issue --title Test --description inline --description-file some.md`)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "[description description-file]")
}

func TestWorkItemsCreate_FlagValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    string
		wantErr string
	}{
		{
			name:    "missing work item type",
			args:    "",
			wantErr: "required flag(s) \"type\" not set",
		},
		{
			name:    "unknown type",
			args:    "--type bogus",
			wantErr: "must be one of",
		},
		{
			name:    "missing --title",
			args:    "--type issue",
			wantErr: "--title required",
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
