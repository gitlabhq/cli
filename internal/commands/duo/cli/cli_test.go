//go:build !integration

package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/binarymgr"
	"gitlab.com/gitlab-org/cli/internal/binarymgr/binaries"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestNewCmd_Structure(t *testing.T) {
	t.Parallel()

	ios, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
	factory := cmdtest.NewTestFactory(ios)
	cmd := NewCmd(factory)

	assert.True(t, cmd.DisableFlagParsing, "DisableFlagParsing should be enabled for transparent pass-through")
	require.NoError(t, cmd.Args(cmd, []string{"run", "--goal", "x"}), "Args should accept any arguments")
	assert.NotNil(t, cmd.RunE, "RunE should be set")

	// Verify glab-owned flags are registered for documentation
	assert.NotNil(t, cmd.Flags().Lookup("install"), "--install flag should be registered")
	assert.NotNil(t, cmd.Flags().Lookup("update"), "--update flag should be registered")
	yesFlag := cmd.Flags().Lookup("yes")
	assert.NotNil(t, yesFlag, "--yes flag should be registered")
	assert.Equal(t, "y", yesFlag.Shorthand, "--yes should have -y shorthand")
}

func TestRunWithCustomPath_Validation(t *testing.T) {
	if _, err := binarymgr.ManagedBinaryPath(binaries.DuoCLI()); errors.Is(err, binarymgr.ErrUnsupportedPlatform) {
		t.Skipf("skipping on unsupported platform: %v", err)
	}

	t.Run("non-existent path returns clear error", func(t *testing.T) {
		ios, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
		factory := cmdtest.NewTestFactory(ios)

		t.Setenv("GLAB_DUO_CLI_BINARY_PATH", "/nonexistent/path/to/duo")
		runner := newRunner(factory.IO(), factory.Config(), binaries.DuoCLI())
		err := runner.Run(t.Context())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "GLAB_DUO_CLI_BINARY_PATH")
		assert.Contains(t, err.Error(), "duo_cli_binary_path")
		assert.Contains(t, err.Error(), "/nonexistent/path/to/duo")
		assert.Contains(t, err.Error(), "was not found")
	})

	t.Run("directory path returns clear error", func(t *testing.T) {
		ios, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
		factory := cmdtest.NewTestFactory(ios)

		dir := t.TempDir()
		t.Setenv("GLAB_DUO_CLI_BINARY_PATH", dir)
		runner := newRunner(factory.IO(), factory.Config(), binaries.DuoCLI())
		err := runner.Run(t.Context())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "GLAB_DUO_CLI_BINARY_PATH")
		assert.Contains(t, err.Error(), "duo_cli_binary_path")
		assert.Contains(t, err.Error(), "is a directory, not an executable file")
	})

	t.Run("non-executable file returns clear error", func(t *testing.T) {
		ios, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
		factory := cmdtest.NewTestFactory(ios)

		dir := t.TempDir()
		nonExecFile := filepath.Join(dir, "duo")
		require.NoError(t, os.WriteFile(nonExecFile, []byte("#!/bin/sh\n"), 0o644))

		t.Setenv("GLAB_DUO_CLI_BINARY_PATH", nonExecFile)
		runner := newRunner(factory.IO(), factory.Config(), binaries.DuoCLI())
		err := runner.Run(t.Context())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "GLAB_DUO_CLI_BINARY_PATH")
		assert.Contains(t, err.Error(), "duo_cli_binary_path")
		assert.Contains(t, err.Error(), "is not executable")
		assert.Contains(t, err.Error(), "chmod +x")
	})
}

func TestHandleInstall_CustomPath(t *testing.T) {
	if _, err := binarymgr.ManagedBinaryPath(binaries.DuoCLI()); errors.Is(err, binarymgr.ErrUnsupportedPlatform) {
		t.Skipf("skipping on unsupported platform: %v", err)
	}

	t.Run("custom path reports the path and returns no error", func(t *testing.T) {
		ios, _, stderr, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
		factory := cmdtest.NewTestFactory(ios)

		dir := t.TempDir()
		execFile := filepath.Join(dir, "duo")
		require.NoError(t, os.WriteFile(execFile, []byte("#!/bin/sh\n"), 0o755))

		t.Setenv("GLAB_DUO_CLI_BINARY_PATH", execFile)
		runner := newRunner(factory.IO(), factory.Config(), binaries.DuoCLI())
		err := runner.HandleInstall(t.Context())

		require.NoError(t, err)
		assert.Contains(t, stderr.String(), "Using custom GitLab Duo CLI binary:")
		assert.Contains(t, stderr.String(), execFile)
	})
}

func TestRunE_InstallAndUpdateAreMutuallyExclusive(t *testing.T) {
	t.Parallel()

	ios, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
	factory := cmdtest.NewTestFactory(ios)
	cmd := NewCmd(factory)
	cmd.SetArgs([]string{"--install", "--update"})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

func TestWarnIfSnapConfined(t *testing.T) {
	t.Run("warns on stderr when SNAP is set and command is a normal run", func(t *testing.T) {
		t.Setenv("SNAP_NAME", "glab")
		ios, _, stdout, stderr := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))

		warnIfSnapConfined(ios, false, false)

		out := stderr.String()
		assert.Contains(t, out, "snap confinement")
		assert.Contains(t, out, "glab auth credential-helper")
		assert.Contains(t, out, "brew install glab")
		assert.Empty(t, stdout.String(), "warning must not leak onto stdout")
	})

	t.Run("stays silent when SNAP is unset", func(t *testing.T) {
		t.Setenv("SNAP_NAME", "")
		ios, _, stdout, stderr := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))

		warnIfSnapConfined(ios, false, false)

		assert.Empty(t, stderr.String())
		assert.Empty(t, stdout.String())
	})

	t.Run("stays silent under --install even when SNAP is set", func(t *testing.T) {
		t.Setenv("SNAP_NAME", "glab")
		ios, _, stdout, stderr := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))

		warnIfSnapConfined(ios, true, false)

		assert.Empty(t, stderr.String())
		assert.Empty(t, stdout.String())
	})

	t.Run("stays silent under --update even when SNAP is set", func(t *testing.T) {
		t.Setenv("SNAP_NAME", "glab")
		ios, _, stdout, stderr := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))

		warnIfSnapConfined(ios, false, true)

		assert.Empty(t, stderr.String())
		assert.Empty(t, stdout.String())
	})
}

func TestShouldForceUpdateCheck(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected bool
	}{
		{"env var set to true", "true", true},
		{"env var set to false", "false", false},
		{"env var not set", "", false},
		{"env var set to other value", "yes", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GLAB_DUO_CLI_CHECK_UPDATE", tt.envValue)

			ios, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
			factory := cmdtest.NewTestFactory(ios)
			runner := newRunner(factory.IO(), factory.Config(), binaries.DuoCLI())
			assert.Equal(t, tt.expected, runner.ShouldForceUpdateCheck())
		})
	}
}

func TestBinaryStatus(t *testing.T) {
	if _, err := binarymgr.ManagedBinaryPath(binaries.DuoCLI()); errors.Is(err, binarymgr.ErrUnsupportedPlatform) {
		t.Skipf("skipping on unsupported platform: %v", err)
	}

	t.Run("reports installed with the configured version", func(t *testing.T) {
		execFile := filepath.Join(t.TempDir(), "duo")
		require.NoError(t, os.WriteFile(execFile, []byte("#!/bin/sh\n"), 0o755))
		t.Setenv("GLAB_DUO_CLI_BINARY_PATH", execFile)

		cfg := config.NewBlankConfig()
		require.NoError(t, cfg.Set("", "duo_cli_binary_version", "9.5.0"))

		path, version, installed := binaryStatus(cfg)
		assert.Equal(t, execFile, path)
		assert.True(t, installed)
		assert.Equal(t, "9.5.0", version)
	})

	t.Run("falls back to \"unknown version\" when duo_cli_binary_version is unset", func(t *testing.T) {
		execFile := filepath.Join(t.TempDir(), "duo")
		require.NoError(t, os.WriteFile(execFile, []byte("#!/bin/sh\n"), 0o755))
		t.Setenv("GLAB_DUO_CLI_BINARY_PATH", execFile)

		_, version, installed := binaryStatus(config.NewBlankConfig())
		assert.True(t, installed)
		assert.Equal(t, "unknown version", version)
	})

	t.Run("reports not installed for a missing binary", func(t *testing.T) {
		t.Setenv("GLAB_DUO_CLI_BINARY_PATH", filepath.Join(t.TempDir(), "missing-duo"))

		_, _, installed := binaryStatus(config.NewBlankConfig())
		assert.False(t, installed)
	})
}

func TestAppendBinaryStatusFooter_ShowsSetupStepsWhenNotInstalled(t *testing.T) {
	if _, err := binarymgr.ManagedBinaryPath(binaries.DuoCLI()); errors.Is(err, binarymgr.ErrUnsupportedPlatform) {
		t.Skipf("skipping on unsupported platform: %v", err)
	}

	ios, _, stdout, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
	factory := cmdtest.NewTestFactory(ios)

	t.Setenv("GLAB_DUO_CLI_BINARY_PATH", filepath.Join(t.TempDir(), "missing-duo"))

	cmd := NewCmd(factory)
	// The help func delegates the standard part of the output to the root help
	// func, so give the command a root with a no-op help func.
	root := &cobra.Command{Use: "glab"}
	root.SetHelpFunc(func(*cobra.Command, []string) {})
	root.AddCommand(cmd)
	require.NoError(t, cmd.Help())

	assert.Contains(t, stdout.String(), "not installed yet")
	assert.Contains(t, stdout.String(), "glab duo cli --install")
}

func TestNewCmd_UpdateRouting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "update is glab's subcommand", args: []string{"update"}, want: "update"},
		{name: "update after --yes is glab's subcommand", args: []string{"--yes", "update"}, want: "update"},
		{name: "--update stays on the pass-through command", args: []string{"--update"}, want: "cli"},
		{name: "update after another command passes through", args: []string{"run", "--goal", "update"}, want: "cli"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ios, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
			cmd := NewCmd(cmdtest.NewTestFactory(ios))

			found, _, err := cmd.Find(tc.args)
			require.NoError(t, err)
			assert.Equal(t, tc.want, found.Name())
		})
	}
}

func TestAppendBinaryStatusFooter_OmittedFromSubcommandHelp(t *testing.T) {
	t.Setenv("GLAB_CONFIG_DIR", t.TempDir())
	ios, _, stdout, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
	cmd := NewCmd(cmdtest.NewTestFactory(ios))
	root := &cobra.Command{Use: "glab"}
	root.SetHelpFunc(func(*cobra.Command, []string) {})
	root.AddCommand(cmd)
	update, _, err := cmd.Find([]string{"update"})
	require.NoError(t, err)

	require.NoError(t, update.Help())

	assert.NotContains(t, stdout.String(), "GITLAB DUO CLI")
}
