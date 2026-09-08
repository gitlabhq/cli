//go:build !integration

package create

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func writeAttachFixture(t *testing.T, name string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte("\x89PNG\r\n\x1a\nfake png body"), 0o600))
	return path
}

func TestWorkItemsCreate_AttachAppendsReferenceToDescription(t *testing.T) {
	path := writeAttachFixture(t, "screenshot.png")

	tc := gitlabtesting.NewTestClient(t)
	tc.MockProjectMarkdownUploads.EXPECT().
		UploadProjectMarkdown("OWNER/REPO", gomock.Any(), "screenshot.png", gomock.Any()).
		Return(&gitlab.ProjectMarkdownUploadedFile{Markdown: "![screenshot](/uploads/abc/screenshot.png)"}, nil, nil)

	var gotDescription *string
	tc.MockWorkItems.EXPECT().
		CreateWorkItem("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ string, _ gitlab.WorkItemTypeID, opts *gitlab.CreateWorkItemOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.WorkItem, *gitlab.Response, error) {
			gotDescription = opts.Description
			return &gitlab.WorkItem{IID: 1, WebURL: "https://gitlab.com/OWNER/REPO/-/work_items/1"}, &gitlab.Response{}, nil
		})

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
	)

	_, err := exec(`--type issue --title "Add feature" --description "See below." --attach ` + path)
	require.NoError(t, err)

	require.NotNil(t, gotDescription)
	assert.Equal(t, "See below.\n\n![screenshot](/uploads/abc/screenshot.png)", *gotDescription)
}

func TestWorkItemsCreate_AttachIsRejectedWithGroup(t *testing.T) {
	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
	)

	_, err := exec(`--type epic --title "Add feature" --group my-group --attach ./screenshot.png`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "group")
	assert.Contains(t, err.Error(), "attach")
}

func TestWorkItemsCreate_NoAttachLeavesTheDescriptionAlone(t *testing.T) {
	tc := gitlabtesting.NewTestClient(t)

	var gotDescription *string
	tc.MockWorkItems.EXPECT().
		CreateWorkItem("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ string, _ gitlab.WorkItemTypeID, opts *gitlab.CreateWorkItemOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.WorkItem, *gitlab.Response, error) {
			gotDescription = opts.Description
			return &gitlab.WorkItem{IID: 1, WebURL: "https://gitlab.com/OWNER/REPO/-/work_items/1"}, &gitlab.Response{}, nil
		})

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
	)

	_, err := exec(`--type issue --title "Add feature" --description "See below."`)
	require.NoError(t, err)

	require.NotNil(t, gotDescription)
	assert.Equal(t, "See below.", *gotDescription)
}
