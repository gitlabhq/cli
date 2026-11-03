//go:build !integration

package delete

import (
	"bytes"
	"io"
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/google/shlex"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestAliasDelete(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	tests := []struct {
		name       string
		config     string
		cli        string
		isTTY      bool
		wantStdout string
		wantStderr string
		wantErr    string
	}{
		{
			name:       "no aliases",
			config:     "",
			cli:        "co",
			isTTY:      true,
			wantStdout: "",
			wantStderr: "",
			wantErr:    `no such alias "co"`,
		},
		{
			name: "delete one",
			config: heredoc.Doc(`
				aliases:
				  il: issue list
				  co: mr checkout
			`),
			cli:        "co",
			isTTY:      true,
			wantStdout: "",
			wantStderr: "✓ Deleted alias 'co'; was 'mr checkout'.\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.NewFromString(tt.config)

			ios, _, stdout, stderr := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))

			factory := cmdtest.NewTestFactory(ios,
				cmdtest.WithConfig(cfg),
			)

			cmd := NewCmdDelete(factory)

			argv, err := shlex.Split(tt.cli)
			require.NoError(t, err)
			cmd.SetArgs(argv)

			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)

			_, err = cmd.ExecuteC()
			if tt.wantErr != "" {
				if assert.Error(t, err) {
					assert.Equal(t, tt.wantErr, err.Error())
				}
				return
			}
			require.NoError(t, err)

			assert.Equal(t, tt.wantStdout, stdout.String())
			assert.Equal(t, tt.wantStderr, stderr.String())
		})
	}
}

func TestAliasDeleteCompletion(t *testing.T) {
	t.Parallel()

	ios, _, _, _ := cmdtest.TestIOStreams()
	cfg := config.NewFromString("aliases:\n  co: mr checkout\n  il: issue list\n")
	cmd := NewCmdDelete(cmdtest.NewTestFactory(ios, cmdtest.WithConfig(cfg)))

	got, dir := cmd.ValidArgsFunction(cmd, nil, "")
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, dir)
	assert.Equal(t, []string{cobra.CompletionWithDesc("co", "mr checkout"), cobra.CompletionWithDesc("il", "issue list")}, got)

	got, dir = cmd.ValidArgsFunction(cmd, []string{"co"}, "")
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, dir)
	assert.Empty(t, got)
}
