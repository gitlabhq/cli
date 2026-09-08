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

	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func writeAttachFixture(t *testing.T, name string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte("\x89PNG\r\n\x1a\nfake png body"), 0o600))
	return path
}

func expectUpdate(tc *gitlabtesting.TestClient) func() *string {
	var gotDescription *string
	tc.MockWorkItems.EXPECT().
		UpdateWorkItem("OWNER/REPO", int64(42), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ string, _ int64, opts *gitlab.UpdateWorkItemOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.WorkItem, *gitlab.Response, error) {
			gotDescription = opts.Description
			return &gitlab.WorkItem{IID: 42, WebURL: "https://gitlab.com/OWNER/REPO/-/work_items/42"}, &gitlab.Response{}, nil
		})
	return func() *string { return gotDescription }
}

func TestWorkItemsUpdate_AttachFetchesAndAppendsToTheExistingDescription(t *testing.T) {
	path := writeAttachFixture(t, "screenshot.png")

	tc := gitlabtesting.NewTestClient(t)
	tc.MockWorkItems.EXPECT().
		GetWorkItem("OWNER/REPO", int64(42), gomock.Any()).
		Return(&gitlab.WorkItem{IID: 42, Description: "Original body."}, &gitlab.Response{}, nil)
	tc.MockProjectMarkdownUploads.EXPECT().
		UploadProjectMarkdown("OWNER/REPO", gomock.Any(), "screenshot.png", gomock.Any()).
		Return(&gitlab.ProjectMarkdownUploadedFile{Markdown: "![screenshot](/uploads/abc/screenshot.png)"}, nil, nil)
	description := expectUpdate(tc)

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
	)

	_, err := exec(`42 --attach ` + path)
	require.NoError(t, err)

	require.NotNil(t, description())
	assert.Equal(t, "Original body.\n\n![screenshot](/uploads/abc/screenshot.png)", *description())
}

func TestWorkItemsUpdate_AttachWithDescriptionSkipsTheFetch(t *testing.T) {
	path := writeAttachFixture(t, "screenshot.png")

	tc := gitlabtesting.NewTestClient(t)
	// No GetWorkItem expectation: the extra round trip must not happen.
	tc.MockProjectMarkdownUploads.EXPECT().
		UploadProjectMarkdown("OWNER/REPO", gomock.Any(), "screenshot.png", gomock.Any()).
		Return(&gitlab.ProjectMarkdownUploadedFile{Markdown: "![screenshot](/uploads/abc/screenshot.png)"}, nil, nil)
	description := expectUpdate(tc)

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
	)

	_, err := exec(`42 --description "Replaced body." --attach ` + path)
	require.NoError(t, err)

	require.NotNil(t, description())
	assert.Equal(t, "Replaced body.\n\n![screenshot](/uploads/abc/screenshot.png)", *description())
}

func TestWorkItemsUpdate_AttachIsRejectedWithGroup(t *testing.T) {
	tc := gitlabtesting.NewTestClient(t)

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
	)

	_, err := exec(`42 --group my-group --attach ./screenshot.png`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "group")
	assert.Contains(t, err.Error(), "attach")
}

func TestWorkItemsUpdate_NoAttachDoesNotFetchTheDescription(t *testing.T) {
	tc := gitlabtesting.NewTestClient(t)
	description := expectUpdate(tc)

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
	)

	_, err := exec(`42 --title "new title"`)
	require.NoError(t, err)

	assert.Nil(t, description())
}
