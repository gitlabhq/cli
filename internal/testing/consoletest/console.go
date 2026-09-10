// Package consoletest wires an *iostreams.IOStreams to a real terminal
// emulator (via ugh) so tests can drive interactive prompts with actual
// keystrokes instead of pre-canned values. It is kept dependency-light
// (no internal/api) so packages that internal/api itself depends on, such
// as internal/oauth2, can use it in tests without an import cycle; cmdtest
// delegates to it for the same helper.
package consoletest

import (
	"context"
	"io"
	"testing"

	tea "charm.land/bubbletea/v2"
	"git.sr.ht/~timofurrer/ugh"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

const (
	Width  = 120
	Height = 40
)

// IOStreamsWithConsole wires an IOStreams to a ugh console driving a real
// huh form/prompt over an emulated terminal.
func IOStreamsWithConsole(t *testing.T, console *ugh.Console) (*iostreams.IOStreams, context.CancelFunc) {
	t.Helper()

	appInR, appInW := io.Pipe()
	appOutR, appOutW := io.Pipe()

	ctx, cancel := context.WithCancel(t.Context())
	wait := console.Start(ctx, appOutR, appInW)

	ios := iostreams.New(
		iostreams.WithStdin(appInR, true),
		iostreams.WithStdout(appOutW, true),
		iostreams.WithStderr(io.Discard, true),
		iostreams.WithProgramOptions(tea.WithWindowSize(Width, Height)),
	)

	cleanup := func() {
		appInW.Close()
		appOutW.Close()
		appOutR.Close()
		cancel()
		wait()
	}

	return ios, cleanup
}
