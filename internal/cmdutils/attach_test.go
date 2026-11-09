package cmdutils

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

func attachTestCmd(t *testing.T) *cobra.Command {
	t.Helper()

	cmd := &cobra.Command{Use: "test", RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.Flags().String(descriptionFlag, "", "")
	AddDescriptionFileFlag(cmd, "issue")

	var attach []string
	AddAttachFlag(cmd, &attach, "description")

	return cmd
}

func attachTestIOStreams(stdin string) *iostreams.IOStreams {
	return iostreams.New(
		iostreams.WithStdin(io.NopCloser(bytes.NewBufferString(stdin)), false),
		iostreams.WithStdout(io.Discard, false),
		iostreams.WithStderr(io.Discard, false),
	)
}

func TestAddAttachFlag_MarksTheFlagExperimental(t *testing.T) {
	t.Parallel()

	flag := attachTestCmd(t).Flags().Lookup(attachFlag)
	require.NotNil(t, flag)
	assert.Contains(t, flag.Usage, "(EXPERIMENTAL)")
	// stringArray, not stringSlice: attachment paths may contain commas.
	assert.Equal(t, "stringArray", flag.Value.Type())
}

func TestResolveDescriptionFile_RejectsSharingStdinWithAnAttachment(t *testing.T) {
	t.Parallel()

	cmd := attachTestCmd(t)
	require.NoError(t, cmd.ParseFlags([]string{"--description-file", "-", "--attach", "-"}))

	err := ResolveDescriptionFile(attachTestIOStreams("body text"), cmd)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot both read from")
}

func TestResolveDescriptionFile_AllowsAnAttachmentFromStdinWithAFileDescription(t *testing.T) {
	t.Parallel()

	cmd := attachTestCmd(t)
	path := t.TempDir() + "/body.md"
	require.NoError(t, os.WriteFile(path, []byte("from a file"), 0o600))
	require.NoError(t, cmd.ParseFlags([]string{"--description-file", path, "--attach", "-"}))

	require.NoError(t, ResolveDescriptionFile(attachTestIOStreams(""), cmd))

	description, err := cmd.Flags().GetString(descriptionFlag)
	require.NoError(t, err)
	assert.Equal(t, "from a file", description)
}

func TestResolveDescriptionFile_AllowsStdinDescriptionWithAFileAttachment(t *testing.T) {
	t.Parallel()

	cmd := attachTestCmd(t)
	require.NoError(t, cmd.ParseFlags([]string{"--description-file", "-", "--attach", "./screenshot.png"}))

	require.NoError(t, ResolveDescriptionFile(attachTestIOStreams("body from stdin"), cmd))

	description, err := cmd.Flags().GetString(descriptionFlag)
	require.NoError(t, err)
	assert.Equal(t, "body from stdin", description)
}

func TestAppendAttachments_NoPathsLeavesTheBodyAlone(t *testing.T) {
	t.Parallel()

	body, err := AppendAttachments(t.Context(), attachTestIOStreams(""), nil, "OWNER/REPO", "unchanged", nil)
	require.NoError(t, err)
	assert.Equal(t, "unchanged", body)
}

func TestSkipNoteBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		attach []string
		isTTY  bool
		want   bool
	}{
		{name: "no attachment reads the body", attach: nil, isTTY: true, want: false},
		{name: "file attachment with piped stdin reads the body", attach: []string{"./a.png"}, isTTY: false, want: false},
		{name: "stdin attachment claims stdin", attach: []string{"./a.png", "-"}, isTTY: false, want: true},
		{name: "stdin attachment on a terminal", attach: []string{"-"}, isTTY: true, want: true},
		{name: "file attachment on a terminal does not prompt", attach: []string{"./a.png"}, isTTY: true, want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ios := iostreams.New(iostreams.WithStdin(io.NopCloser(bytes.NewBufferString("")), tc.isTTY))
			assert.Equal(t, tc.want, SkipNoteBody(ios, tc.attach))
		})
	}
}
