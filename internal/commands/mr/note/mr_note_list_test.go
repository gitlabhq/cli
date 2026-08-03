//go:build !integration

package note

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/acarl005/stripansi"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// makeMRForList is an alias for mockMR1 kept for readability in this file.
var makeMRForList = mockMR1

func setupListCmd(t *testing.T, tc *gitlabtesting.TestClient) cmdtest.CmdExecFunc {
	t.Helper()
	return cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
		return NewCmdNote(f)
	}, true,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)
}

func ts(s string) *time.Time {
	t, _ := time.Parse(time.DateTime, s)
	return &t
}

func Test_NoteList(t *testing.T) {
	t.Parallel()

	t.Run("lists all discussions using shared TTY format", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		makeMRForList(t, tc)

		tc.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return([]*gitlab.Discussion{
				{
					ID:             "abcdef1234567890abcdef1234567890abcdef12",
					IndividualNote: true,
					Notes: []*gitlab.Note{
						{
							ID:        100,
							Body:      "General comment",
							Author:    gitlab.NoteAuthor{Username: "alice"},
							CreatedAt: ts("2025-01-15 10:30:00"),
						},
					},
				},
				{
					ID:             "12345678abcdef90abcdef1234567890abcdef12",
					IndividualNote: true,
					Notes: []*gitlab.Note{
						{
							ID:         200,
							Body:       "Diff note",
							Author:     gitlab.NoteAuthor{Username: "bob"},
							CreatedAt:  ts("2025-01-15 11:00:00"),
							Position:   &gitlab.NotePosition{NewPath: "main.go", OldPath: "main.go", NewLine: 42},
							Resolvable: true,
							Resolved:   false,
						},
					},
				},
			}, nil, nil)

		exec := setupListCmd(t, tc)
		output, err := exec(`list 1`)
		require.NoError(t, err)

		out := stripansi.Strip(output.String())
		// Uses shared PrintDiscussions renderer
		assert.Contains(t, out, "@alice commented")
		assert.Contains(t, out, "[note #100] [discussion: abcdef12…]")
		assert.Contains(t, out, "General comment")
		assert.Contains(t, out, "@bob commented")
		assert.Contains(t, out, "[note #200] [discussion: 12345678…]")
		assert.Contains(t, out, "Diff note")
		assert.Contains(t, out, "main.go")
		assert.Contains(t, out, ":42")
	})

	t.Run("lists a single note non-individual discussion with a prefix", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		makeMRForList(t, tc)

		tc.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return([]*gitlab.Discussion{
				{
					ID:             "fedcba9876543210fedcba9876543210fedcba98",
					IndividualNote: false,
					Notes: []*gitlab.Note{
						{
							ID:        301,
							Body:      "Single-note discussion",
							Author:    gitlab.NoteAuthor{Username: "carol"},
							CreatedAt: ts("2025-01-15 12:00:00"),
						},
					},
				},
			}, nil, nil)

		exec := setupListCmd(t, tc)
		output, err := exec(`list 1`)
		require.NoError(t, err)

		out := stripansi.Strip(output.String())
		assert.Contains(t, out, "[note #301] [discussion: fedcba98…]")
	})

	t.Run("no discussions found", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		makeMRForList(t, tc)

		tc.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return([]*gitlab.Discussion{}, nil, nil)

		exec := setupListCmd(t, tc)
		output, err := exec(`list 1`)
		require.NoError(t, err)
		assert.Contains(t, output.String(), "No discussions found.")
	})

	t.Run("filter by diff", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		makeMRForList(t, tc)

		tc.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return([]*gitlab.Discussion{
				{
					ID:             "general1234567890abcdef1234567890abcdef12",
					IndividualNote: true,
					Notes: []*gitlab.Note{
						{ID: 100, Body: "General", Author: gitlab.NoteAuthor{Username: "alice"}, CreatedAt: ts("2025-01-15 10:00:00")},
					},
				},
				{
					ID:             "diffnote234567890abcdef1234567890abcdef12",
					IndividualNote: true,
					Notes: []*gitlab.Note{
						{
							ID: 200, Body: "Diff comment", Author: gitlab.NoteAuthor{Username: "bob"},
							Position:  &gitlab.NotePosition{NewPath: "main.go", OldPath: "main.go", NewLine: 5},
							CreatedAt: ts("2025-01-15 11:00:00"),
						},
					},
				},
			}, nil, nil)

		exec := setupListCmd(t, tc)
		output, err := exec(`list 1 --type diff`)
		require.NoError(t, err)

		out := output.String()
		assert.NotContains(t, out, "General")
		assert.Contains(t, out, "Diff comment")
		assert.Contains(t, out, "main.go")
		assert.Contains(t, out, ":5")
	})

	t.Run("filter by general", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		makeMRForList(t, tc)

		tc.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return([]*gitlab.Discussion{
				{
					ID:             "general1234567890abcdef1234567890abcdef12",
					IndividualNote: true,
					Notes: []*gitlab.Note{
						{ID: 100, Body: "General note", Author: gitlab.NoteAuthor{Username: "alice"}, CreatedAt: ts("2025-01-15 10:00:00")},
					},
				},
				{
					ID:             "diffnote234567890abcdef1234567890abcdef12",
					IndividualNote: true,
					Notes: []*gitlab.Note{
						{
							ID: 200, Body: "Diff", Author: gitlab.NoteAuthor{Username: "bob"},
							Position:  &gitlab.NotePosition{NewPath: "main.go", OldPath: "main.go", NewLine: 5},
							CreatedAt: ts("2025-01-15 11:00:00"),
						},
					},
				},
			}, nil, nil)

		exec := setupListCmd(t, tc)
		output, err := exec(`list 1 --type general`)
		require.NoError(t, err)

		out := output.String()
		assert.Contains(t, out, "General note")
		assert.NotContains(t, out, "@bob")
	})

	t.Run("filter by system shows system notes", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		makeMRForList(t, tc)

		tc.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return([]*gitlab.Discussion{
				{
					ID:             "general1234567890abcdef1234567890abcdef12",
					IndividualNote: true,
					Notes: []*gitlab.Note{
						{ID: 100, Body: "General", Author: gitlab.NoteAuthor{Username: "alice"}, CreatedAt: ts("2025-01-15 10:00:00")},
					},
				},
				{
					ID:             "systemnote34567890abcdef1234567890abcdef12",
					IndividualNote: true,
					Notes: []*gitlab.Note{
						{ID: 300, Body: "merged", Author: gitlab.NoteAuthor{Username: "bot"}, System: true, CreatedAt: ts("2025-01-15 12:00:00")},
					},
				},
			}, nil, nil)

		exec := setupListCmd(t, tc)
		output, err := exec(`list 1 --type system`)
		require.NoError(t, err)

		out := output.String()
		assert.NotContains(t, out, "General")
		assert.Contains(t, out, "@bot")
		assert.Contains(t, out, "merged")
	})

	t.Run("state resolved", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		makeMRForList(t, tc)

		tc.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return([]*gitlab.Discussion{
				{
					ID:             "resolved234567890abcdef1234567890abcdef12",
					IndividualNote: true,
					Notes: []*gitlab.Note{
						{
							ID: 100, Body: "Done", Author: gitlab.NoteAuthor{Username: "alice"},
							Resolvable: true, Resolved: true, CreatedAt: ts("2025-01-15 10:00:00"),
						},
					},
				},
				{
					ID:             "unresolv234567890abcdef1234567890abcdef12",
					IndividualNote: true,
					Notes: []*gitlab.Note{
						{
							ID: 200, Body: "TODO", Author: gitlab.NoteAuthor{Username: "bob"},
							Resolvable: true, Resolved: false, CreatedAt: ts("2025-01-15 11:00:00"),
						},
					},
				},
			}, nil, nil)

		exec := setupListCmd(t, tc)
		output, err := exec(`list 1 --state resolved`)
		require.NoError(t, err)

		out := output.String()
		assert.Contains(t, out, "Done")
		assert.NotContains(t, out, "TODO")
	})

	t.Run("state unresolved", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		makeMRForList(t, tc)

		tc.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return([]*gitlab.Discussion{
				{
					ID:             "resolved234567890abcdef1234567890abcdef12",
					IndividualNote: true,
					Notes: []*gitlab.Note{
						{
							ID: 100, Body: "Done", Author: gitlab.NoteAuthor{Username: "alice"},
							Resolvable: true, Resolved: true, CreatedAt: ts("2025-01-15 10:00:00"),
						},
					},
				},
				{
					ID:             "unresolv234567890abcdef1234567890abcdef12",
					IndividualNote: true,
					Notes: []*gitlab.Note{
						{
							ID: 200, Body: "TODO item", Author: gitlab.NoteAuthor{Username: "bob"},
							Resolvable: true, Resolved: false, CreatedAt: ts("2025-01-15 11:00:00"),
						},
					},
				},
			}, nil, nil)

		exec := setupListCmd(t, tc)
		output, err := exec(`list 1 --state unresolved`)
		require.NoError(t, err)

		out := output.String()
		assert.NotContains(t, out, "Done")
		assert.Contains(t, out, "TODO item")
	})

	t.Run("filter by file", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		makeMRForList(t, tc)

		tc.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return([]*gitlab.Discussion{
				{
					ID:             "filemain234567890abcdef1234567890abcdef12",
					IndividualNote: true,
					Notes: []*gitlab.Note{
						{
							ID: 100, Body: "Comment on main", Author: gitlab.NoteAuthor{Username: "alice"},
							Position:  &gitlab.NotePosition{NewPath: "main.go", OldPath: "main.go", NewLine: 10},
							CreatedAt: ts("2025-01-15 10:00:00"),
						},
					},
				},
				{
					ID:             "fileutil234567890abcdef1234567890abcdef12",
					IndividualNote: true,
					Notes: []*gitlab.Note{
						{
							ID: 200, Body: "Comment on utils", Author: gitlab.NoteAuthor{Username: "bob"},
							Position:  &gitlab.NotePosition{NewPath: "utils.go", OldPath: "utils.go", NewLine: 5},
							CreatedAt: ts("2025-01-15 11:00:00"),
						},
					},
				},
				{
					ID:             "generalx234567890abcdef1234567890abcdef12",
					IndividualNote: true,
					Notes: []*gitlab.Note{
						{ID: 300, Body: "General note", Author: gitlab.NoteAuthor{Username: "carol"}, CreatedAt: ts("2025-01-15 12:00:00")},
					},
				},
			}, nil, nil)

		exec := setupListCmd(t, tc)
		output, err := exec(`list 1 --file main.go`)
		require.NoError(t, err)

		out := output.String()
		assert.Contains(t, out, "Comment on main")
		assert.NotContains(t, out, "Comment on utils")
		assert.NotContains(t, out, "General note")
	})

	t.Run("json output", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		makeMRForList(t, tc)

		tc.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return([]*gitlab.Discussion{
				{
					ID: "jsontest234567890abcdef1234567890abcdef12",
					Notes: []*gitlab.Note{
						{ID: 100, Body: "Hello", Author: gitlab.NoteAuthor{Username: "alice"}},
					},
				},
			}, nil, nil)

		exec := setupListCmd(t, tc)
		output, err := exec(`list 1 -F json`)
		require.NoError(t, err)

		var parsed []struct {
			ID string `json:"id"`
		}
		err = json.Unmarshal([]byte(output.String()), &parsed)
		require.NoError(t, err)
		require.Len(t, parsed, 1)
		assert.Equal(t, "jsontest234567890abcdef1234567890abcdef12", parsed[0].ID)
	})

	t.Run("threaded discussion with replies", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		makeMRForList(t, tc)

		tc.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return([]*gitlab.Discussion{
				{
					ID:             "thread12345678901234567890abcdef12345678",
					IndividualNote: false,
					Notes: []*gitlab.Note{
						{
							ID: 100, Body: "First note", Author: gitlab.NoteAuthor{Username: "alice"},
							CreatedAt: ts("2025-01-15 10:00:00"),
						},
						{
							ID: 101, Body: "Reply here", Author: gitlab.NoteAuthor{Username: "bob"},
							CreatedAt: ts("2025-01-15 10:05:00"),
						},
					},
				},
			}, nil, nil)

		exec := setupListCmd(t, tc)
		output, err := exec(`list 1`)
		require.NoError(t, err)

		out := output.String()
		// Thread format from PrintDiscussions
		assert.Contains(t, out, "Thread [discussion: thread12…]")
		assert.Contains(t, out, "@alice commented")
		assert.Contains(t, out, "[note #100]")
		assert.Contains(t, out, "First note")
		assert.Contains(t, out, "@bob replied")
		assert.Contains(t, out, "[note #101]")
		assert.Contains(t, out, "Reply here")
	})

	t.Run("combined filters", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		makeMRForList(t, tc)

		tc.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return([]*gitlab.Discussion{
				{
					ID:             "diffresol234567890abcdef1234567890abcdef12",
					IndividualNote: true,
					Notes: []*gitlab.Note{
						{
							ID: 100, Body: "Resolved diff", Author: gitlab.NoteAuthor{Username: "alice"},
							Position:   &gitlab.NotePosition{NewPath: "main.go", OldPath: "main.go", NewLine: 10},
							Resolvable: true, Resolved: true, CreatedAt: ts("2025-01-15 10:00:00"),
						},
					},
				},
				{
					ID:             "diffunres234567890abcdef1234567890abcdef12",
					IndividualNote: true,
					Notes: []*gitlab.Note{
						{
							ID: 200, Body: "Unresolved diff", Author: gitlab.NoteAuthor{Username: "bob"},
							Position:   &gitlab.NotePosition{NewPath: "main.go", OldPath: "main.go", NewLine: 20},
							Resolvable: true, Resolved: false, CreatedAt: ts("2025-01-15 11:00:00"),
						},
					},
				},
				{
					ID:             "genunres234567890abcdef1234567890abcdef12",
					IndividualNote: true,
					Notes: []*gitlab.Note{
						{
							ID: 300, Body: "Unresolved general", Author: gitlab.NoteAuthor{Username: "carol"},
							Resolvable: true, Resolved: false, CreatedAt: ts("2025-01-15 12:00:00"),
						},
					},
				},
			}, nil, nil)

		exec := setupListCmd(t, tc)
		output, err := exec(`list 1 --type diff --state unresolved`)
		require.NoError(t, err)

		out := output.String()
		assert.NotContains(t, out, "Resolved diff")      // resolved, excluded
		assert.Contains(t, out, "Unresolved diff")       // unresolved diff, included
		assert.NotContains(t, out, "Unresolved general") // general, excluded by type
	})

	t.Run("--type all includes system notes in text output", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		makeMRForList(t, tc)

		tc.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return([]*gitlab.Discussion{
				{
					ID:             "general1234567890abcdef1234567890abcdef12",
					IndividualNote: true,
					Notes: []*gitlab.Note{
						{ID: 100, Body: "General comment", Author: gitlab.NoteAuthor{Username: "alice"}, CreatedAt: ts("2025-01-15 10:00:00")},
					},
				},
				{
					ID:             "systemnote34567890abcdef1234567890abcdef12",
					IndividualNote: true,
					Notes: []*gitlab.Note{
						{ID: 200, Body: "assigned to alice", Author: gitlab.NoteAuthor{Username: "bot"}, System: true, CreatedAt: ts("2025-01-15 11:00:00")},
					},
				},
			}, nil, nil)

		exec := setupListCmd(t, tc)
		output, err := exec(`list 1 --type all`)
		require.NoError(t, err)

		out := output.String()
		assert.Contains(t, out, "General comment")
		assert.Contains(t, out, "assigned to alice")
	})

	t.Run("default (no --type flag) includes system notes in text output", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		makeMRForList(t, tc)

		tc.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return([]*gitlab.Discussion{
				{
					ID:             "general1234567890abcdef1234567890abcdef12",
					IndividualNote: true,
					Notes: []*gitlab.Note{
						{ID: 100, Body: "General comment", Author: gitlab.NoteAuthor{Username: "alice"}, CreatedAt: ts("2025-01-15 10:00:00")},
					},
				},
				{
					ID:             "systemnote34567890abcdef1234567890abcdef12",
					IndividualNote: true,
					Notes: []*gitlab.Note{
						{ID: 200, Body: "assigned to alice", Author: gitlab.NoteAuthor{Username: "bot"}, System: true, CreatedAt: ts("2025-01-15 11:00:00")},
					},
				},
			}, nil, nil)

		exec := setupListCmd(t, tc)
		output, err := exec(`list 1`)
		require.NoError(t, err)

		out := output.String()
		assert.Contains(t, out, "General comment")
		assert.Contains(t, out, "assigned to alice")
	})

	t.Run("non-resolvable excluded from state filter", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		makeMRForList(t, tc)

		tc.MockDiscussions.EXPECT().
			ListMergeRequestDiscussions("OWNER/REPO", int64(1), gomock.Any(), gomock.Any()).
			Return([]*gitlab.Discussion{
				{
					ID:             "nonresol234567890abcdef1234567890abcdef12",
					IndividualNote: true,
					Notes: []*gitlab.Note{
						{
							ID: 100, Body: "System note", Author: gitlab.NoteAuthor{Username: "bot"},
							System: true, Resolvable: false, CreatedAt: ts("2025-01-15 10:00:00"),
						},
					},
				},
				{
					ID:             "resolvabl34567890abcdef1234567890abcdef12",
					IndividualNote: true,
					Notes: []*gitlab.Note{
						{
							ID: 200, Body: "Unresolved note", Author: gitlab.NoteAuthor{Username: "alice"},
							Resolvable: true, Resolved: false, CreatedAt: ts("2025-01-15 11:00:00"),
						},
					},
				},
			}, nil, nil)

		exec := setupListCmd(t, tc)
		output, err := exec(`list 1 --state unresolved`)
		require.NoError(t, err)

		out := output.String()
		assert.NotContains(t, out, "System note")  // non-resolvable, excluded by filter
		assert.Contains(t, out, "Unresolved note") // resolvable + unresolved, included
	})

	t.Run("help explains discussion IDs for replies", func(t *testing.T) {
		t.Parallel()

		var output bytes.Buffer
		cmd := NewCmdNote(cmdtest.NewTestFactory(nil))
		cmd.SetArgs([]string{"list", "--help"})
		cmd.SetOut(&output)
		require.NoError(t, cmd.Execute())

		out := output.String()
		assert.Contains(t, out, "eight-character prefix")
		assert.Contains(t, out, "characters before the ellipsis")
		assert.Contains(t, out, "glab mr note create --reply")
		assert.Contains(t, out, "glab mr note list -F json | jq -r '.[].id'")
		assert.Contains(t, out, "the `id` field of each discussion object")
		assert.NotContains(t, out, "top-level `.id`")
		assert.NotContains(t, out, "Uses the same output format as `glab mr view --comments`.")
	})
}
