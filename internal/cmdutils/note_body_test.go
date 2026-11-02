//go:build !integration

package cmdutils

import (
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"git.sr.ht/~timofurrer/ugh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

func TestNoteBodyFromStdinOrEditor_ReadsStdin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		stdin string
		want  string
	}{
		{
			name:  "trims surrounding whitespace",
			stdin: "\n  hello\n\n",
			want:  "hello",
		},
		{
			name:  "keeps inner blank lines and indentation",
			stdin: "line one\n\n  line two\n",
			want:  "line one\n\n  line two",
		},
		{
			name:  "empty stdin returns an empty body",
			stdin: "",
			want:  "",
		},
		{
			name:  "whitespace-only stdin returns an empty body",
			stdin: "  \n\t\n",
			want:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ios := testIOStreamsWithStdin(tc.stdin)
			cfg := func() config.Config { return config.NewFromString("editor: vi") }

			note, err := NoteBodyFromStdinOrEditor(t.Context(), ios, cfg, "title", "help text")
			require.NoError(t, err)
			assert.Equal(t, tc.want, note)
		})
	}
}

func TestNoteBodyFromStdinOrEditor_WrapsReadError(t *testing.T) {
	t.Parallel()

	readErr := errors.New("some read error")

	ios := iostreams.New(
		iostreams.WithStdin(io.NopCloser(iotest.ErrReader(readErr)), false),
		iostreams.WithStdout(&strings.Builder{}, false),
		iostreams.WithStderr(&strings.Builder{}, false),
	)
	cfg := func() config.Config { return config.NewFromString("editor: vi") }

	body, err := NoteBodyFromStdinOrEditor(t.Context(), ios, cfg, "title", "help text")

	require.Error(t, err)
	require.ErrorIs(t, err, readErr)
	assert.Contains(t, err.Error(), "failed to read from stdin")
	assert.Empty(t, body)
}

func TestNoteBodyFromStdinOrEditor_PromptsWithTitle(t *testing.T) {
	// Not parallel: huh text inputs share global state.

	u := ugh.New(t, ugh.WithTimeout(2*time.Second))
	u.Expect(ugh.Input("Probe title:")).Do(ugh.Type("typed body"))

	ios, cancel := testIOStreamsWithConsole(t, u)
	defer cancel()
	cfg := func() config.Config { return config.NewFromString("editor: vi") }

	body, err := NoteBodyFromStdinOrEditor(t.Context(), ios, cfg, "Probe title:", "help text")

	require.NoError(t, err)
	assert.Equal(t, "typed body", body)
}
