//go:build !integration

package update

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestDraftUpdate(t *testing.T) {
	t.Parallel()

	t.Run("general pending comment sends no position", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		mockGetDraft(tc, &gitlab.DraftNote{ID: 501, Note: "old", Position: &gitlab.NotePosition{}})
		tc.MockDraftNotes.EXPECT().
			UpdateDraftNote("OWNER/REPO", int64(1), int64(501), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ any, _ int64, _ int64, opts *gitlab.UpdateDraftNoteOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.DraftNote, *gitlab.Response, error) {
				assert.Equal(t, "new", *opts.Note)
				assert.Nil(t, opts.Position)
				return &gitlab.DraftNote{ID: 501}, nil, nil
			})

		output, err := setupExec(t, tc)(`1 501 -m "new"`)
		require.NoError(t, err)
		assert.Equal(t, "501\n", output.String())
	})

	t.Run("pending reply sends no position", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		mockGetDraft(tc, &gitlab.DraftNote{ID: 501, Note: "old", DiscussionID: "abc12345"})
		tc.MockDraftNotes.EXPECT().
			UpdateDraftNote("OWNER/REPO", int64(1), int64(501), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ any, _ int64, _ int64, opts *gitlab.UpdateDraftNoteOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.DraftNote, *gitlab.Response, error) {
				assert.Nil(t, opts.Position)
				return &gitlab.DraftNote{ID: 501}, nil, nil
			})

		_, err := setupExec(t, tc)(`1 501 -m "new"`)
		require.NoError(t, err)
	})

	t.Run("single-line diff comment re-sends its position", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		mockGetDraft(tc, &gitlab.DraftNote{ID: 501, Note: "old", Position: &gitlab.NotePosition{
			BaseSHA:      "base",
			StartSHA:     "start",
			HeadSHA:      "head",
			PositionType: "text",
			NewPath:      "main.go",
			OldPath:      "main.go",
			NewLine:      42,
		}})
		tc.MockDraftNotes.EXPECT().
			UpdateDraftNote("OWNER/REPO", int64(1), int64(501), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ any, _ int64, _ int64, opts *gitlab.UpdateDraftNoteOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.DraftNote, *gitlab.Response, error) {
				assert.Equal(t, &gitlab.PositionOptions{
					BaseSHA:      new("base"),
					StartSHA:     new("start"),
					HeadSHA:      new("head"),
					PositionType: new("text"),
					NewPath:      new("main.go"),
					OldPath:      new("main.go"),
					NewLine:      new(int64(42)),
				}, opts.Position)
				return &gitlab.DraftNote{ID: 501}, nil, nil
			})

		_, err := setupExec(t, tc)(`1 501 -m "new"`)
		require.NoError(t, err)
	})

	t.Run("file-level comment re-sends a position without lines", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		mockGetDraft(tc, &gitlab.DraftNote{ID: 501, Note: "old", Position: &gitlab.NotePosition{
			BaseSHA:      "base",
			StartSHA:     "start",
			HeadSHA:      "head",
			PositionType: "file",
			NewPath:      "main.go",
			OldPath:      "main.go",
		}})
		tc.MockDraftNotes.EXPECT().
			UpdateDraftNote("OWNER/REPO", int64(1), int64(501), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ any, _ int64, _ int64, opts *gitlab.UpdateDraftNoteOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.DraftNote, *gitlab.Response, error) {
				assert.Equal(t, &gitlab.PositionOptions{
					BaseSHA:      new("base"),
					StartSHA:     new("start"),
					HeadSHA:      new("head"),
					PositionType: new("file"),
					NewPath:      new("main.go"),
					OldPath:      new("main.go"),
				}, opts.Position)
				return &gitlab.DraftNote{ID: 501}, nil, nil
			})

		_, err := setupExec(t, tc)(`1 501 -m "new"`)
		require.NoError(t, err)
	})

	t.Run("multiline diff comment re-sends its line range", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		mockGetDraft(tc, &gitlab.DraftNote{ID: 501, Note: "old", Position: &gitlab.NotePosition{
			BaseSHA:      "base",
			StartSHA:     "start",
			HeadSHA:      "head",
			PositionType: "text",
			NewPath:      "main.go",
			OldPath:      "main.go",
			NewLine:      12,
			OldLine:      11,
			LineRange: &gitlab.LineRange{
				StartRange: &gitlab.LinePosition{LineCode: "abc_0_10", Type: "new", NewLine: 10},
				EndRange:   &gitlab.LinePosition{LineCode: "abc_11_12", NewLine: 12, OldLine: 11},
			},
		}})
		tc.MockDraftNotes.EXPECT().
			UpdateDraftNote("OWNER/REPO", int64(1), int64(501), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ any, _ int64, _ int64, opts *gitlab.UpdateDraftNoteOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.DraftNote, *gitlab.Response, error) {
				require.NotNil(t, opts.Position)
				assert.Equal(t, new(int64(12)), opts.Position.NewLine)
				assert.Equal(t, new(int64(11)), opts.Position.OldLine)
				assert.Equal(t, &gitlab.LineRangeOptions{
					Start: &gitlab.LinePositionOptions{LineCode: new("abc_0_10"), Type: new("new"), NewLine: new(int64(10))},
					End:   &gitlab.LinePositionOptions{LineCode: new("abc_11_12"), NewLine: new(int64(12)), OldLine: new(int64(11))},
				}, opts.Position.LineRange)
				return &gitlab.DraftNote{ID: 501}, nil, nil
			})

		_, err := setupExec(t, tc)(`1 501 -m "new"`)
		require.NoError(t, err)
	})

	t.Run("comment on a removed line re-sends only the old side", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		mockGetDraft(tc, &gitlab.DraftNote{ID: 501, Note: "old", Position: &gitlab.NotePosition{
			BaseSHA:      "base",
			StartSHA:     "start",
			HeadSHA:      "head",
			PositionType: "text",
			OldPath:      "gone.go",
			NewPath:      "gone.go",
			OldLine:      7,
		}})
		tc.MockDraftNotes.EXPECT().
			UpdateDraftNote("OWNER/REPO", int64(1), int64(501), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ any, _ int64, _ int64, opts *gitlab.UpdateDraftNoteOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.DraftNote, *gitlab.Response, error) {
				require.NotNil(t, opts.Position)
				assert.Equal(t, new(int64(7)), opts.Position.OldLine)
				assert.Nil(t, opts.Position.NewLine)
				assert.Nil(t, opts.Position.LineRange)
				return &gitlab.DraftNote{ID: 501}, nil, nil
			})

		_, err := setupExec(t, tc)(`1 501 -m "new"`)
		require.NoError(t, err)
	})

	t.Run("image comment is refused before reading a body", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		mockGetDraft(tc, &gitlab.DraftNote{ID: 501, Note: "old", Position: &gitlab.NotePosition{
			PositionType: "image",
			NewPath:      "logo.png",
		}})

		_, err := setupExec(t, tc)(`1 501 -m "new"`)
		require.EqualError(t, err, "pending review comment 501 is on an image and cannot be updated without losing its position")
	})

	t.Run("reads the body from stdin", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		mockGetDraft(tc, &gitlab.DraftNote{ID: 501, Note: "old"})
		tc.MockDraftNotes.EXPECT().
			UpdateDraftNote("OWNER/REPO", int64(1), int64(501), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ any, _ int64, _ int64, opts *gitlab.UpdateDraftNoteOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.DraftNote, *gitlab.Response, error) {
				assert.Equal(t, "from stdin", *opts.Note)
				return &gitlab.DraftNote{ID: 501}, nil, nil
			})

		_, err := setupExec(t, tc, cmdtest.WithStdin("from stdin\n"))(`1 501`)
		require.NoError(t, err)
	})

	t.Run("--attach without --message appends to the existing body", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "screenshot.png")
		require.NoError(t, os.WriteFile(path, []byte("\x89PNG\r\n\x1a\nfake png body"), 0o600))

		tc := setupMR(t)
		mockGetDraft(tc, &gitlab.DraftNote{ID: 501, Note: "Renders wrong here."})
		tc.MockProjectMarkdownUploads.EXPECT().
			UploadProjectMarkdown("OWNER/REPO", gomock.Any(), "screenshot.png", gomock.Any()).
			Return(&gitlab.ProjectMarkdownUploadedFile{Markdown: "![screenshot](/uploads/abc/screenshot.png)"}, nil, nil)
		tc.MockDraftNotes.EXPECT().
			UpdateDraftNote("OWNER/REPO", int64(1), int64(501), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ any, _ int64, _ int64, opts *gitlab.UpdateDraftNoteOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.DraftNote, *gitlab.Response, error) {
				assert.Equal(t, "Renders wrong here.\n\n![screenshot](/uploads/abc/screenshot.png)", *opts.Note)
				return &gitlab.DraftNote{ID: 501}, nil, nil
			})

		_, err := setupExec(t, tc)(`1 501 --attach ` + path)
		require.NoError(t, err)
	})

	t.Run("empty body", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		mockGetDraft(tc, &gitlab.DraftNote{ID: 501, Note: "old"})

		_, err := setupExec(t, tc, cmdtest.WithStdin("  \n"))(`1 501`)
		require.EqualError(t, err, "aborted: comment has an empty message")
	})

	t.Run("a lone ID is the pending comment on the current branch's merge request", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		mockBranchMR(t, tc)
		mockGetDraft(tc, &gitlab.DraftNote{ID: 501, Note: "old"})
		tc.MockDraftNotes.EXPECT().
			UpdateDraftNote("OWNER/REPO", int64(1), int64(501), gomock.Any(), gomock.Any()).
			Return(&gitlab.DraftNote{ID: 501}, nil, nil)

		output, err := setupExec(t, tc, cmdtest.WithBranch("feature"))(`501 -m "new"`)
		require.NoError(t, err)
		assert.Equal(t, "501\n", output.String())
	})

	t.Run("--message with --attach replaces the body and appends the upload", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "screenshot.png")
		require.NoError(t, os.WriteFile(path, []byte("\x89PNG\r\n\x1a\nfake png body"), 0o600))

		tc := setupMR(t)
		mockGetDraft(tc, &gitlab.DraftNote{ID: 501, Note: "old body"})
		tc.MockProjectMarkdownUploads.EXPECT().
			UploadProjectMarkdown("OWNER/REPO", gomock.Any(), "screenshot.png", gomock.Any()).
			Return(&gitlab.ProjectMarkdownUploadedFile{Markdown: "![screenshot](/uploads/abc/screenshot.png)"}, nil, nil)
		tc.MockDraftNotes.EXPECT().
			UpdateDraftNote("OWNER/REPO", int64(1), int64(501), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ any, _ int64, _ int64, opts *gitlab.UpdateDraftNoteOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.DraftNote, *gitlab.Response, error) {
				assert.Equal(t, "new body\n\n![screenshot](/uploads/abc/screenshot.png)", *opts.Note)
				return &gitlab.DraftNote{ID: 501}, nil, nil
			})

		_, err := setupExec(t, tc)(`1 501 -m "new body" --attach ` + path)
		require.NoError(t, err)
	})

	t.Run("unknown ID", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		tc.MockDraftNotes.EXPECT().
			GetDraftNote("OWNER/REPO", int64(1), int64(999), gomock.Any()).
			Return(nil, nil, gitlab.ErrNotFound)

		_, err := setupExec(t, tc)(`1 999 -m "new"`)
		require.EqualError(t, err, "pending review comment 999 not found in merge request !1")
	})

	t.Run("non-numeric ID is rejected before any API call", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)

		_, err := setupExec(t, tc)(`1 abc -m "new"`)
		require.EqualError(t, err, `invalid pending review comment ID "abc": must be a number`)
	})

	t.Run("update error is wrapped", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		mockGetDraft(tc, &gitlab.DraftNote{ID: 501, Note: "old"})
		tc.MockDraftNotes.EXPECT().
			UpdateDraftNote("OWNER/REPO", int64(1), int64(501), gomock.Any(), gomock.Any()).
			Return(nil, nil, errors.New("boom"))

		_, err := setupExec(t, tc)(`1 501 -m "new"`)
		require.EqualError(t, err, "failed to update pending review comment: boom")
	})
}

func mockGetDraft(tc *gitlabtesting.TestClient, draft *gitlab.DraftNote) {
	tc.MockDraftNotes.EXPECT().
		GetDraftNote("OWNER/REPO", int64(1), draft.ID, gomock.Any()).
		Return(draft, nil, nil)
}

func setupMR(t *testing.T) *gitlabtesting.TestClient {
	t.Helper()
	tc := gitlabtesting.NewTestClient(t)
	tc.MockMergeRequests.EXPECT().
		GetMergeRequest("OWNER/REPO", int64(1), gomock.Any()).
		Return(&gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{ID: 1, IID: 1}}, nil, nil)
	return tc
}

// mockBranchMR resolves branch "feature" to merge request !1.
func mockBranchMR(t *testing.T, tc *gitlabtesting.TestClient) {
	t.Helper()
	tc.MockMergeRequests.EXPECT().
		ListProjectMergeRequests("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(_ any, opts *gitlab.ListProjectMergeRequestsOptions, _ ...gitlab.RequestOptionFunc) ([]*gitlab.BasicMergeRequest, *gitlab.Response, error) {
			assert.Equal(t, "feature", *opts.SourceBranch)
			return []*gitlab.BasicMergeRequest{{ID: 1, IID: 1}}, nil, nil
		})
}

func setupExec(t *testing.T, tc *gitlabtesting.TestClient, opts ...cmdtest.FactoryOption) cmdtest.CmdExecFunc {
	t.Helper()
	opts = append([]cmdtest.FactoryOption{
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		cmdtest.WithConfig(config.NewFromString("editor: vi")),
	}, opts...)
	return cmdtest.SetupCmdForTest(t, NewCmd, false, opts...)
}
