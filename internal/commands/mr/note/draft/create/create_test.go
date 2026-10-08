//go:build !integration

package create

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

func TestDraftCreate(t *testing.T) {
	t.Parallel()

	t.Run("creates a general pending comment and prints its ID", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		tc.MockDraftNotes.EXPECT().
			CreateDraftNote("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ any, _ int64, opts *gitlab.CreateDraftNoteOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.DraftNote, *gitlab.Response, error) {
				assert.Equal(t, "Needs work", *opts.Note)
				assert.Nil(t, opts.Position)
				assert.Nil(t, opts.InReplyToDiscussionID)
				return &gitlab.DraftNote{ID: 601}, nil, nil
			})

		output, err := setupExec(t, tc)(`1 -m "Needs work"`)
		require.NoError(t, err)
		assert.Equal(t, "601\n", output.String())
		assert.Empty(t, output.Stderr())
	})

	t.Run("reads the body from stdin", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		tc.MockDraftNotes.EXPECT().
			CreateDraftNote("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ any, _ int64, opts *gitlab.CreateDraftNoteOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.DraftNote, *gitlab.Response, error) {
				assert.Equal(t, "from stdin", *opts.Note)
				return &gitlab.DraftNote{ID: 602}, nil, nil
			})

		output, err := setupExec(t, tc, cmdtest.WithStdin("from stdin\n"))(`1`)
		require.NoError(t, err)
		assert.Equal(t, "602\n", output.String())
	})

	t.Run("diff comment carries the resolved position", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		mockDiffVersion(t, tc)
		tc.MockDraftNotes.EXPECT().
			CreateDraftNote("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ any, _ int64, opts *gitlab.CreateDraftNoteOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.DraftNote, *gitlab.Response, error) {
				require.NotNil(t, opts.Position)
				assert.Equal(t, "main.go", *opts.Position.NewPath)
				assert.Equal(t, "head", *opts.Position.HeadSHA)
				assert.Equal(t, int64(2), *opts.Position.NewLine)
				assert.Nil(t, opts.InReplyToDiscussionID)
				return &gitlab.DraftNote{ID: 603}, nil, nil
			})

		output, err := setupExec(t, tc)(`1 --file main.go --line 2 -m "Off-by-one?"`)
		require.NoError(t, err)
		assert.Equal(t, "603\n", output.String())
	})

	t.Run("diff comment on a removed line", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		mockDiffVersion(t, tc)
		tc.MockDraftNotes.EXPECT().
			CreateDraftNote("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ any, _ int64, opts *gitlab.CreateDraftNoteOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.DraftNote, *gitlab.Response, error) {
				require.NotNil(t, opts.Position)
				assert.Equal(t, int64(2), *opts.Position.OldLine)
				assert.Nil(t, opts.Position.NewLine)
				return &gitlab.DraftNote{ID: 604}, nil, nil
			})

		_, err := setupExec(t, tc)(`1 --file main.go --old-line 2 -m "Why?"`)
		require.NoError(t, err)
	})

	t.Run("diff comment on a line range carries the line range", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		mockDiffVersion(t, tc)
		tc.MockDraftNotes.EXPECT().
			CreateDraftNote("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ any, _ int64, opts *gitlab.CreateDraftNoteOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.DraftNote, *gitlab.Response, error) {
				require.NotNil(t, opts.Position)
				assert.Equal(t, new(int64(3)), opts.Position.NewLine)
				require.NotNil(t, opts.Position.LineRange)
				assert.Equal(t, new(int64(2)), opts.Position.LineRange.Start.NewLine)
				assert.Equal(t, new(int64(3)), opts.Position.LineRange.End.NewLine)
				return &gitlab.DraftNote{ID: 607}, nil, nil
			})

		_, err := setupExec(t, tc)(`1 --file main.go --line 2:3 -m "Extract this."`)
		require.NoError(t, err)
	})

	t.Run("file-level comment targets the file without --line", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		mockDiffVersion(t, tc)
		tc.MockDraftNotes.EXPECT().
			CreateDraftNote("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ any, _ int64, opts *gitlab.CreateDraftNoteOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.DraftNote, *gitlab.Response, error) {
				require.NotNil(t, opts.Position)
				assert.Equal(t, "main.go", *opts.Position.NewPath)
				assert.Nil(t, opts.Position.LineRange)
				return &gitlab.DraftNote{ID: 608}, nil, nil
			})

		_, err := setupExec(t, tc)(`1 --file main.go -m "General note on this file."`)
		require.NoError(t, err)
	})

	t.Run("file missing from the diff fails before creating anything", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		mockDiffVersion(t, tc)

		_, err := setupExec(t, tc)(`1 --file nope.go --line 1 -m "hi"`)
		require.ErrorContains(t, err, `file "nope.go" not found in MR diff`)
	})

	t.Run("reply resolves a discussion ID prefix", func(t *testing.T) {
		t.Parallel()

		const fullID = "abc12345deadbeef1234567890abcdef12345678"

		tc := setupMR(t)
		tc.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return([]*gitlab.Discussion{{ID: fullID}}, &gitlab.Response{}, nil)
		tc.MockDraftNotes.EXPECT().
			CreateDraftNote("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ any, _ int64, opts *gitlab.CreateDraftNoteOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.DraftNote, *gitlab.Response, error) {
				require.NotNil(t, opts.InReplyToDiscussionID)
				assert.Equal(t, fullID, *opts.InReplyToDiscussionID)
				assert.Nil(t, opts.Position)
				return &gitlab.DraftNote{ID: 605}, nil, nil
			})

		output, err := setupExec(t, tc)(`1 --reply abc12345 -m "Agreed."`)
		require.NoError(t, err)
		assert.Equal(t, "605\n", output.String())
	})

	t.Run("attachment is appended to the body", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "screenshot.png")
		require.NoError(t, os.WriteFile(path, []byte("\x89PNG\r\n\x1a\nfake png body"), 0o600))

		tc := setupMR(t)
		tc.MockProjectMarkdownUploads.EXPECT().
			UploadProjectMarkdown("OWNER/REPO", gomock.Any(), "screenshot.png", gomock.Any()).
			Return(&gitlab.ProjectMarkdownUploadedFile{Markdown: "![screenshot](/uploads/abc/screenshot.png)"}, nil, nil)
		tc.MockDraftNotes.EXPECT().
			CreateDraftNote("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ any, _ int64, opts *gitlab.CreateDraftNoteOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.DraftNote, *gitlab.Response, error) {
				assert.Equal(t, "![screenshot](/uploads/abc/screenshot.png)", *opts.Note)
				return &gitlab.DraftNote{ID: 606}, nil, nil
			})

		_, err := setupExec(t, tc)(`1 --attach ` + path)
		require.NoError(t, err)
	})

	t.Run("API error is wrapped", func(t *testing.T) {
		t.Parallel()

		tc := setupMR(t)
		tc.MockDraftNotes.EXPECT().
			CreateDraftNote("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return(nil, nil, errors.New("boom"))

		_, err := setupExec(t, tc)(`1 -m "hi"`)
		require.ErrorContains(t, err, "failed to create pending review comment: boom")
	})
}

func TestDraftCreate_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cli     string
		stdin   string
		mockMR  bool
		wantErr string
	}{
		{
			name:    "empty body",
			cli:     `1`,
			stdin:   "  \n",
			mockMR:  true,
			wantErr: "aborted: comment has an empty message",
		},
		// No -m and no API mocks: these must fail before any request or body prompt.
		{
			name:    "reply prefix shorter than 8 characters",
			cli:     `1 --reply abc`,
			wantErr: "discussion ID prefix must be at least 8 characters, got 3",
		},
		{
			name:    "--line without --file",
			cli:     `1 --line 5`,
			wantErr: "--line and --old-line require --file",
		},
		{
			name:    "--old-line without --file",
			cli:     `1 --old-line 5`,
			wantErr: "--line and --old-line require --file",
		},
		{
			name:    "malformed --line",
			cli:     `1 --file main.go --line 10-15`,
			wantErr: `invalid argument "10-15" for "--line" flag: invalid line number "10-15"`,
		},
		{
			name:    "--reply with --file",
			cli:     `1 --reply abc12345 --file main.go -m "hi"`,
			wantErr: "if any flags in the group [reply file] are set none of the others can be",
		},
		{
			name:    "--line with --old-line",
			cli:     `1 --file main.go --line 1 --old-line 1 -m "hi"`,
			wantErr: "if any flags in the group [line old-line] are set none of the others can be",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tc := gitlabtesting.NewTestClient(t)
			if tt.mockMR {
				mockMR1(t, tc)
			}

			_, err := setupExec(t, tc, cmdtest.WithStdin(tt.stdin))(tt.cli)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func setupMR(t *testing.T) *gitlabtesting.TestClient {
	t.Helper()
	tc := gitlabtesting.NewTestClient(t)
	mockMR1(t, tc)
	return tc
}

func mockMR1(t *testing.T, tc *gitlabtesting.TestClient) {
	t.Helper()
	tc.MockMergeRequests.EXPECT().
		GetMergeRequest("OWNER/REPO", int64(1), gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:     1,
				IID:    1,
				WebURL: "https://gitlab.com/OWNER/REPO/merge_requests/1",
			},
		}, nil, nil)
}

func mockDiffVersion(t *testing.T, tc *gitlabtesting.TestClient) {
	t.Helper()
	tc.MockMergeRequests.EXPECT().
		GetMergeRequestDiffVersions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
		Return([]*gitlab.MergeRequestDiffVersion{{ID: 10}}, nil, nil)
	tc.MockMergeRequests.EXPECT().
		GetSingleMergeRequestDiffVersion("OWNER/REPO", int64(1), int64(10), gomock.Any(), gomock.Any()).
		Return(&gitlab.MergeRequestDiffVersion{
			ID:             10,
			BaseCommitSHA:  "base",
			HeadCommitSHA:  "head",
			StartCommitSHA: "start",
			Diffs: []*gitlab.Diff{{
				NewPath: "main.go",
				OldPath: "main.go",
				Diff:    "@@ -1,3 +1,3 @@\n line1\n-old line2\n+new line2\n line3\n",
			}},
		}, nil, nil)
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
