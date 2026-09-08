//go:build !integration

package note

import (
	"errors"
	"net/http"
	"testing"

	"git.sr.ht/~timofurrer/ugh"
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

func TestMain(m *testing.M) {
	cmdtest.InitTest(m, "mr_note_test")
}

func Test_NewCmdNote(t *testing.T) {
	t.Parallel()

	t.Run("--message flag specified", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)

		// Mock GetMergeRequest
		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(1), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:     1,
					IID:    1,
					WebURL: "https://gitlab.com/OWNER/REPO/merge_requests/1",
				},
			}, nil, nil)

		// Mock CreateMergeRequestNote
		testClient.MockNotes.EXPECT().
			CreateMergeRequestNote("OWNER/REPO", int64(1), gomock.Any()).
			DoAndReturn(func(pid any, mrIID int64, opts *gitlab.CreateMergeRequestNoteOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Note, *gitlab.Response, error) {
				assert.Equal(t, "Here is my note", *opts.Body)
				return &gitlab.Note{
					ID:           301,
					NoteableID:   1,
					NoteableType: "MergeRequest",
					NoteableIID:  1,
				}, nil, nil
			})

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdNote(f)
		}, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			cmdtest.WithConfig(config.NewFromString("editor: vi")),
		)

		output, err := exec(`1 --message "Here is my note"`)
		require.NoError(t, err)
		assert.Empty(t, output.Stderr())
		assert.Equal(t, "https://gitlab.com/OWNER/REPO/merge_requests/1#note_301\n", output.String())
	})

	t.Run("merge request not found", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)

		// Mock GetMergeRequest - returns 404
		notFoundResp := &gitlab.Response{
			Response: &http.Response{StatusCode: http.StatusNotFound},
		}
		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(122), gomock.Any()).
			Return(nil, notFoundResp, gitlab.ErrNotFound)

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdNote(f)
		}, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			cmdtest.WithConfig(config.NewFromString("editor: vi")),
		)

		_, err := exec(`122`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Not Found")
	})
}

func Test_NewCmdNote_error(t *testing.T) {
	t.Parallel()

	t.Run("note could not be created", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)

		// Mock GetMergeRequest
		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(1), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:     1,
					IID:    1,
					WebURL: "https://gitlab.com/OWNER/REPO/merge_requests/1",
				},
			}, nil, nil)

		// Mock CreateMergeRequestNote - returns 401
		unauthorizedResp := &gitlab.Response{
			Response: &http.Response{StatusCode: http.StatusUnauthorized},
		}
		testClient.MockNotes.EXPECT().
			CreateMergeRequestNote("OWNER/REPO", int64(1), gomock.Any()).
			Return(nil, unauthorizedResp, errors.New("401 Unauthorized"))

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdNote(f)
		}, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			cmdtest.WithConfig(config.NewFromString("editor: vi")),
		)

		_, err := exec(`1 -m "Some message"`)
		require.Error(t, err)
	})
}

func Test_mrNoteCreate_prompt(t *testing.T) {
	// NOTE: This test cannot run in parallel because the huh form library
	// uses global state (charmbracelet/bubbles runeutil sanitizer).

	t.Run("message provided", func(t *testing.T) {
		testClient := gitlabtesting.NewTestClient(t)

		// Mock GetMergeRequest
		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(1), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:     1,
					IID:    1,
					WebURL: "https://gitlab.com/OWNER/REPO/merge_requests/1",
				},
			}, nil, nil)

		// Mock CreateMergeRequestNote
		testClient.MockNotes.EXPECT().
			CreateMergeRequestNote("OWNER/REPO", int64(1), gomock.Any()).
			DoAndReturn(func(pid any, mrIID int64, opts *gitlab.CreateMergeRequestNoteOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Note, *gitlab.Response, error) {
				assert.Contains(t, *opts.Body, "some note message")
				return &gitlab.Note{
					ID:           301,
					NoteableID:   1,
					NoteableType: "MergeRequest",
					NoteableIID:  1,
				}, nil, nil
			})

		c := ugh.New(t)
		c.Expect(ugh.Input("Note message:")).
			Do(ugh.Type("some note message"))

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdNote(f)
		}, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			cmdtest.WithConfig(config.NewFromString("editor: vi")),
			cmdtest.WithConsole(t, c),
		)

		output, err := exec(`1`)
		require.NoError(t, err)
		assert.Empty(t, output.Stderr())
		assert.Contains(t, output.String(), "https://gitlab.com/OWNER/REPO/merge_requests/1#note_301")
	})

	t.Run("message is empty", func(t *testing.T) {
		testClient := gitlabtesting.NewTestClient(t)

		// Mock GetMergeRequest
		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(1), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:     1,
					IID:    1,
					WebURL: "https://gitlab.com/OWNER/REPO/merge_requests/1",
				},
			}, nil, nil)

		c := ugh.New(t)
		c.Expect(ugh.Input("Note message:")).
			Do(ugh.Type(""))

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdNote(f)
		}, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			cmdtest.WithConfig(config.NewFromString("editor: vi")),
			cmdtest.WithConsole(t, c),
		)

		_, err := exec(`1`)
		require.Error(t, err)
		assert.Equal(t, "aborted: note has an empty message", err.Error())
	})
}

func Test_mrNoteCreate_no_duplicate(t *testing.T) {
	// NOTE: This test cannot run in parallel because the huh form library
	// uses global state (charmbracelet/bubbles runeutil sanitizer).

	t.Run("message provided", func(t *testing.T) {
		testClient := gitlabtesting.NewTestClient(t)

		// Mock GetMergeRequest
		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(1), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:     1,
					IID:    1,
					WebURL: "https://gitlab.com/OWNER/REPO/merge_requests/1",
				},
			}, nil, nil)

		// Mock ListMergeRequestNotes - returns existing notes including the duplicate
		testClient.MockNotes.EXPECT().
			ListMergeRequestNotes("OWNER/REPO", int64(1), gomock.Any()).
			Return([]*gitlab.Note{
				{ID: 0, Body: "aaa"},
				{ID: 111, Body: "bbb"},
				{ID: 222, Body: "some note message"},
				{ID: 333, Body: "ccc"},
			}, nil, nil)

		c := ugh.New(t)
		c.Expect(ugh.Input("Note message:")).
			Do(ugh.Type("some note message"))

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdNote(f)
		}, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			cmdtest.WithConfig(config.NewFromString("editor: vi")),
			cmdtest.WithConsole(t, c),
		)

		output, err := exec(`1 --unique`)
		require.NoError(t, err)
		assert.Empty(t, output.Stderr())
		assert.Contains(t, output.String(), "https://gitlab.com/OWNER/REPO/merge_requests/1#note_222")
	})
}

func Test_mrNote_resolve(t *testing.T) {
	t.Parallel()

	t.Run("resolve discussion by note ID", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)

		// Mock GetMergeRequest
		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(1), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:     1,
					IID:    1,
					WebURL: "https://gitlab.com/OWNER/REPO/merge_requests/1",
				},
			}, nil, nil)

		// Mock ListMergeRequestDiscussions
		testClient.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return([]*gitlab.Discussion{
				{
					ID: "abc123",
					Notes: []*gitlab.Note{
						{ID: 100, Body: "First discussion"},
					},
				},
				{
					ID: "def456",
					Notes: []*gitlab.Note{
						{ID: 200, Body: "Second discussion"},
						{ID: 201, Body: "Reply to second"},
					},
				},
			}, nil, nil)

		// Mock ResolveMergeRequestDiscussion
		testClient.MockDiscussions.EXPECT().
			ResolveMergeRequestDiscussion("OWNER/REPO", int64(1), "def456", gomock.Any(), gomock.Any()).
			Return(&gitlab.Discussion{ID: "def456"}, nil, nil)

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdNote(f)
		}, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			cmdtest.WithConfig(config.NewFromString("editor: vi")),
		)

		output, err := exec(`1 --resolve 200`)
		require.NoError(t, err)
		assert.Empty(t, output.Stderr())
		assert.Contains(t, output.String(), "✓ Discussion resolved (note #200 in !1)")
	})

	t.Run("note not found", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)

		// Mock GetMergeRequest
		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(1), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:     1,
					IID:    1,
					WebURL: "https://gitlab.com/OWNER/REPO/merge_requests/1",
				},
			}, nil, nil)

		// Mock ListMergeRequestDiscussions - note 999 doesn't exist
		testClient.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return([]*gitlab.Discussion{
				{
					ID: "abc123",
					Notes: []*gitlab.Note{
						{ID: 100, Body: "First discussion"},
					},
				},
			}, nil, nil)

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdNote(f)
		}, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			cmdtest.WithConfig(config.NewFromString("editor: vi")),
		)

		_, err := exec(`1 --resolve 999`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "note 999 not found in merge request !1")
	})
}

func Test_mrNote_unresolve(t *testing.T) {
	t.Parallel()

	t.Run("unresolve discussion by note ID", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)

		// Mock GetMergeRequest
		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(1), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:     1,
					IID:    1,
					WebURL: "https://gitlab.com/OWNER/REPO/merge_requests/1",
				},
			}, nil, nil)

		// Mock ListMergeRequestDiscussions
		testClient.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return([]*gitlab.Discussion{
				{
					ID: "abc123",
					Notes: []*gitlab.Note{
						{ID: 100, Body: "First discussion"},
					},
				},
				{
					ID: "ghi789",
					Notes: []*gitlab.Note{
						{ID: 300, Body: "Third discussion"},
					},
				},
			}, nil, nil)

		// Mock ResolveMergeRequestDiscussion with Resolved: false
		testClient.MockDiscussions.EXPECT().
			ResolveMergeRequestDiscussion("OWNER/REPO", int64(1), "ghi789", gomock.Any(), gomock.Any()).
			Return(&gitlab.Discussion{ID: "ghi789"}, nil, nil)

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdNote(f)
		}, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			cmdtest.WithConfig(config.NewFromString("editor: vi")),
		)

		output, err := exec(`1 --unresolve 300`)
		require.NoError(t, err)
		assert.Empty(t, output.Stderr())
		assert.Contains(t, output.String(), "✓ Discussion unresolved (note #300 in !1)")
	})
}
