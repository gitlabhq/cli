//go:build !integration

package cmdutils

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

// newDescriptionCmd builds a command with the same flag pair the real commands
// register: a bound --description plus --description-file.
func newDescriptionCmd(description *string) *cobra.Command {
	cmd := &cobra.Command{Use: "test", RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.Flags().StringVarP(description, "description", "d", "", "Description.")
	AddDescriptionFileFlag(cmd, "issue")
	return cmd
}

func TestAddDescriptionFileFlag(t *testing.T) {
	t.Parallel()

	var description string
	cmd := newDescriptionCmd(&description)

	flag := cmd.Flags().Lookup("description-file")
	require.NotNil(t, flag)
	assert.Empty(t, flag.Shorthand, "--description-file must not take a short flag")
	assert.Equal(t, `Read the issue description from a file. Use "-" to read from standard input.`, flag.Usage)
}

func TestAddDescriptionFileFlag_MutuallyExclusiveWithDescription(t *testing.T) {
	t.Parallel()

	var description string
	cmd := newDescriptionCmd(&description)
	cmd.SetArgs([]string{"--description", "inline", "--description-file", "some.md"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := cmd.Execute()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "[description description-file]")
}

func TestResolveDescriptionFile(t *testing.T) {
	t.Parallel()

	const body = "# Heading\n\nA body with \"quotes\", $VARS and `backticks`.\n"

	tests := []struct {
		name        string
		fileContent string
		stdin       string
		useStdin    bool
		want        string
	}{
		{
			name:        "reads a multi-line file verbatim",
			fileContent: body,
			want:        body,
		},
		{
			name:        "reads an empty file",
			fileContent: "",
			want:        "",
		},
		{
			name:     "reads from standard input",
			stdin:    body,
			useStdin: true,
			want:     body,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ios := testIOStreamsWithStdin(tc.stdin)

			var description string
			cmd := newDescriptionCmd(&description)

			path := "-"
			if !tc.useStdin {
				path = filepath.Join(t.TempDir(), "description.md")
				require.NoError(t, os.WriteFile(path, []byte(tc.fileContent), 0o600))
			}
			require.NoError(t, cmd.ParseFlags([]string{"--description-file", path}))

			require.NoError(t, ResolveDescriptionFile(ios, cmd))

			// The file content lands on --description, so commands only read one flag.
			assert.Equal(t, tc.want, description)
			got, err := cmd.Flags().GetString("description")
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)

			// Callers gate on Changed("description"), so it must report the file too.
			assert.True(t, cmd.Flags().Changed("description"))
		})
	}
}

func TestResolveDescriptionFile_NoFlagIsNoOp(t *testing.T) {
	t.Parallel()

	ios := testIOStreamsWithStdin("")

	var description string
	cmd := newDescriptionCmd(&description)
	require.NoError(t, cmd.ParseFlags([]string{"--description", "inline"}))

	require.NoError(t, ResolveDescriptionFile(ios, cmd))

	assert.Equal(t, "inline", description, "an inline description must not be overwritten")
}

func TestResolveDescriptionFile_MissingFile(t *testing.T) {
	t.Parallel()

	ios := testIOStreamsWithStdin("")

	var description string
	cmd := newDescriptionCmd(&description)
	missing := filepath.Join(t.TempDir(), "absent.md")
	require.NoError(t, cmd.ParseFlags([]string{"--description-file", missing}))

	err := ResolveDescriptionFile(ios, cmd)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read description file")
	assert.Empty(t, description)
}

func TestResolveDescriptionFile_RejectsDashOnlyContent(t *testing.T) {
	t.Parallel()

	ios := testIOStreamsWithStdin("")

	var description string
	cmd := newDescriptionCmd(&description)
	path := filepath.Join(t.TempDir(), "dash.md")
	require.NoError(t, os.WriteFile(path, []byte("-"), 0o600))
	require.NoError(t, cmd.ParseFlags([]string{"--description-file", path}))

	err := ResolveDescriptionFile(ios, cmd)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "which --description uses to open an editor")
	assert.Empty(t, description, `a file of just "-" must not reach --description and open an editor`)
}

// failingCloser reads normally but fails to close, so the deferred cleanup error
// has something to join.
type failingCloser struct{ io.Reader }

func (failingCloser) Close() error { return errors.New("close failed") }

func TestResolveDescriptionFile_JoinsStdinCloseError(t *testing.T) {
	t.Parallel()

	ios := iostreams.New(
		iostreams.WithStdin(failingCloser{strings.NewReader("a description")}, false),
		iostreams.WithStdout(&strings.Builder{}, false),
		iostreams.WithStderr(&strings.Builder{}, false),
	)

	var description string
	cmd := newDescriptionCmd(&description)
	require.NoError(t, cmd.ParseFlags([]string{"--description-file", "-"}))

	err := ResolveDescriptionFile(ios, cmd)

	require.Error(t, err, "a failed stdin close must not be swallowed")
	assert.Contains(t, err.Error(), "close failed")
}

// testIOStreamsWithStdin is testIOStreams with readable stdin content, for the
// --description-file "-" cases.
func testIOStreamsWithStdin(stdin string) *iostreams.IOStreams {
	var out, errOut strings.Builder
	return iostreams.New(
		iostreams.WithStdin(io.NopCloser(strings.NewReader(stdin)), false),
		iostreams.WithStdout(&out, false),
		iostreams.WithStderr(&errOut, false),
	)
}
