//go:build !integration

package binarymgr

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func runnerFor(t *testing.T) (*Runner, config.Config) {
	t.Helper()
	t.Setenv("GLAB_CONFIG_DIR", t.TempDir())
	ios, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
	cfg := config.NewBlankConfig()
	spec := testSpec()
	return &Runner{
		IO:      ios,
		Cfg:     cfg,
		Spec:    spec,
		Manager: NewManager(ios, spec),
	}, cfg
}

// These tests use t.Setenv (via runnerFor) to point ConfigFile() at a temp dir and so cannot be parallel

func TestRunner_saveAutoDownloadPreference(t *testing.T) {
	t.Run("empty preference is a no-op", func(t *testing.T) {
		r, cfg := runnerFor(t)
		r.saveAutoDownloadPreference("")

		got, _ := cfg.Get("", r.Spec.configKey("auto_download"))
		assert.Empty(t, got, "no preference should be persisted for empty input")
	})

	t.Run("opt-in is persisted", func(t *testing.T) {
		r, cfg := runnerFor(t)
		r.saveAutoDownloadPreference("true")

		got, _ := cfg.Get("", r.Spec.configKey("auto_download"))
		assert.Equal(t, "true", got)
	})
}

func TestRunner_versionAfterFloor(t *testing.T) {
	managed := "/managed/bin/test"
	newRunner := func(t *testing.T, min string) *Runner {
		t.Helper()
		ios, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
		spec := testSpec()
		spec.MinVersion = min
		return &Runner{IO: ios, Spec: spec}
	}

	t.Run("below floor is blanked to force download", func(t *testing.T) {
		r := newRunner(t, "0.101.1")
		assert.Empty(t, r.versionAfterFloor("0.101.0", managed, managed))
	})

	t.Run("at or above floor is preserved", func(t *testing.T) {
		r := newRunner(t, "0.101.1")
		assert.Equal(t, "0.101.1", r.versionAfterFloor("0.101.1", managed, managed))
		assert.Equal(t, "0.102.0", r.versionAfterFloor("0.102.0", managed, managed))
	})

	t.Run("custom binary path is never forced", func(t *testing.T) {
		r := newRunner(t, "0.101.1")
		assert.Equal(t, "0.101.0", r.versionAfterFloor("0.101.0", "/custom/orbit", managed))
	})

	t.Run("no floor is a no-op", func(t *testing.T) {
		r := newRunner(t, "")
		assert.Equal(t, "0.0.1", r.versionAfterFloor("0.0.1", managed, managed))
	})
}

func TestRunner_saveLastUpdateCheck(t *testing.T) {
	r, cfg := runnerFor(t)
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	r.saveLastUpdateCheck(now)

	got, _ := cfg.Get("", r.Spec.configKey("last_update_check"))
	require.NotEmpty(t, got)

	parsed, err := time.Parse(time.RFC3339, got)
	require.NoError(t, err)
	assert.True(t, parsed.Equal(now), "expected %s, got %s", now, parsed)
}

type failingConfig struct {
	config.Config
	failKey string
}

func (c failingConfig) Get(hostname, key string) (string, error) {
	if key == c.failKey {
		return "", errors.New("keyring locked")
	}
	return c.Config.Get(hostname, key)
}

func TestInstalledBinary(t *testing.T) {
	writeExecutable := func(t *testing.T, path string) {
		t.Helper()
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755))
	}
	managedSetup := func(t *testing.T, version string) (Spec, config.Config, string) {
		t.Helper()
		t.Setenv("GLAB_CONFIG_DIR", t.TempDir())
		spec := testSpec()
		spec.MinVersion = "0.103.0"
		managed, err := ManagedBinaryPath(spec)
		require.NoError(t, err)
		cfg := config.NewBlankConfig()
		if version != "" {
			require.NoError(t, cfg.Set("", spec.configKey("binary_version"), version))
		}
		return spec, cfg, managed
	}

	customSetup := func(t *testing.T, name string) (config.Config, string) {
		t.Helper()
		custom := filepath.Join(t.TempDir(), name)
		cfg := config.NewBlankConfig()
		require.NoError(t, cfg.Set("", testSpec().configKey("binary_path"), custom))
		return cfg, custom
	}

	t.Run("custom path that passes validation is installed", func(t *testing.T) {
		cfg, custom := customSetup(t, "custom")
		writeExecutable(t, custom)

		status, err := InstalledBinary(cfg, testSpec())
		require.NoError(t, err)
		assert.Equal(t, InstallStatus{Path: custom, Version: "unknown version", Installed: true}, status)
	})

	t.Run("custom path that is missing is not installed", func(t *testing.T) {
		cfg, _ := customSetup(t, "missing")

		status, err := InstalledBinary(cfg, testSpec())
		require.NoError(t, err)
		assert.False(t, status.Installed)
	})

	t.Run("managed binary at or above the floor is installed", func(t *testing.T) {
		spec, cfg, managed := managedSetup(t, "0.103.0")
		writeExecutable(t, managed)

		status, err := InstalledBinary(cfg, spec)
		require.NoError(t, err)
		assert.Equal(t, InstallStatus{Path: managed, Version: "0.103.0", Installed: true}, status)
	})

	t.Run("managed binary below the floor is not installed because Run would download", func(t *testing.T) {
		spec, cfg, managed := managedSetup(t, "0.100.0")
		writeExecutable(t, managed)

		status, err := InstalledBinary(cfg, spec)
		require.NoError(t, err)
		assert.Equal(t, InstallStatus{Path: managed, Version: "0.100.0", Installed: false}, status)
	})

	t.Run("managed binary without a recorded version is not installed because Run would download", func(t *testing.T) {
		spec, cfg, managed := managedSetup(t, "")
		writeExecutable(t, managed)

		status, err := InstalledBinary(cfg, spec)
		require.NoError(t, err)
		assert.False(t, status.Installed)
	})

	t.Run("config read error is returned with a best-effort status", func(t *testing.T) {
		blank, custom := customSetup(t, "custom")
		writeExecutable(t, custom)
		spec := testSpec()
		cfg := failingConfig{Config: blank, failKey: spec.configKey("binary_version")}

		status, err := InstalledBinary(cfg, spec)
		require.ErrorContains(t, err, "reading test_cli_binary_version: keyring locked")
		assert.Equal(t, InstallStatus{Path: custom, Version: "unknown version", Installed: true}, status)
	})
}
