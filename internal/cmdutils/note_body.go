package cmdutils

import (
	"context"
	"fmt"
	"io"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

// NoteBodyFromStdinOrEditor reads a note body from standard input when it is
// not a terminal, and from the configured editor otherwise. Input read from
// standard input is trimmed of surrounding whitespace.
//
// title and help are the heading and help line of the editor prompt.
func NoteBodyFromStdinOrEditor(ctx context.Context, ios *iostreams.IOStreams, cfg func() config.Config, title string, help string) (string, error) {
	if !ios.IsInTTY {
		data, err := io.ReadAll(ios.In)
		if err != nil {
			return "", fmt.Errorf("failed to read from stdin: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}

	editor, err := GetEditor(cfg)
	if err != nil {
		return "", err
	}

	var body string
	if err := ios.Editor(ctx, &body, title, help, "", editor); err != nil {
		return "", err
	}
	return body, nil
}
