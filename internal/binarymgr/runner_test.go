//go:build !integration

package binarymgr

import (
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
