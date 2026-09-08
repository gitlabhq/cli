//go:build !integration

package note

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func writeAttachFixture(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "screenshot.png")
	require.NoError(t, os.WriteFile(path, []byte("\x89PNG\r\n\x1a\nfake png body"), 0o600))
	return path
}

func expectUpload(tc *gitlabtesting.TestClient) {
	tc.MockProjectMarkdownUploads.EXPECT().
		UploadProjectMarkdown("OWNER/REPO", gomock.Any(), "screenshot.png", gomock.Any()).
		Return(&gitlab.ProjectMarkdownUploadedFile{
			Markdown: "![screenshot](/uploads/abc/screenshot.png)",
		}, nil, nil)
}

func TestNoteCreate_AttachAppendsReferenceToTheDiscussionBody(t *testing.T) {
	t.Parallel()

	path := writeAttachFixture(t)

	tc := gitlabtesting.NewTestClient(t)
	mockMR1(t, tc)
	expectUpload(tc)
	tc.MockDiscussions.EXPECT().
		CreateMergeRequestDiscussion("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ any, _ int64, opts *gitlab.CreateMergeRequestDiscussionOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.Discussion, *gitlab.Response, error) {
			assert.Equal(t, "Renders wrong here.\n\n![screenshot](/uploads/abc/screenshot.png)", *opts.Body)
			return &gitlab.Discussion{Notes: []*gitlab.Note{{ID: 301}}}, nil, nil
		})

	exec := setupCreateExec(t, tc)

	_, err := exec(`1 --message "Renders wrong here." --attach ` + path)
	require.NoError(t, err)
}

func TestNoteCreate_AttachOnlyPostsWithoutReadingABody(t *testing.T) {
	t.Parallel()

	path := writeAttachFixture(t)

	tc := gitlabtesting.NewTestClient(t)
	mockMR1(t, tc)
	expectUpload(tc)
	tc.MockDiscussions.EXPECT().
		CreateMergeRequestDiscussion("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ any, _ int64, opts *gitlab.CreateMergeRequestDiscussionOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.Discussion, *gitlab.Response, error) {
			assert.Equal(t, "![screenshot](/uploads/abc/screenshot.png)", *opts.Body)
			return &gitlab.Discussion{Notes: []*gitlab.Note{{ID: 302}}}, nil, nil
		})

	exec := setupCreateExec(t, tc)

	_, err := exec(`1 --attach ` + path)
	require.NoError(t, err)
}

func TestNoteCreate_AttachIsRejectedWithUnique(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	exec := setupCreateExec(t, tc)

	_, err := exec(`1 --message "LGTM" --unique --attach ./screenshot.png`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unique")
	assert.Contains(t, err.Error(), "attach")
}

func TestNoteUpdate_AttachAppendsToTheExistingNoteBody(t *testing.T) {
	t.Parallel()

	path := writeAttachFixture(t)

	tc := gitlabtesting.NewTestClient(t)
	mockMR1(t, tc)
	tc.MockDiscussions.EXPECT().
		ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
		Return([]*gitlab.Discussion{{
			ID:    "abc123",
			Notes: []*gitlab.Note{{ID: 12345, Body: "Original note body."}},
		}}, &gitlab.Response{}, nil)
	expectUpload(tc)
	tc.MockDiscussions.EXPECT().
		UpdateMergeRequestDiscussionNote("OWNER/REPO", int64(1), "abc123", int64(12345), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ any, _ int64, _ string, _ int64, opts *gitlab.UpdateMergeRequestDiscussionNoteOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.Note, *gitlab.Response, error) {
			assert.Equal(t, "Original note body.\n\n![screenshot](/uploads/abc/screenshot.png)", *opts.Body)
			return &gitlab.Note{ID: 12345}, nil, nil
		})

	exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
		return NewCmdUpdate(f)
	}, true,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		cmdtest.WithConfig(config.NewFromString("editor: vi")),
	)

	_, err := exec(`1 12345 --attach ` + path)
	require.NoError(t, err)
}

func TestNoteUpdate_AttachWithMessageReplacesTheNoteBody(t *testing.T) {
	t.Parallel()

	path := writeAttachFixture(t)

	tc := gitlabtesting.NewTestClient(t)
	mockMR1(t, tc)
	tc.MockDiscussions.EXPECT().
		ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
		Return([]*gitlab.Discussion{{
			ID:    "abc123",
			Notes: []*gitlab.Note{{ID: 12345, Body: "Original note body."}},
		}}, &gitlab.Response{}, nil)
	expectUpload(tc)
	tc.MockDiscussions.EXPECT().
		UpdateMergeRequestDiscussionNote("OWNER/REPO", int64(1), "abc123", int64(12345), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ any, _ int64, _ string, _ int64, opts *gitlab.UpdateMergeRequestDiscussionNoteOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.Note, *gitlab.Response, error) {
			assert.Equal(t, "Replaced body.\n\n![screenshot](/uploads/abc/screenshot.png)", *opts.Body)
			return &gitlab.Note{ID: 12345}, nil, nil
		})

	exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
		return NewCmdUpdate(f)
	}, true,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		cmdtest.WithConfig(config.NewFromString("editor: vi")),
	)

	_, err := exec(`1 12345 --message "Replaced body." --attach ` + path)
	require.NoError(t, err)
}
