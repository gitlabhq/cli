//go:build !integration

package duo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestRunE_BareShowsHelpWithoutError(t *testing.T) {
	t.Parallel()

	ios, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
	factory := cmdtest.NewTestFactory(ios)
	cmd := NewCmd(factory)
	cmd.SetArgs([]string{})

	require.NoError(t, cmd.Execute())
}

func TestRunE_HelpFlagShowsHelpWithoutError(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"--help", "-h"} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()
			ios, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
			factory := cmdtest.NewTestFactory(ios)
			cmd := NewCmd(factory)
			cmd.SetArgs([]string{flag})

			require.NoError(t, cmd.Execute())
		})
	}
}

func TestRunE_UnknownSubcommandSuggestsGlabDuoCli(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		args        []string
		wantSuggest string
	}{
		{name: "bare word", args: []string{"run"}, wantSuggest: `"glab duo cli run"`},
		{name: "word with flag", args: []string{"run", "--goal", "foo"}, wantSuggest: `"glab duo cli run --goal foo"`},
		{name: "multi-word", args: []string{"plugin", "marketplace", "add"}, wantSuggest: `"glab duo cli plugin marketplace add"`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ios, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
			factory := cmdtest.NewTestFactory(ios)
			cmd := NewCmd(factory)
			cmd.SetArgs(tc.args)

			err := cmd.Execute()
			require.Error(t, err)
			assert.Contains(t, err.Error(), `unknown command`)
			assert.Contains(t, err.Error(), tc.wantSuggest)
			assert.Contains(t, err.Error(), `"glab duo cli --help"`)
		})
	}
}

func TestRunE_KnownSubcommandsStillDispatch(t *testing.T) {
	t.Parallel()

	ios, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
	factory := cmdtest.NewTestFactory(ios)
	cmd := NewCmd(factory)

	cliSub, _, err := cmd.Find([]string{"cli"})
	require.NoError(t, err)
	assert.Equal(t, "cli", cliSub.Name())

	askSub, _, err := cmd.Find([]string{"ask"})
	require.NoError(t, err)
	assert.Equal(t, "ask", askSub.Name())
}
