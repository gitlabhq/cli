package mrutils

import (
	"context"
	"fmt"
	"io"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

// NoteBodyFromStdinOrEditor reads a note body from standard input when it is
// not a terminal, and from the configured editor otherwise.
func NoteBodyFromStdinOrEditor(ctx context.Context, ios *iostreams.IOStreams, cfg func() config.Config) (string, error) {
	if !ios.IsInTTY {
		data, err := io.ReadAll(ios.In)
		if err != nil {
			return "", fmt.Errorf("failed to read from stdin: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}

	editor, err := cmdutils.GetEditor(cfg)
	if err != nil {
		return "", err
	}

	var body string
	if err := ios.Editor(ctx, &body, "Note message:", "Enter the note message for the merge request.", "", editor); err != nil {
		return "", err
	}
	return body, nil
}

// NotePreview returns body on a single line, cut to 80 runes, for showing
// in a confirmation prompt.
func NotePreview(body string) string {
	if r := []rune(body); len(r) > 80 {
		body = string(r[:80]) + "..."
	}
	return strings.ReplaceAll(body, "\n", " ")
}
