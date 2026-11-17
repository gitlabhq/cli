//go:build !integration

package minder

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/binarymgr"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestNewCmd_PassthroughStructure(t *testing.T) {
	t.Parallel()
	ios, _, _, _ := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(ios)

	cmd := NewCmd(f)

	assert.Equal(t, "minder", cmd.Name())
	assert.True(t, cmd.DisableFlagParsing)
	assert.Equal(t, "true", cmd.Annotations["mcp:exclude"])
	assert.NotNil(t, cmd.Flags().Lookup("install"))
	assert.NotNil(t, cmd.Flags().Lookup("update"))
	yesFlag := cmd.Flags().Lookup("yes")
	require.NotNil(t, yesFlag)
	assert.Equal(t, "y", yesFlag.Shorthand)

	// Governance subcommands must be attached so they take priority over the
	// pass-through; everything else forwards to agent-client.
	subs := map[string]struct{}{}
	for _, c := range cmd.Commands() {
		subs[c.Name()] = struct{}{}
	}
	assert.Contains(t, subs, "setup", "setup subcommand must be registered")
	assert.Contains(t, subs, "doctor", "doctor subcommand must be registered")
	assert.Contains(t, subs, "audit", "audit subcommand must be registered")
	assert.Contains(t, subs, "update", "update subcommand must be registered")
}

func TestValidate_InstallUpdateMutuallyExclusive(t *testing.T) {
	t.Parallel()
	opts := &options{flags: binarymgr.SplitFlags([]string{"--install", "--update"})}

	err := opts.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

func TestValidate_InstallWithCommandErrors(t *testing.T) {
	t.Parallel()
	opts := &options{flags: binarymgr.SplitFlags([]string{"--update", "install"})}

	err := opts.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be combined with a command")
}

// The downloaded binary's real feature tree lives under `agent-client agent`,
// so exec must prepend the literal "agent" subcommand before pass-through args.
func TestExec_PrependsAgentSubcommand(t *testing.T) {
	t.Parallel()
	ios, _, _, _ := cmdtest.TestIOStreams()

	var gotArgs []string
	opts := &options{
		io: ios,
		execute: func(_ context.Context, _ *iostreams.IOStreams, _ string, args []string) error {
			gotArgs = args
			return nil
		},
		flags: binarymgr.SplitFlags([]string{"install"}),
	}

	require.NoError(t, opts.exec(t.Context(), "/tmp/agent-client", opts.flags.Forwarded))
	assert.Equal(t, []string{"agent", "install"}, gotArgs)
}

func TestNewCmd_HelpShowsGlabTextUntilBinaryIsInstalled(t *testing.T) {
	t.Setenv("GLAB_MINDER_CLI_BINARY_PATH", filepath.Join(t.TempDir(), "missing-agent"))
	exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
		cmd := NewCmd(f)
		cmd.SetOut(f.IO().StdOut)
		return cmd
	}, false, cmdtest.WithConfig(config.NewBlankConfig()))

	for _, args := range []string{"", "--help", "-h status"} {
		out, err := exec(args)
		require.NoError(t, err, args)
		assert.Contains(t, out.String(), "GitLab Endpoint Agent Platform", args)
	}
}

func TestRunHelp_ExecsInstalledBinaryWithAgentSubcommand(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "agent-client")
	require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755))
	t.Setenv("GLAB_MINDER_CLI_BINARY_PATH", binary)
	ios, _, _, _ := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(ios, cmdtest.WithConfig(config.NewBlankConfig()))

	var gotPath string
	var gotArgs []string
	opts := &options{
		io:    ios,
		cfg:   f.Config(),
		flags: binarymgr.SplitFlags([]string{"--help"}),
		help:  func() error { return errors.New("glab help must not render") },
		execute: func(_ context.Context, _ *iostreams.IOStreams, path string, args []string) error {
			gotPath = path
			gotArgs = args
			return nil
		},
	}

	require.NoError(t, opts.runHelp(t.Context()))
	assert.Equal(t, binary, gotPath)
	assert.Equal(t, []string{"agent", "--help"}, gotArgs)
}

func TestRunHelp_FallsBackToGlabTextWhenBinaryIsMissing(t *testing.T) {
	t.Setenv("GLAB_MINDER_CLI_BINARY_PATH", filepath.Join(t.TempDir(), "missing-agent"))
	ios, _, _, _ := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(ios, cmdtest.WithConfig(config.NewBlankConfig()))

	helpRendered := false
	opts := &options{
		io:    ios,
		cfg:   f.Config(),
		flags: binarymgr.SplitFlags([]string{"--help"}),
		help:  func() error { helpRendered = true; return nil },
		execute: func(context.Context, *iostreams.IOStreams, string, []string) error {
			return errors.New("must not exec a missing binary")
		},
	}

	require.NoError(t, opts.runHelp(t.Context()))
	assert.True(t, helpRendered)
}
