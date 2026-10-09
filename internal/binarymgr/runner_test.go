//go:build !integration

package binarymgr

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

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
		assert.Equal(t, InstallStatus{Path: custom, Installed: true}, status)
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
		assert.Equal(t, InstallStatus{Path: custom, Installed: true}, status)
	})
}

func TestRunner_ReportUpdate(t *testing.T) {
	installManaged := func(t *testing.T, cfg config.Config, spec Spec, version string) {
		t.Helper()
		managed, err := ManagedBinaryPath(spec)
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(managed), 0o755))
		require.NoError(t, os.WriteFile(managed, []byte("#!/bin/sh\n"), 0o755))
		require.NoError(t, cfg.Set("", spec.configKey("binary_version"), version))
	}
	runnerWithRegistry := func(t *testing.T, mock func(*gitlabtesting.TestClient)) (*Runner, config.Config, *bytes.Buffer, *bytes.Buffer) {
		t.Helper()
		t.Setenv("GLAB_CONFIG_DIR", t.TempDir())
		ios, _, stdout, stderr := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
		testClient := gitlabtesting.NewTestClient(t, gitlab.WithBaseURL("https://gitlab.com"))
		mock(testClient)
		cfg := config.NewBlankConfig()
		spec := testSpec()
		return &Runner{IO: ios, Cfg: cfg, Spec: spec, Manager: &Manager{io: ios, spec: spec, client: testClient.Client}}, cfg, stdout, stderr
	}
	runnerWithLatest := func(t *testing.T, latest ...string) (*Runner, config.Config, *bytes.Buffer, *bytes.Buffer) {
		t.Helper()
		return runnerWithRegistry(t, func(tc *gitlabtesting.TestClient) {
			for _, v := range latest {
				tc.MockPackages.EXPECT().
					ListProjectPackages(testSpec().ProjectID, gomock.Any(), gomock.Any(), gomock.Any()).
					Return([]*gitlab.Package{{ID: 1, Version: v}}, noMorePages(), nil)
			}
		})
	}
	runnerWithRegistryError := func(t *testing.T, err error) (*Runner, config.Config, *bytes.Buffer, *bytes.Buffer) {
		t.Helper()
		return runnerWithRegistry(t, func(tc *gitlabtesting.TestClient) {
			tc.MockPackages.EXPECT().
				ListProjectPackages(testSpec().ProjectID, gomock.Any(), gomock.Any(), gomock.Any()).
				Return(nil, nil, err)
		})
	}

	t.Run("installed binary with an update prints the update command", func(t *testing.T) {
		r, cfg, stdout, stderr := runnerWithLatest(t, "8.1.0")
		installManaged(t, cfg, r.Spec, "8.0.0")

		require.NoError(t, r.ReportUpdate(t.Context()))

		assert.Contains(t, stderr.String(), "New Test CLI version available: 8.0.0 → 8.1.0")
		assert.Contains(t, stderr.String(), "Run 'glab test cli update' to update to the latest version")
		assert.Empty(t, stdout.String())
	})

	t.Run("installed binary checks even within the 24-hour throttle", func(t *testing.T) {
		r, cfg, _, stderr := runnerWithLatest(t, "8.1.0")
		installManaged(t, cfg, r.Spec, "8.0.0")
		require.NoError(t, cfg.Set("", r.Spec.configKey("last_update_check"), time.Now().Format(time.RFC3339)))

		require.NoError(t, r.ReportUpdate(t.Context()))

		assert.Contains(t, stderr.String(), "8.0.0 → 8.1.0")
	})

	t.Run("installed binary below the minimum version is still reported", func(t *testing.T) {
		r, cfg, _, stderr := runnerWithLatest(t, "8.1.0")
		r.Spec.MinVersion = "8.0.5"
		installManaged(t, cfg, r.Spec, "8.0.0")

		require.NoError(t, r.ReportUpdate(t.Context()))

		assert.Contains(t, stderr.String(), "8.0.0 → 8.1.0")
	})

	t.Run("recorded version without a binary on disk prints nothing and makes no request", func(t *testing.T) {
		r, cfg, stdout, stderr := runnerWithLatest(t)
		require.NoError(t, cfg.Set("", r.Spec.configKey("binary_version"), "8.0.0"))

		require.NoError(t, r.ReportUpdate(t.Context()))

		assert.Empty(t, stdout.String())
		assert.Empty(t, stderr.String())
	})

	t.Run("registry failure is returned with the binary name", func(t *testing.T) {
		r, cfg, stdout, stderr := runnerWithRegistryError(t, errors.New("connection refused"))
		installManaged(t, cfg, r.Spec, "8.0.0")

		err := r.ReportUpdate(t.Context())

		require.EqualError(t, err, "failed checking for Test CLI updates: failed to fetch packages: connection refused")
		assert.Empty(t, stdout.String())
		assert.Empty(t, stderr.String())
	})

	t.Run("registry HTTP error reports only the status code", func(t *testing.T) {
		r, cfg, _, _ := runnerWithRegistryError(t, &gitlab.ErrorResponse{StatusCode: http.StatusServiceUnavailable, Message: "<html>maintenance</html>"})
		installManaged(t, cfg, r.Spec, "8.0.0")

		err := r.ReportUpdate(t.Context())

		require.EqualError(t, err, "failed checking for Test CLI updates: the package registry responded with HTTP 503")
	})

	t.Run("installed binary that is current prints nothing", func(t *testing.T) {
		r, cfg, stdout, stderr := runnerWithLatest(t, "8.0.0")
		installManaged(t, cfg, r.Spec, "8.0.0")

		require.NoError(t, r.ReportUpdate(t.Context()))

		assert.Empty(t, stdout.String())
		assert.Empty(t, stderr.String())
	})

	t.Run("binary that is not installed prints nothing and makes no request", func(t *testing.T) {
		r, _, stdout, stderr := runnerWithLatest(t)

		require.NoError(t, r.ReportUpdate(t.Context()))

		assert.Empty(t, stdout.String())
		assert.Empty(t, stderr.String())
	})

	t.Run("custom binary prints nothing and makes no request", func(t *testing.T) {
		r, cfg, stdout, stderr := runnerWithLatest(t)
		custom := filepath.Join(t.TempDir(), "custom")
		require.NoError(t, os.WriteFile(custom, []byte("#!/bin/sh\n"), 0o755))
		require.NoError(t, cfg.Set("", r.Spec.configKey("binary_path"), custom))
		require.NoError(t, cfg.Set("", r.Spec.configKey("binary_version"), "8.0.0"))

		require.NoError(t, r.ReportUpdate(t.Context()))

		assert.Empty(t, stdout.String())
		assert.Empty(t, stderr.String())
	})
}
