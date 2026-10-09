//go:build !integration

package setup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/claudehooks"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestNewCmd(t *testing.T) {
	t.Parallel()
	ios, _, _, _ := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(ios)
	cmd := NewCmd(f)

	assert.Equal(t, "setup", cmd.Name())
	assert.NotNil(t, cmd.Flags().Lookup("yes"))
	assert.NotNil(t, cmd.Flags().Lookup("no-fallback-sync"))
	assert.NotNil(t, cmd.Flags().Lookup("uninstall"))
}

func TestInstallClaudeHooks(t *testing.T) {
	t.Run("creates settings file with hooks when none exists", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".claude", "settings.json")

		ios, _, _, _ := cmdtest.TestIOStreams()
		opts := &options{io: ios, settingsPathOverride: path}

		err := installClaudeHooks(opts)
		require.NoError(t, err)

		data, err := os.ReadFile(path)
		require.NoError(t, err)

		var raw map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(data, &raw))
		hooks, err := claudehooks.HooksFromRaw(raw)
		require.NoError(t, err)

		assert.Len(t, hooks["Stop"], 1)
		assert.Len(t, hooks["SessionEnd"], 1)
		assert.Equal(t, claudehooks.StopHookCommand, hooks["Stop"][0].Hooks[0].Command)
		assert.Equal(t, claudehooks.SessionEndHookCommand, hooks["SessionEnd"][0].Hooks[0].Command)
	})

	t.Run("is idempotent -- does not duplicate hooks", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".claude", "settings.json")

		ios, _, _, _ := cmdtest.TestIOStreams()
		opts := &options{io: ios, settingsPathOverride: path}

		require.NoError(t, installClaudeHooks(opts))
		require.NoError(t, installClaudeHooks(opts))
		require.NoError(t, installClaudeHooks(opts))

		data, err := os.ReadFile(path)
		require.NoError(t, err)

		var raw map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(data, &raw))
		hooks, err := claudehooks.HooksFromRaw(raw)
		require.NoError(t, err)

		assert.Len(t, hooks["Stop"], 1, "Stop hook should not be duplicated")
		assert.Len(t, hooks["SessionEnd"], 1, "SessionEnd hook should not be duplicated")
	})

	t.Run("preserves existing hooks in the file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".claude", "settings.json")

		// Write existing settings with a different hook
		existingHooks := map[string][]claudehooks.HookGroup{
			"PreToolUse": {
				{Hooks: []claudehooks.HookEntry{{Type: "command", Command: "echo pre-tool"}}},
			},
		}
		hooksJSON, err := json.Marshal(existingHooks)
		require.NoError(t, err)

		existing := map[string]json.RawMessage{"hooks": hooksJSON}

		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, claudehooks.WriteSettings(path, existing))

		ios, _, _, _ := cmdtest.TestIOStreams()
		opts := &options{io: ios, settingsPathOverride: path}

		require.NoError(t, installClaudeHooks(opts))

		data2, err2 := os.ReadFile(path)
		require.NoError(t, err2)

		var raw2 map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(data2, &raw2))

		hooks2, err := claudehooks.HooksFromRaw(raw2)
		require.NoError(t, err)

		assert.Len(t, hooks2["PreToolUse"], 1, "existing PreToolUse hook should be preserved")
		assert.Len(t, hooks2["Stop"], 1, "Stop hook should be added")
		assert.Len(t, hooks2["SessionEnd"], 1, "SessionEnd hook should be added")
	})
}

func TestRunSetup_YesSkipsPrompt(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
	path := filepath.Join(dir, ".claude", "settings.json")

	ios, _, stdout, _ := cmdtest.TestIOStreams()
	opts := &options{
		io:                   ios,
		executor:             expectLinuxInstall(t, "/usr/local/bin/glab"),
		buildInfo:            testBuild,
		goos:                 "linux",
		yes:                  true,
		settingsPathOverride: path,
	}

	err := runSetup(t.Context(), opts)
	require.NoError(t, err)

	_, err = os.Stat(path)
	require.NoError(t, err, "settings file should have been created")
	assert.Contains(t, stdout.String(), "Fallback periodic sync installed: a systemd user timer")
	assert.FileExists(t, filepath.Join(dir, ".config", "systemd", "user", "glab-govern-audit-sync.timer"))

	service, err := os.ReadFile(filepath.Join(dir, ".config", "systemd", "user", "glab-govern-audit-sync.service"))
	require.NoError(t, err)
	assert.Contains(t, string(service), `Environment="GLAB_CONFIG_DIR=`+filepath.Join(dir, "glab")+`"`, "the job uses the same config directory as setup")
}

// testBuild is the build the tests pretend is running setup.
var testBuild = api.BuildInfo{Version: "v1.121.0", Commit: "abc1234"}

// expectGlabOnPath makes glabPath the glab on PATH, reporting versionOutput
// from `glab version`.
func expectGlabOnPath(mExec *cmdtest.MockExecutor, glabPath, versionOutput string) {
	mExec.EXPECT().LookPath("glab").Return(glabPath, nil)
	mExec.EXPECT().
		ExecWithIO(gomock.Any(), glabPath, []string{"version"}, nil, nil, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _, _ []string, _ io.Reader, stdout, _ io.Writer) error {
			_, err := io.WriteString(stdout, versionOutput)
			return err
		})
}

// expectLinuxInstall returns an executor that expects the fallback periodic
// sync to be installed with systemd, running glabPath.
func expectLinuxInstall(t *testing.T, glabPath string) *cmdtest.MockExecutor {
	t.Helper()
	mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
	expectGlabOnPath(mExec, glabPath, "glab 1.121.0 (abc1234)\n")
	mExec.EXPECT().ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "daemon-reload"}, nil).Return(nil, nil)
	mExec.EXPECT().ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "enable", "--now", "glab-govern-audit-sync.timer"}, nil).Return(nil, nil)
	return mExec
}

func TestRunSetup_FallbackSyncFailureIsAWarning(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
	expectGlabOnPath(mExec, "/usr/bin/glab", "glab 1.121.0 (abc1234)\n")
	mExec.EXPECT().ExecWithCombinedOutput(gomock.Any(), "systemctl", gomock.Any(), nil).Return([]byte("Failed to connect to bus"), errors.New("exit status 1"))

	ios, _, stdout, stderr := cmdtest.TestIOStreams()
	opts := &options{
		io:                   ios,
		executor:             mExec,
		buildInfo:            testBuild,
		goos:                 "linux",
		yes:                  true,
		settingsPathOverride: filepath.Join(dir, ".claude", "settings.json"),
	}

	require.NoError(t, runSetup(t.Context(), opts))
	assert.Contains(t, stderr.String(), "warning: could not install fallback periodic sync")
	assert.Contains(t, stderr.String(), "Any previous fallback periodic sync job has been removed.")
	assert.Contains(t, stdout.String(), "Setup complete")
}

func TestSetup_NoFallbackSync(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	// The executor has no expectations, so any attempt to install the job fails the test.
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithExecutor(cmdtest.NewMockExecutor(gomock.NewController(t))),
	)

	out, err := exec("--yes --no-fallback-sync")
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(dir, ".claude", "settings.json"))
	assert.NotContains(t, out.String(), "Fallback periodic sync")
	assert.Contains(t, out.String(), "Hooks installed")
}

func TestSetup_UninstallConflictsWithNoFallbackSync(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
	_, err := exec("--uninstall --no-fallback-sync")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "none of the others can be")
}

func TestRunUninstall(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	path := filepath.Join(dir, ".claude", "settings.json")

	ios, _, stdout, _ := cmdtest.TestIOStreams()
	opts := &options{
		io:                   ios,
		executor:             expectLinuxInstall(t, "/usr/bin/glab"),
		buildInfo:            testBuild,
		goos:                 "linux",
		yes:                  true,
		settingsPathOverride: path,
	}
	require.NoError(t, runSetup(t.Context(), opts))

	mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
	mExec.EXPECT().ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "disable", "--now", "glab-govern-audit-sync.timer"}, nil).Return(nil, nil)
	mExec.EXPECT().ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "daemon-reload"}, nil).Return(nil, nil)
	opts.executor = mExec

	require.NoError(t, runUninstall(t.Context(), opts))

	assert.NoFileExists(t, filepath.Join(dir, ".config", "systemd", "user", "glab-govern-audit-sync.timer"))
	assert.Contains(t, stdout.String(), "Fallback periodic sync removed")

	raw, err := claudehooks.LoadRawSettings(path)
	require.NoError(t, err)
	hooks, err := claudehooks.HooksFromRaw(raw)
	require.NoError(t, err)
	assert.True(t, claudehooks.HookPresent(hooks, "Stop", claudehooks.StopHookCommand), "uninstall leaves the hooks in place")
	assert.True(t, claudehooks.HookPresent(hooks, "SessionEnd", claudehooks.SessionEndHookCommand), "uninstall leaves the hooks in place")
}

func TestRunUninstall_NothingInstalled(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
	mExec.EXPECT().ExecWithCombinedOutput(gomock.Any(), "systemctl", gomock.Any(), nil).Return(nil, errors.New("unit not loaded"))

	ios, _, stdout, _ := cmdtest.TestIOStreams()
	opts := &options{
		io:       ios,
		executor: mExec,
		goos:     "linux",
		yes:      true,
	}

	require.NoError(t, runUninstall(t.Context(), opts))
	assert.Contains(t, stdout.String(), "No fallback periodic sync job found")
}

func TestGlabBinaryPath(t *testing.T) {
	t.Parallel()

	running, err := os.Executable()
	require.NoError(t, err)

	t.Run("keeps the PATH entry when it is the running build", func(t *testing.T) {
		t.Parallel()
		mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
		expectGlabOnPath(mExec, "/home/me/.local/share/mise/shims/glab", "glab 1.121.0 (abc1234)\n")

		got, err := glabBinaryPath(t.Context(), mExec, testBuild)
		require.NoError(t, err)
		assert.Equal(t, "/home/me/.local/share/mise/shims/glab", got, "the shim or symlink is kept, not resolved")
	})

	t.Run("uses the running binary when PATH has a different build", func(t *testing.T) {
		t.Parallel()
		mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
		expectGlabOnPath(mExec, "/usr/local/bin/glab", "glab 1.120.0 (34ea62162)\n")

		got, err := glabBinaryPath(t.Context(), mExec, testBuild)
		require.NoError(t, err)
		assert.Equal(t, running, got)
	})

	t.Run("uses the running binary when the PATH entry does not run", func(t *testing.T) {
		t.Parallel()
		mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
		mExec.EXPECT().LookPath("glab").Return("/usr/local/bin/glab", nil)
		mExec.EXPECT().
			ExecWithIO(gomock.Any(), "/usr/local/bin/glab", []string{"version"}, nil, nil, gomock.Any(), gomock.Any()).
			Return(errors.New("exec format error"))

		got, err := glabBinaryPath(t.Context(), mExec, testBuild)
		require.NoError(t, err)
		assert.Equal(t, running, got)
	})

	t.Run("uses the running binary when glab is not in PATH", func(t *testing.T) {
		t.Parallel()
		mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
		mExec.EXPECT().LookPath("glab").Return("", errors.New("not found"))

		got, err := glabBinaryPath(t.Context(), mExec, testBuild)
		require.NoError(t, err)
		assert.Equal(t, running, got)
	})
}

func TestRunSetup_NonInteractiveWithoutYesFlag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude", "settings.json")
	ios, _, _, _ := cmdtest.TestIOStreams()

	opts := &options{
		io:                   ios,
		yes:                  false,
		settingsPathOverride: path,
	}
	err := runSetup(t.Context(), opts)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-interactive")

	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr), "settings file should not have been created")
}

func TestInstallClaudeHooks_PreservesAllSettingsKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude", "settings.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))

	existing := `{
  "model": "claude-opus-4-5",
  "env": {"MY_VAR": "value"},
  "permissions": {
    "allow": ["Bash"],
    "deny": ["WebSearch"]
  },
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
	"hooks": [{"type": "command", "command": "echo pre-tool", "timeout": 5}]
      }
    ]
  }
}`
	require.NoError(t, os.WriteFile(path, []byte(existing), 0o644))

	ios, _, _, _ := cmdtest.TestIOStreams()
	opts := &options{io: ios, yes: true, settingsPathOverride: path}
	require.NoError(t, installClaudeHooks(opts))

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))

	assert.Contains(t, raw, "model", "model key should be preserved")
	assert.Contains(t, raw, "env", "env key should be preserved")
	assert.Contains(t, raw, "permissions", "permissions key should be preserved")

	hooks, err := claudehooks.HooksFromRaw(raw)
	require.NoError(t, err)
	require.Len(t, hooks["PreToolUse"], 1)
	assert.Equal(t, "Bash", hooks["PreToolUse"][0].Matcher, "matcher should be preserved")
	assert.Equal(t, 5, hooks["PreToolUse"][0].Hooks[0].Timeout, "timeout should be preserved")

	assert.Len(t, hooks["Stop"], 1)
	assert.Len(t, hooks["SessionEnd"], 1)
}

func TestMCPDestructiveAnnotation(t *testing.T) {
	t.Parallel()
	ios, _, _, _ := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(ios)
	cmd := NewCmd(f)
	assert.Equal(t, "true", cmd.Annotations[mcpannotations.Destructive])
}
