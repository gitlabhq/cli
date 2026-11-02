package mrutils

import (
	"context"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

// NoteBodyFromStdinOrEditor is cmdutils.NoteBodyFromStdinOrEditor with the
// editor prompt worded for a merge request note.
func NoteBodyFromStdinOrEditor(ctx context.Context, ios *iostreams.IOStreams, cfg func() config.Config) (string, error) {
	return cmdutils.NoteBodyFromStdinOrEditor(ctx, ios, cfg, "Note message:", "Enter the note message for the merge request.")
}

// NotePreview returns body on a single line, cut to 80 runes, for showing
// in a confirmation prompt.
func NotePreview(body string) string {
	if r := []rune(body); len(r) > 80 {
		body = string(r[:80]) + "..."
	}
	return strings.ReplaceAll(body, "\n", " ")
}
