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

func setupAttachUpdate(t *testing.T, existingDescription string) (*gitlabtesting.TestClient, func() *string, cmdtest.CmdExecFunc) {
	t.Helper()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockIssues.EXPECT().
		GetIssue("OWNER/REPO", int64(42)).
		Return(&gitlab.Issue{IID: 42, Title: "issueTitle", Description: existingDescription}, nil, nil)

	var gotDescription *string
	tc.MockIssues.EXPECT().
		UpdateIssue("OWNER/REPO", int64(42), gomock.Any()).
		DoAndReturn(func(_ any, _ int64, opts *gitlab.UpdateIssueOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.Issue, *gitlab.Response, error) {
			gotDescription = opts.Description
			return &gitlab.Issue{IID: 42, WebURL: "https://gitlab.com/OWNER/REPO/-/issues/42"}, nil, nil
		})

	exec := cmdtest.SetupCmdForTest(t, NewCmdUpdate, false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
	)

	return tc, func() *string { return gotDescription }, exec
}

func TestIssueUpdate_AttachAppendsToTheExistingDescription(t *testing.T) {
	path := writeAttachFixture(t, "screenshot.png")

	tc, description, exec := setupAttachUpdate(t, "Original body.")
	tc.MockProjectMarkdownUploads.EXPECT().
		UploadProjectMarkdown("OWNER/REPO", gomock.Any(), "screenshot.png", gomock.Any()).
		Return(&gitlab.ProjectMarkdownUploadedFile{Markdown: "![screenshot](/uploads/abc/screenshot.png)"}, nil, nil)

	_, err := exec(`42 --attach ` + path)
	require.NoError(t, err)

	require.NotNil(t, description())
	assert.Equal(t, "Original body.\n\n![screenshot](/uploads/abc/screenshot.png)", *description())
}

func TestIssueUpdate_AttachWithDescriptionReplacesTheBody(t *testing.T) {
	path := writeAttachFixture(t, "screenshot.png")

	tc, description, exec := setupAttachUpdate(t, "Original body.")
	tc.MockProjectMarkdownUploads.EXPECT().
		UploadProjectMarkdown("OWNER/REPO", gomock.Any(), "screenshot.png", gomock.Any()).
		Return(&gitlab.ProjectMarkdownUploadedFile{Markdown: "![screenshot](/uploads/abc/screenshot.png)"}, nil, nil)

	_, err := exec(`42 --description "Replaced body." --attach ` + path)
	require.NoError(t, err)

	require.NotNil(t, description())
	assert.Equal(t, "Replaced body.\n\n![screenshot](/uploads/abc/screenshot.png)", *description())
}

func TestIssueUpdate_AttachReportsTheFileCount(t *testing.T) {
	first := writeAttachFixture(t, "first.png")
	second := writeAttachFixture(t, "second.png")

	tc, _, exec := setupAttachUpdate(t, "Original body.")
	tc.MockProjectMarkdownUploads.EXPECT().
		UploadProjectMarkdown("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&gitlab.ProjectMarkdownUploadedFile{Markdown: "![x](/uploads/x)"}, nil, nil).
		Times(2)

	output, err := exec(`42 --attach ` + first + ` --attach ` + second)
	require.NoError(t, err)
	assert.Contains(t, output.String(), "attached 2 files")
}

func TestIssueUpdate_NoAttachLeavesTheDescriptionUnset(t *testing.T) {
	tc := gitlabtesting.NewTestClient(t)
	tc.MockIssues.EXPECT().
		GetIssue("OWNER/REPO", int64(42)).
		Return(&gitlab.Issue{IID: 42, Description: "Original body."}, nil, nil)

	var gotDescription *string
	tc.MockIssues.EXPECT().
		UpdateIssue("OWNER/REPO", int64(42), gomock.Any()).
		DoAndReturn(func(_ any, _ int64, opts *gitlab.UpdateIssueOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.Issue, *gitlab.Response, error) {
			gotDescription = opts.Description
			return &gitlab.Issue{IID: 42, WebURL: "https://gitlab.com/OWNER/REPO/-/issues/42"}, nil, nil
		})

	exec := cmdtest.SetupCmdForTest(t, NewCmdUpdate, false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
	)

	_, err := exec(`42 --title "new title"`)
	require.NoError(t, err)

	assert.Nil(t, gotDescription)
}
