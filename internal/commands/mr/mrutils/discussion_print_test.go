//go:build !integration

package mrutils

import (
	"bytes"
	"testing"
	"time"

	"github.com/acarl005/stripansi"
	"github.com/stretchr/testify/assert"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_PrintDiscussions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		discussion  *gitlab.Discussion
		options     PrintDiscussionsOptions
		contains    string
		notContains string
	}{
		{
			name: "enabled individual one-note discussion",
			discussion: &gitlab.Discussion{
				ID:             "abc12345deadbeef1234567890abcdef12345678",
				IndividualNote: true,
				Notes: []*gitlab.Note{
					{ID: 101, Body: "Individual note", Author: gitlab.NoteAuthor{Username: "alice"}},
				},
			},
			options:  PrintDiscussionsOptions{ShowSingleNoteDiscussionPrefix: true},
			contains: "[note #101] [discussion: abc12345…]",
		},
		{
			name: "enabled non-individual one-note discussion",
			discussion: &gitlab.Discussion{
				ID:             "def67890deadbeef1234567890abcdef12345678",
				IndividualNote: false,
				Notes: []*gitlab.Note{
					{ID: 102, Body: "Single-note thread", Author: gitlab.NoteAuthor{Username: "bob"}},
				},
			},
			options:  PrintDiscussionsOptions{ShowSingleNoteDiscussionPrefix: true},
			contains: "[note #102] [discussion: def67890…]",
		},
		{
			name: "disabled one-note discussion",
			discussion: &gitlab.Discussion{
				ID:             "fedcba98deadbeef1234567890abcdef12345678",
				IndividualNote: true,
				Notes: []*gitlab.Note{
					{ID: 103, Body: "Hidden prefix", Author: gitlab.NoteAuthor{Username: "carol"}},
				},
			},
			contains:    "[note #103]",
			notContains: "[discussion:",
		},
		{
			name: "system note remains label-free",
			discussion: &gitlab.Discussion{
				ID:             "system12deadbeef1234567890abcdef12345678",
				IndividualNote: true,
				Notes: []*gitlab.Note{
					{ID: 104, Body: "changed status", System: true, Author: gitlab.NoteAuthor{Username: "bot"}},
				},
			},
			options:     PrintDiscussionsOptions{ShowSystemLogs: true, ShowSingleNoteDiscussionPrefix: true},
			contains:    "@bot changed status",
			notContains: "[discussion:",
		},
		{
			name: "empty ID one-note discussion remains label-free",
			discussion: &gitlab.Discussion{
				IndividualNote: true,
				Notes: []*gitlab.Note{
					{ID: 105, Body: "No ID", Author: gitlab.NoteAuthor{Username: "dana"}},
				},
			},
			options:     PrintDiscussionsOptions{ShowSingleNoteDiscussionPrefix: true},
			contains:    "[note #105]",
			notContains: "[discussion:",
		},
		{
			name: "non-empty multi-note thread header is unchanged",
			discussion: &gitlab.Discussion{
				ID:             "abc12345deadbeef1234567890abcdef12345678",
				IndividualNote: false,
				Notes: []*gitlab.Note{
					{ID: 106, Body: "First", Author: gitlab.NoteAuthor{Username: "erin"}},
					{ID: 107, Body: "Reply", Author: gitlab.NoteAuthor{Username: "frank"}},
				},
			},
			contains: "Thread [discussion: abc12345…]",
		},
		{
			name: "empty ID multi-note thread has no discussion label",
			discussion: &gitlab.Discussion{
				IndividualNote: false,
				Notes: []*gitlab.Note{
					{ID: 108, Body: "First", Author: gitlab.NoteAuthor{Username: "gina"}},
					{ID: 109, Body: "Reply", Author: gitlab.NoteAuthor{Username: "hank"}},
				},
			},
			contains:    "Thread\n",
			notContains: "[discussion:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ioStr, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
			var buf bytes.Buffer

			PrintDiscussions(&buf, ioStr, []*gitlab.Discussion{tt.discussion}, tt.options)

			out := stripansi.Strip(buf.String())
			assert.Contains(t, out, tt.contains)
			if tt.notContains != "" {
				assert.NotContains(t, out, tt.notContains)
			}
		})
	}
}

func Test_noteTimeAgo(t *testing.T) {
	t.Parallel()
	t.Run("combines relative and absolute time", func(t *testing.T) {
		t.Parallel()
		created := time.Now().Add(-24 * time.Hour)
		got := noteTimeAgo(&gitlab.Note{CreatedAt: &created})
		expected := "about 1 day ago (" + created.Format("2006-01-02 15:04:05") + ")"
		assert.Equal(t, expected, got)
	})

	t.Run("empty when CreatedAt is nil", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, noteTimeAgo(&gitlab.Note{}))
	})
}

func Test_PrintCommentFileContext(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	ioStr, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))
	c := ioStr.Color()

	tests := []struct {
		name     string
		note     *gitlab.Note
		expected string
	}{
		{
			name: "single line comment on new file",
			note: &gitlab.Note{
				Position: &gitlab.NotePosition{
					NewPath: "internal/commands/mr/view/mr_view.go",
					NewLine: 42,
				},
			},
			expected: " on internal/commands/mr/view/mr_view.go:42\n",
		},
		{
			name: "single line comment on old file",
			note: &gitlab.Note{
				Position: &gitlab.NotePosition{
					OldPath: "internal/commands/mr/view/mr_view.go",
					OldLine: 35,
				},
			},
			expected: " on internal/commands/mr/view/mr_view.go:35\n",
		},
		{
			name: "multi-line comment",
			note: &gitlab.Note{
				Position: &gitlab.NotePosition{
					NewPath: "internal/gateway/mcp/tools/get_coin_open_interest.go",
					LineRange: &gitlab.LineRange{
						StartRange: &gitlab.LinePosition{NewLine: 63},
						EndRange:   &gitlab.LinePosition{NewLine: 68},
					},
				},
			},
			expected: " on internal/gateway/mcp/tools/get_coin_open_interest.go:63-68\n",
		},
		{
			name: "single line range (same start and end)",
			note: &gitlab.Note{
				Position: &gitlab.NotePosition{
					NewPath: "main.go",
					LineRange: &gitlab.LineRange{
						StartRange: &gitlab.LinePosition{NewLine: 10},
						EndRange:   &gitlab.LinePosition{NewLine: 10},
					},
				},
			},
			expected: " on main.go:10\n",
		},
		{
			name: "position with no line numbers",
			note: &gitlab.Note{
				Position: &gitlab.NotePosition{
					NewPath: "file.go",
					NewLine: 0,
				},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			PrintCommentFileContext(&buf, c, tt.note.Position)
			got := buf.String()
			assert.Equal(t, tt.expected, got)
		})
	}
}
