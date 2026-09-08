//go:build !integration

package note

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/acarl005/stripansi"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/issuable"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

var pngBytes = []byte("\x89PNG\r\n\x1a\nfake png body")

func writeAttachment(t *testing.T, name string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, pngBytes, 0o600))
	return path
}

func expectIssue(testClient *gitlabtesting.TestClient) {
	testClient.MockIssues.EXPECT().
		GetIssue("OWNER/REPO", int64(1), gomock.Any()).
		Return(&gitlab.Issue{
			ID:        1,
			IID:       1,
			IssueType: new("issue"),
			WebURL:    "https://gitlab.com/OWNER/REPO/issues/1",
		}, nil, nil)
}

func setupNoteCmd(t *testing.T, testClient *gitlabtesting.TestClient) cmdtest.CmdExecFunc {
	t.Helper()

	return cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
		return NewCmdNote(f, issuable.TypeIssue)
	}, true,
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		cmdtest.WithConfig(config.NewFromString("editor: vi")),
	)
}

func TestNoteCreate_AttachAppendsReferenceToMessage(t *testing.T) {
	t.Parallel()

	path := writeAttachment(t, "screenshot.png")

	testClient := gitlabtesting.NewTestClient(t)
	expectIssue(testClient)
	testClient.MockProjectMarkdownUploads.EXPECT().
		UploadProjectMarkdown("OWNER/REPO", gomock.Any(), "screenshot.png", gomock.Any()).
		Return(&gitlab.ProjectMarkdownUploadedFile{
			Markdown: "![screenshot](/uploads/abc/screenshot.png)",
		}, nil, nil)
	testClient.MockNotes.EXPECT().
		CreateIssueNote("OWNER/REPO", int64(1), gomock.Any()).
		DoAndReturn(func(pid any, iid int64, opts *gitlab.CreateIssueNoteOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Note, *gitlab.Response, error) {
			assert.Equal(t, "Here is the repro.\n\n![screenshot](/uploads/abc/screenshot.png)", *opts.Body)
			return &gitlab.Note{ID: 301}, nil, nil
		})

	exec := setupNoteCmd(t, testClient)

	output, err := exec(`1 --message "Here is the repro." --attach ` + path)
	require.NoError(t, err)
	assert.Equal(t, "https://gitlab.com/OWNER/REPO/issues/1#note_301\n", output.String())
	assert.Contains(t, stripansi.Strip(output.Stderr()), "Uploading screenshot.png")
}

func TestNoteCreate_AttachOnlyPostsWithoutAMessage(t *testing.T) {
	t.Parallel()

	path := writeAttachment(t, "screenshot.png")

	testClient := gitlabtesting.NewTestClient(t)
	expectIssue(testClient)
	testClient.MockProjectMarkdownUploads.EXPECT().
		UploadProjectMarkdown("OWNER/REPO", gomock.Any(), "screenshot.png", gomock.Any()).
		Return(&gitlab.ProjectMarkdownUploadedFile{
			Markdown: "![screenshot](/uploads/abc/screenshot.png)",
		}, nil, nil)
	testClient.MockNotes.EXPECT().
		CreateIssueNote("OWNER/REPO", int64(1), gomock.Any()).
		DoAndReturn(func(pid any, iid int64, opts *gitlab.CreateIssueNoteOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Note, *gitlab.Response, error) {
			assert.Equal(t, "![screenshot](/uploads/abc/screenshot.png)", *opts.Body)
			return &gitlab.Note{ID: 302}, nil, nil
		})

	exec := setupNoteCmd(t, testClient)

	output, err := exec(`1 --attach ` + path)
	require.NoError(t, err)
	assert.Equal(t, "https://gitlab.com/OWNER/REPO/issues/1#note_302\n", output.String())
}

func TestNoteCreate_MultipleAttachmentsAppendInOrder(t *testing.T) {
	t.Parallel()

	first := writeAttachment(t, "first.png")
	second := writeAttachment(t, "second.png")

	testClient := gitlabtesting.NewTestClient(t)
	expectIssue(testClient)
	gomock.InOrder(
		testClient.MockProjectMarkdownUploads.EXPECT().
			UploadProjectMarkdown("OWNER/REPO", gomock.Any(), "first.png", gomock.Any()).
			Return(&gitlab.ProjectMarkdownUploadedFile{Markdown: "![first](/uploads/a/first.png)"}, nil, nil),
		testClient.MockProjectMarkdownUploads.EXPECT().
			UploadProjectMarkdown("OWNER/REPO", gomock.Any(), "second.png", gomock.Any()).
			Return(&gitlab.ProjectMarkdownUploadedFile{Markdown: "![second](/uploads/b/second.png)"}, nil, nil),
	)
	testClient.MockNotes.EXPECT().
		CreateIssueNote("OWNER/REPO", int64(1), gomock.Any()).
		DoAndReturn(func(pid any, iid int64, opts *gitlab.CreateIssueNoteOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Note, *gitlab.Response, error) {
			assert.Equal(t, "Two files.\n\n![first](/uploads/a/first.png)\n![second](/uploads/b/second.png)", *opts.Body)
			return &gitlab.Note{ID: 303}, nil, nil
		})

	exec := setupNoteCmd(t, testClient)

	_, err := exec(`1 --message "Two files." --attach ` + first + ` --attach ` + second)
	require.NoError(t, err)
}

func TestNoteCreate_AttachFromStdinSniffsAName(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	expectIssue(testClient)
	testClient.MockProjectMarkdownUploads.EXPECT().
		UploadProjectMarkdown("OWNER/REPO", gomock.Any(), "upload.png", gomock.Any()).
		Return(&gitlab.ProjectMarkdownUploadedFile{Markdown: "![upload](/uploads/abc/upload.png)"}, nil, nil)
	testClient.MockNotes.EXPECT().
		CreateIssueNote("OWNER/REPO", int64(1), gomock.Any()).
		DoAndReturn(func(pid any, iid int64, opts *gitlab.CreateIssueNoteOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Note, *gitlab.Response, error) {
			assert.Equal(t, "![upload](/uploads/abc/upload.png)", *opts.Body)
			return &gitlab.Note{ID: 304}, nil, nil
		})

	exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
		return NewCmdNote(f, issuable.TypeIssue)
	}, true,
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		cmdtest.WithConfig(config.NewFromString("editor: vi")),
		cmdtest.WithStdin(string(pngBytes)),
	)

	_, err := exec(`1 --attach -`)
	require.NoError(t, err)
}

func TestNoteCreate_AttachMissingFileUploadsNothing(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)
	expectIssue(testClient)

	exec := setupNoteCmd(t, testClient)

	_, err := exec(`1 --message "hi" --attach ` + filepath.Join(t.TempDir(), "absent.png"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read attachment")
}
