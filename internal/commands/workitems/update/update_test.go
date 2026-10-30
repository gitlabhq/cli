//go:build !integration

package update

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
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
					UpdateWorkItem(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
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
				UpdateWorkItem(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
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

func TestWorkItemsUpdate_Iteration(t *testing.T) {
	tests := []struct {
		name          string
		args          string
		wantIteration *int64
	}{
		{
			name:          "sets the iteration",
			args:          "1 --iteration 123",
			wantIteration: new(int64(123)),
		},
		{
			name: "leaves the iteration unchanged without the flag",
			args: "1 --title Test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := gitlabtesting.NewTestClient(t)

			var gotIteration *int64
			tc.MockWorkItems.EXPECT().
				UpdateWorkItem(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ string, _ int64, opts *gitlab.UpdateWorkItemOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.WorkItem, *gitlab.Response, error) {
					gotIteration = opts.IterationID
					return &gitlab.WorkItem{IID: 1, WebURL: "https://gitlab.com/OWNER/REPO/-/work_items/1"}, &gitlab.Response{}, nil
				})

			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmd,
				false,
				cmdtest.WithGitLabClient(tc.Client),
				cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
			)

			_, err := exec(tt.args)
			require.NoError(t, err)
			assert.Equal(t, tt.wantIteration, gotIteration)
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
		{
			name:    "non-numeric --iteration",
			args:    "1 --iteration abc",
			wantErr: "invalid argument \"abc\" for \"--iteration\"",
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

func TestWorkItemsUpdate_Parent(t *testing.T) {
	tests := []struct {
		name           string
		args           string
		wantParentPath string
		wantParentIID  int64
		wantParentID   *int64
	}{
		{
			name:           "sets the parent from an ID in the current project",
			args:           "1 --parent 42",
			wantParentPath: "OWNER/REPO",
			wantParentIID:  42,
			wantParentID:   new(int64(9001)),
		},
		{
			name:           "sets the parent from a URL",
			args:           "1 --parent https://gitlab.com/groups/my-group/-/work_items/7",
			wantParentPath: "my-group",
			wantParentIID:  7,
			wantParentID:   new(int64(9001)),
		},
		{
			name: "leaves the parent unset without the flag",
			args: "1 --title Test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := gitlabtesting.NewTestClient(t, gitlab.WithBaseURL("https://gitlab.com/api/v4"))

			if tt.wantParentID != nil {
				tc.MockWorkItems.EXPECT().
					GetWorkItem(tt.wantParentPath, tt.wantParentIID, gomock.Any()).
					Return(&gitlab.WorkItem{ID: *tt.wantParentID, IID: tt.wantParentIID}, &gitlab.Response{}, nil)
			}

			var gotParentID *int64
			tc.MockWorkItems.EXPECT().
				UpdateWorkItem("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ string, _ int64, opts *gitlab.UpdateWorkItemOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.WorkItem, *gitlab.Response, error) {
					gotParentID = opts.ParentID
					return &gitlab.WorkItem{IID: 1, WebURL: "https://gitlab.com/OWNER/REPO/-/work_items/1"}, &gitlab.Response{}, nil
				})

			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmd,
				false,
				cmdtest.WithGitLabClient(tc.Client),
				cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
			)

			_, err := exec(tt.args)
			require.NoError(t, err)
			assert.Equal(t, tt.wantParentID, gotParentID)
		})
	}
}

func TestWorkItemsUpdate_ParentErrors(t *testing.T) {
	t.Run("invalid parent", func(t *testing.T) {
		tc := gitlabtesting.NewTestClient(t)

		exec := cmdtest.SetupCmdForTest(
			t,
			NewCmd,
			false,
			cmdtest.WithGitLabClient(tc.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
		)

		_, err := exec("1 --parent abc")
		var flagErr cmdutils.FlagError
		require.ErrorAs(t, err, &flagErr)
		assert.Contains(t, err.Error(), `invalid work item: "abc"`)
	})

	t.Run("parent URL on another host", func(t *testing.T) {
		tc := gitlabtesting.NewTestClient(t, gitlab.WithBaseURL("https://gitlab.com/api/v4"))

		exec := cmdtest.SetupCmdForTest(
			t,
			NewCmd,
			false,
			cmdtest.WithGitLabClient(tc.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
		)

		_, err := exec("1 --parent https://gitlab.example.com/group/project/-/work_items/42")
		var flagErr cmdutils.FlagError
		require.ErrorAs(t, err, &flagErr)
		assert.Contains(t, err.Error(), "is not on gitlab.com")
	})

	t.Run("parent not found", func(t *testing.T) {
		tc := gitlabtesting.NewTestClient(t)
		tc.MockWorkItems.EXPECT().
			GetWorkItem("OWNER/REPO", int64(42), gomock.Any()).
			Return(nil, nil, gitlab.ErrNotFound)

		exec := cmdtest.SetupCmdForTest(
			t,
			NewCmd,
			false,
			cmdtest.WithGitLabClient(tc.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
		)

		// No upload mock is set, so an upload before the parent lookup fails the test.
		_, err := exec("1 --parent 42 --attach " + writeAttachFixture(t, "screenshot.png"))
		require.ErrorIs(t, err, gitlab.ErrNotFound)
		assert.Contains(t, err.Error(), "failed to find parent work item OWNER/REPO#42")
	})
}
