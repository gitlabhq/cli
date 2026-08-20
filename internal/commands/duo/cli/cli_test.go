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
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestNewCmd_Structure(t *testing.T) {
	t.Parallel()

	ios, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
	factory := cmdtest.NewTestFactory(ios)
	cmd := NewCmd(factory)

	assert.True(t, cmd.DisableFlagParsing, "DisableFlagParsing should be enabled for transparent pass-through")
	assert.Nil(t, cmd.Args, "Args should be nil to accept any arguments")
	assert.NotNil(t, cmd.RunE, "RunE should be set")

	// Verify glab-owned flags are registered for documentation
	assert.NotNil(t, cmd.Flags().Lookup("install"), "--install flag should be registered")
	assert.NotNil(t, cmd.Flags().Lookup("update"), "--update flag should be registered")
	yesFlag := cmd.Flags().Lookup("yes")
	assert.NotNil(t, yesFlag, "--yes flag should be registered")
	assert.Equal(t, "y", yesFlag.Shorthand, "--yes should have -y shorthand")
}

func TestSpec_Wiring(t *testing.T) {
	t.Parallel()

	s := Spec()
	assert.Equal(t, "GitLab Duo CLI", s.DisplayName)
	assert.Equal(t, "46519181", s.ProjectID)
	assert.Equal(t, "duo-cli", s.PackageName)
	assert.Equal(t, "duo_cli", s.ConfigPrefix)
	assert.Equal(t, "GLAB_DUO_CLI", s.EnvVarPrefix)
	assert.Equal(t, duoMaxCompatibleMajorVersion, s.MaxCompatibleMajor)
	assert.ElementsMatch(t, []string{"darwin", "linux", "windows"}, s.SupportedOS)
	assert.Nil(t, s.Extract, "Duo ships raw binaries; no extractor expected")
}

func TestDuoNormalizeArch(t *testing.T) {
	t.Parallel()

	baseline := func() string { return "x64" }
	modern := func() string { return "x64-modern" }

	tests := []struct {
		name        string
		goos        string
		goarch      string
		linuxX64    func() string
		want        string
		expectError bool
	}{
		{name: "amd64 darwin", goos: "darwin", goarch: "amd64", linuxX64: baseline, want: "x64"},
		{name: "amd64 linux baseline (no AVX2)", goos: "linux", goarch: "amd64", linuxX64: baseline, want: "x64"},
		{name: "amd64 linux modern (AVX2)", goos: "linux", goarch: "amd64", linuxX64: modern, want: "x64-modern"},
		{name: "amd64 windows", goos: "windows", goarch: "amd64", linuxX64: baseline, want: "x64-baseline"},
		{name: "arm64 darwin", goos: "darwin", goarch: "arm64", linuxX64: baseline, want: "arm64"},
		{name: "arm64 linux", goos: "linux", goarch: "arm64", linuxX64: baseline, want: "arm64"},
		{name: "arm64 windows", goos: "windows", goarch: "arm64", linuxX64: baseline, want: "arm64"},
		{name: "aarch64 alias", goos: "linux", goarch: "aarch64", linuxX64: baseline, want: "arm64"},
		{name: "unsupported arch", goos: "linux", goarch: "386", linuxX64: baseline, expectError: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := duoNormalizeArchFor(tc.goos, tc.goarch, tc.linuxX64)
			if tc.expectError {
				require.Error(t, err)
				require.ErrorIs(t, err, binarymgr.ErrUnsupportedPlatform)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			}
		})
	}
}

func TestDetectLinuxX64ArchVariant(t *testing.T) {
	t.Parallel()
	// Host CPU is unknown at test time; just assert the detector returns
	// one of the two valid variants.
	got := detectLinuxX64ArchVariant()
	assert.Contains(t, []string{"x64", "x64-modern"}, got)
}

func TestDuoAssetName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "duo-darwin-arm64", duoAssetName("darwin", "arm64"))
	assert.Equal(t, "duo-linux-x64", duoAssetName("linux", "x64"))
	assert.Equal(t, "duo-linux-x64-modern", duoAssetName("linux", "x64-modern"))
	assert.Equal(t, "duo-windows-x64-baseline.exe", duoAssetName("windows", "x64-baseline"))
	assert.Equal(t, "duo-windows-arm64.exe", duoAssetName("windows", "arm64"))
}

func TestDuoInstalledName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "duo", duoInstalledName("darwin"))
	assert.Equal(t, "duo", duoInstalledName("linux"))
	assert.Equal(t, "duo.exe", duoInstalledName("windows"))
}

func TestRunWithCustomPath_Validation(t *testing.T) {
	if _, err := binarymgr.ManagedBinaryPath(Spec()); errors.Is(err, binarymgr.ErrUnsupportedPlatform) {
		t.Skipf("skipping on unsupported platform: %v", err)
	}

	t.Run("non-existent path returns clear error", func(t *testing.T) {
		ios, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
		factory := cmdtest.NewTestFactory(ios)

		t.Setenv("GLAB_DUO_CLI_BINARY_PATH", "/nonexistent/path/to/duo")
		runner := newRunner(factory.IO(), factory.Config(), Spec())
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
		runner := newRunner(factory.IO(), factory.Config(), Spec())
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
		runner := newRunner(factory.IO(), factory.Config(), Spec())
		err := runner.Run(t.Context())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "GLAB_DUO_CLI_BINARY_PATH")
		assert.Contains(t, err.Error(), "duo_cli_binary_path")
		assert.Contains(t, err.Error(), "is not executable")
		assert.Contains(t, err.Error(), "chmod +x")
	})
}

func TestHandleInstall_CustomPath(t *testing.T) {
	if _, err := binarymgr.ManagedBinaryPath(Spec()); errors.Is(err, binarymgr.ErrUnsupportedPlatform) {
		t.Skipf("skipping on unsupported platform: %v", err)
	}

	t.Run("custom path reports the path and returns no error", func(t *testing.T) {
		ios, _, stderr, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
		factory := cmdtest.NewTestFactory(ios)

		dir := t.TempDir()
		execFile := filepath.Join(dir, "duo")
		require.NoError(t, os.WriteFile(execFile, []byte("#!/bin/sh\n"), 0o755))

		t.Setenv("GLAB_DUO_CLI_BINARY_PATH", execFile)
		runner := newRunner(factory.IO(), factory.Config(), Spec())
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
			runner := newRunner(factory.IO(), factory.Config(), Spec())
			assert.Equal(t, tt.expected, runner.ShouldForceUpdateCheck())
		})
	}
}

func TestBinaryStatus(t *testing.T) {
	if _, err := binarymgr.ManagedBinaryPath(Spec()); errors.Is(err, binarymgr.ErrUnsupportedPlatform) {
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
	if _, err := binarymgr.ManagedBinaryPath(Spec()); errors.Is(err, binarymgr.ErrUnsupportedPlatform) {
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
