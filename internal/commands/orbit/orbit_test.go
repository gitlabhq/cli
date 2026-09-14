//go:build !integration

package orbit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/api"
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

	assert.Equal(t, "orbit", cmd.Name())
	assert.True(t, cmd.DisableFlagParsing)
	assert.Empty(t, cmd.Annotations["mcp:safe"])
	assert.NotNil(t, cmd.Flags().Lookup("install"))
	assert.NotNil(t, cmd.Flags().Lookup("update"))
	yesFlag := cmd.Flags().Lookup("yes")
	require.NotNil(t, yesFlag)
	assert.Equal(t, "y", yesFlag.Shorthand)
}

func TestSplitGlabFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		args          []string
		wantYes       bool
		wantInstall   bool
		wantUpdate    bool
		wantHelpOnly  bool
		wantForwarded []string
	}{
		{
			name:          "verbatim forwarding of a query command",
			args:          []string{"query", "-"},
			wantForwarded: []string{"query", "-"},
		},
		{
			name:          "verbatim forwarding of a sql command",
			args:          []string{"sql", "SELECT 1"},
			wantForwarded: []string{"sql", "SELECT 1"},
		},
		{
			name:          "verbatim forwarding of top-level command",
			args:          []string{"version"},
			wantForwarded: []string{"version"},
		},
		{
			name:          "yes long flag is consumed and dropped",
			args:          []string{"--yes", "index", "."},
			wantYes:       true,
			wantForwarded: []string{"index", "."},
		},
		{
			name:          "yes short flag is consumed and dropped",
			args:          []string{"-y", "query", "-"},
			wantYes:       true,
			wantForwarded: []string{"query", "-"},
		},
		{
			name:        "install flag is consumed and dropped",
			args:        []string{"--install", "--yes"},
			wantYes:     true,
			wantInstall: true,
		},
		{
			name:       "update flag is consumed and dropped",
			args:       []string{"--update"},
			wantUpdate: true,
		},
		{
			name:         "no args forwards nothing so the binary prints its own usage",
			wantHelpOnly: true,
		},
		{
			name:          "lone help is forwarded to the binary",
			args:          []string{"--help"},
			wantHelpOnly:  true,
			wantForwarded: []string{"--help"},
		},
		{
			name:          "lone short help is forwarded to the binary",
			args:          []string{"-h"},
			wantHelpOnly:  true,
			wantForwarded: []string{"-h"},
		},
		{
			name:          "help after a glab flag is forwarded to the binary",
			args:          []string{"--yes", "--help"},
			wantYes:       true,
			wantHelpOnly:  true,
			wantForwarded: []string{"--help"},
		},
		{
			name:          "help with a forwarded command is forwarded to the binary",
			args:          []string{"query", "--help"},
			wantForwarded: []string{"query", "--help"},
		},
		{
			name:          "glab flag after the subcommand is forwarded to the binary",
			args:          []string{"setup", "claude", "--yes"},
			wantForwarded: []string{"setup", "claude", "--yes"},
		},
		{
			name:          "double dash forwards the rest verbatim",
			args:          []string{"--", "--yes", "status"},
			wantForwarded: []string{"--yes", "status"},
		},
		{
			name:          "help before a command is forwarded to the binary",
			args:          []string{"--help", "status"},
			wantHelpOnly:  true,
			wantForwarded: []string{"--help", "status"},
		},
		{
			name:          "unknown leading flag forwards everything to the binary",
			args:          []string{"--log-level", "debug", "status"},
			wantForwarded: []string{"--log-level", "debug", "status"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			flags := splitGlabFlags(tt.args)
			assert.Equal(t, tt.wantYes, flags.yes)
			assert.Equal(t, tt.wantInstall, flags.install)
			assert.Equal(t, tt.wantUpdate, flags.update)
			assert.Equal(t, tt.wantHelpOnly, flags.helpOnly())
			assert.Equal(t, tt.wantForwarded, flags.forwarded)
		})
	}
}

func TestValidate_InstallUpdateMutuallyExclusive(t *testing.T) {
	t.Parallel()
	opts := &options{flags: splitGlabFlags([]string{"--install", "--update"})}

	err := opts.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

func TestValidate_InstallWithCommandErrors(t *testing.T) {
	t.Parallel()
	opts := &options{flags: splitGlabFlags([]string{"--update", "status"})}

	err := opts.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be combined with a command")
}

func TestNewCmd_HelpShowsGlabTextUntilBinaryIsInstalled(t *testing.T) {
	t.Setenv("GLAB_ORBIT_LOCAL_BINARY_PATH", filepath.Join(t.TempDir(), "missing-orbit"))
	exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
		cmd := NewCmd(f)
		cmd.SetOut(f.IO().StdOut)
		return cmd
	}, false, cmdtest.WithConfig(config.NewBlankConfig()))

	for _, args := range []string{"", "--help", "-h status"} {
		out, err := exec(args)
		require.NoError(t, err, args)
		assert.Contains(t, out.String(), "Run the Orbit CLI", args)
	}
}

func TestRun_HelpExecsTheInstalledBinaryWithoutRunLifecycle(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "orbit")
	require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755))
	t.Setenv("GLAB_ORBIT_LOCAL_BINARY_PATH", binary)
	t.Setenv("GITLAB_TOKEN", "glpat-remote")
	client, err := api.NewClientFromConfig("gitlab.com", config.NewBlankConfig(), false, "test-agent")
	require.NoError(t, err)
	ios, _, stdout, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))
	f := cmdtest.NewTestFactory(ios, cmdtest.WithConfig(config.NewBlankConfig()), cmdtest.WithApiClient(client))

	for _, args := range [][]string{{}, {"--help"}, {"-h", "status"}, {"--yes", "--help"}} {
		var forwarded []string
		opts := &options{
			io:      ios,
			cfg:     f.Config(),
			factory: f,
			flags:   splitGlabFlags(args),
			help:    func() error { return errors.New("glab help must not render") },
			execute: func(_ context.Context, _ *iostreams.IOStreams, path string, execArgs, env []string) error {
				assert.Equal(t, binary, path, "%v", args)
				assert.Contains(t, env, "ORBIT_AUTH_HEADER_VALUE=glpat-remote", "%v", args)
				forwarded = execArgs
				return nil
			},
		}

		require.NoError(t, opts.run(t.Context()), "%v", args)
		assert.Equal(t, opts.flags.forwarded, forwarded, "%v", args)
	}
	assert.Empty(t, stdout.String())
}

func TestRun_HelpFallsBackToGlabTextWhenBinaryIsMissing(t *testing.T) {
	t.Setenv("GLAB_ORBIT_LOCAL_BINARY_PATH", filepath.Join(t.TempDir(), "missing-orbit"))
	ios, _, _, _ := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(ios, cmdtest.WithConfig(config.NewBlankConfig()))

	helpRendered := false
	opts := &options{
		io:      ios,
		cfg:     f.Config(),
		factory: f,
		flags:   splitGlabFlags([]string{"--help"}),
		help:    func() error { helpRendered = true; return nil },
		execute: func(context.Context, *iostreams.IOStreams, string, []string, []string) error {
			return errors.New("must not exec a missing binary")
		},
	}

	require.NoError(t, opts.run(t.Context()))
	assert.True(t, helpRendered)
}

func TestOrbitCredentialEnv_InjectsResolvedCredential(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "glpat-remote")

	client, err := api.NewClientFromConfig("gitlab.com", config.NewBlankConfig(), false, "test-agent")
	require.NoError(t, err)

	f := cmdtest.NewTestFactory(nil, cmdtest.WithApiClient(client))

	env := orbitCredentialEnv(t.Context(), f)
	assert.Contains(t, env, "ORBIT_API_BASE_URL=https://gitlab.com")
	assert.Contains(t, env, "ORBIT_AUTH_HEADER_NAME=Private-Token")
	assert.Contains(t, env, "ORBIT_AUTH_HEADER_VALUE=glpat-remote")
}

func TestOrbitCredentialEnv_SkipsWhenUnauthenticated(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "")

	client, err := api.NewClientFromConfig("gitlab.com", config.NewBlankConfig(), false, "test-agent")
	require.NoError(t, err)

	ios, _, _, stderr := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(ios, cmdtest.WithApiClient(client))

	assert.Nil(t, orbitCredentialEnv(t.Context(), f))
	assert.Empty(t, stderr.String())
}
