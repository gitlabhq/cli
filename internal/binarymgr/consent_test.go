//go:build !integration

package binarymgr

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestRunner_checkAutoRun_nonInteractive(t *testing.T) {
	t.Run("runs a verified binary without prompting when there is no TTY", func(t *testing.T) {
		r, _ := runnerFor(t)
		assert.NoError(t, r.checkAutoRun(t.Context()))
	})

	t.Run("no prompt when auto_run is set", func(t *testing.T) {
		r, cfg := runnerFor(t)
		require.NoError(t, cfg.Set("", r.Spec.configKey("auto_run"), "true"))
		assert.NoError(t, r.checkAutoRun(t.Context()))
	})
}

func TestConsent_respectsNoPromptOnATTY(t *testing.T) {
	t.Setenv("GLAB_CONFIG_DIR", t.TempDir())
	ios, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))
	ios.SetPrompt("true")
	spec := testSpec()
	r := &Runner{IO: ios, Cfg: config.NewBlankConfig(), Spec: spec, Manager: NewManager(ios, spec)}

	_, _, err := r.Manager.promptDownload(t.Context(), "")
	require.Error(t, err)

	assert.NoError(t, r.checkAutoRun(t.Context()))
}

func TestManager_promptDownload_nonInteractive(t *testing.T) {
	t.Run("errors naming the auto-download env var", func(t *testing.T) {
		r, _ := runnerFor(t)
		t.Setenv("GITLAB_CI", "")
		t.Setenv("CI", "")

		_, _, err := r.Manager.promptDownload(t.Context(), "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "TEST_CLI_AUTO_DOWNLOAD")
	})

	t.Run("errors mentioning CI in a CI environment", func(t *testing.T) {
		r, _ := runnerFor(t)
		t.Setenv("GITLAB_CI", "true")

		_, _, err := r.Manager.promptDownload(t.Context(), "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "in CI")
	})

	t.Run("no error when download already consented", func(t *testing.T) {
		r, _ := runnerFor(t)

		ok, _, err := r.Manager.promptDownload(t.Context(), "true")
		require.NoError(t, err)
		assert.True(t, ok)
	})
}
