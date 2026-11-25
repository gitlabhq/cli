//go:build !integration

package setup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/claudehooks"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/fallbacksync"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/gaig"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/opencodeplugin"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/syncstate"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
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

func TestSetup_Agents(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))

	run := func(t *testing.T, args string) (string, error) {
		t.Helper()
		mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
		mExec.EXPECT().LookPath("glab").Return("", errors.New("not found")).AnyTimes()
		mExec.EXPECT().ExecWithCombinedOutput(gomock.Any(), gomock.Any(), gomock.Any(), nil).Return(nil, nil).AnyTimes()
		exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithExecutor(mExec))
		out, err := exec("--yes " + args)
		if out == nil {
			return "", err
		}
		return out.String(), err
	}

	out, err := run(t, "--agents claude-code,codex,cursor")
	require.NoError(t, err)
	assert.Contains(t, out, "Syncing sessions from: codex, cursor.")
	agents, err := fallbacksync.DiscoveredAgents()
	require.NoError(t, err)
	assert.Equal(t, []string{"codex", "cursor"}, agents)
	hookAgents, _, err := fallbacksync.RecordedHookAgents()
	require.NoError(t, err)
	assert.Equal(t, []string{"claude-code"}, hookAgents)

	_, err = run(t, "")
	require.NoError(t, err)
	agents, err = fallbacksync.DiscoveredAgents()
	require.NoError(t, err)
	assert.Equal(t, []string{"codex", "cursor"}, agents, "setup without --agents keeps the earlier choice")

	_, err = run(t, "--agents opencode")
	require.NoError(t, err)
	agents, err = fallbacksync.DiscoveredAgents()
	require.NoError(t, err)
	assert.Equal(t, []string{"codex", "cursor"}, agents, "--agents adds to the earlier choice")
	hookAgents, _, err = fallbacksync.RecordedHookAgents()
	require.NoError(t, err)
	assert.Equal(t, []string{"claude-code", "opencode"}, hookAgents)
}

func TestSetup_AgentsValidation(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)

	_, err := exec("--yes --agents claude")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unsupported agent "claude" in --agents; use claude-code, opencode, codex, cursor`)

	_, err = exec(`--yes --agents ""`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--agents needs at least one agent; to remove everything setup installed, use --uninstall")

	_, err = exec("--yes --agents opencode,codex --no-fallback-sync")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "codex sessions are only synced by the fallback periodic sync")

	_, err = exec("--yes --agents codex --uninstall")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "none of the others can be")
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
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
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
	writeSessionState(t)
	setups, err := fallbacksync.ReadAgentSetups()
	require.NoError(t, err)
	require.Contains(t, setups, "claude-code")

	require.NoError(t, runUninstall(t.Context(), opts))

	assert.NoFileExists(t, filepath.Join(dir, ".config", "systemd", "user", "glab-govern-audit-sync.timer"))
	assert.Contains(t, stdout.String(), "Fallback periodic sync removed")
	assert.NoDirExists(t, gaig.StateDir())
	setups, err = fallbacksync.ReadAgentSetups()
	require.NoError(t, err)
	assert.Empty(t, setups, "the next setup uploads from when it runs")
	assert.Contains(t, stdout.String(), "Local records of your agent sessions removed")

	raw, err := claudehooks.LoadRawSettings(path)
	require.NoError(t, err)
	hooks, err := claudehooks.HooksFromRaw(raw)
	require.NoError(t, err)
	assert.False(t, claudehooks.HookPresent(hooks, "Stop", claudehooks.StopHookCommand))
	assert.False(t, claudehooks.HookPresent(hooks, "SessionEnd", claudehooks.SessionEndHookCommand))
	assert.Contains(t, stdout.String(), "Hooks removed")
}

func TestRunUninstall_HooksOnly(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
	path := filepath.Join(dir, ".claude", "settings.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(`{"model":"opus","hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo user-hook"}]}]}}`), 0o644))

	ios, _, stdout, _ := cmdtest.TestIOStreams()
	opts := &options{io: ios, yes: true, noFallbackSync: true, settingsPathOverride: path}
	require.NoError(t, runSetup(t.Context(), opts))

	mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
	mExec.EXPECT().
		ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "is-active", "--quiet", "glab-govern-audit-sync.timer"}, nil).
		Return(nil, &exec.ExitError{})
	opts.executor = mExec
	opts.goos = "linux"

	require.NoError(t, runUninstall(t.Context(), opts), "hooks without a job are still removed")
	assert.Contains(t, stdout.String(), "Hooks removed")
	assert.NotContains(t, stdout.String(), "Fallback periodic sync removed")

	raw, err := claudehooks.LoadRawSettings(path)
	require.NoError(t, err)
	hooks, err := claudehooks.HooksFromRaw(raw)
	require.NoError(t, err)
	assert.False(t, claudehooks.HookPresent(hooks, "Stop", claudehooks.StopHookCommand))
	assert.False(t, claudehooks.HookPresent(hooks, "SessionEnd", claudehooks.SessionEndHookCommand))
	assert.True(t, claudehooks.HookPresent(hooks, "Stop", "echo user-hook"))
	assert.JSONEq(t, `"opus"`, string(raw["model"]))
}

func TestRunSetup_ReplacesHooksFromBeforeTheRename(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	path := filepath.Join(dir, ".claude", "settings.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(`{"hooks":{
		"Stop":[{"hooks":[{"type":"command","command":"glab govern audit sync --silent >/dev/null 2>&1 &"}]}],
		"SessionEnd":[{"hooks":[{"type":"command","command":"glab govern audit sync --silent --complete >/dev/null 2>&1 &"}]}]
	}}`), 0o644))

	ios, _, _, _ := cmdtest.TestIOStreams()
	opts := &options{io: ios, yes: true, noFallbackSync: true, settingsPathOverride: path}
	require.NoError(t, runSetup(t.Context(), opts))

	raw, err := claudehooks.LoadRawSettings(path)
	require.NoError(t, err)
	hooks, err := claudehooks.HooksFromRaw(raw)
	require.NoError(t, err)
	assert.False(t, claudehooks.LegacyHookPresent(hooks))
	require.Len(t, hooks["Stop"], 1, "sessions must not be synced twice")
	require.Len(t, hooks["SessionEnd"], 1)
	assert.Equal(t, claudehooks.StopHookCommand, hooks["Stop"][0].Hooks[0].Command)
	assert.Equal(t, claudehooks.SessionEndHookCommand, hooks["SessionEnd"][0].Hooks[0].Command)
}

func TestRunSetup_KeepsOneHookWhenBothFormsArePresent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	path := filepath.Join(dir, ".claude", "settings.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(`{"hooks":{
		"Stop":[
			{"hooks":[{"type":"command","command":"glab minder audit sync --silent >/dev/null 2>&1 &"}]},
			{"hooks":[{"type":"command","command":"glab govern audit sync --silent >/dev/null 2>&1 &"}]}
		]
	}}`), 0o644))

	ios, _, _, _ := cmdtest.TestIOStreams()
	opts := &options{io: ios, yes: true, noFallbackSync: true, settingsPathOverride: path}
	require.NoError(t, runSetup(t.Context(), opts))

	raw, err := claudehooks.LoadRawSettings(path)
	require.NoError(t, err)
	hooks, err := claudehooks.HooksFromRaw(raw)
	require.NoError(t, err)
	require.Len(t, hooks["Stop"], 1, "sessions must not be synced twice")
	assert.Equal(t, claudehooks.StopHookCommand, hooks["Stop"][0].Hooks[0].Command)
	require.Len(t, hooks["SessionEnd"], 1)
}

func TestRunUninstall_RemovesHooksFromBeforeTheRename(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
	path := filepath.Join(dir, ".claude", "settings.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(`{"model":"opus","hooks":{
		"Stop":[{"hooks":[{"type":"command","command":"glab govern audit sync --silent >/dev/null 2>&1 &"}]}],
		"SessionEnd":[{"hooks":[{"type":"command","command":"glab govern audit sync --silent --complete >/dev/null 2>&1 &"}]}]
	}}`), 0o644))

	mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
	mExec.EXPECT().
		ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "is-active", "--quiet", "glab-govern-audit-sync.timer"}, nil).
		Return(nil, &exec.ExitError{})
	ios, _, stdout, _ := cmdtest.TestIOStreams()
	opts := &options{io: ios, yes: true, executor: mExec, goos: "linux", settingsPathOverride: path}

	require.NoError(t, runUninstall(t.Context(), opts))
	assert.Contains(t, stdout.String(), "Hooks removed")

	raw, err := claudehooks.LoadRawSettings(path)
	require.NoError(t, err)
	assert.NotContains(t, raw, "hooks")
	assert.JSONEq(t, `"opus"`, string(raw["model"]))
}

func TestRunUninstall_RemovesEmptiedHooksKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
	path := filepath.Join(dir, ".claude", "settings.json")

	ios, _, _, _ := cmdtest.TestIOStreams()
	opts := &options{io: ios, yes: true, noFallbackSync: true, settingsPathOverride: path}
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(`{"model":"opus"}`), 0o644))
	require.NoError(t, runSetup(t.Context(), opts))

	mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
	mExec.EXPECT().
		ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "is-active", "--quiet", "glab-govern-audit-sync.timer"}, nil).
		Return(nil, &exec.ExitError{})
	opts.executor = mExec
	opts.goos = "linux"

	require.NoError(t, runUninstall(t.Context(), opts))

	raw, err := claudehooks.LoadRawSettings(path)
	require.NoError(t, err)
	assert.NotContains(t, raw, "hooks", "a hooks key emptied by uninstall is removed, not left as {}")
	assert.JSONEq(t, `"opus"`, string(raw["model"]))
}

func TestRunUninstall_CorruptSettings(t *testing.T) {
	writeCorruptSettings := func(t *testing.T, dir string) string {
		t.Helper()
		path := filepath.Join(dir, ".claude", "settings.json")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o644))
		return path
	}
	expectNoJob := func(t *testing.T) *cmdtest.MockExecutor {
		t.Helper()
		mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
		mExec.EXPECT().
			ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "is-active", "--quiet", "glab-govern-audit-sync.timer"}, nil).
			Return(nil, &exec.ExitError{})
		return mExec
	}

	t.Run("nothing else to remove fails before prompting", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("HOME", dir)
		t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
		path := writeCorruptSettings(t, dir)

		ios, _, _, _ := cmdtest.TestIOStreams()
		// Without --yes and without a terminal, prompting would fail differently.
		err := runUninstall(t.Context(), &options{io: ios, executor: expectNoJob(t), goos: "linux", settingsPathOverride: path})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "the agent session hooks were not removed")
	})

	t.Run("keeps the session records while the hooks may still write them", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("HOME", dir)
		t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
		path := writeCorruptSettings(t, dir)
		writeSessionState(t)

		ios, _, stdout, _ := cmdtest.TestIOStreams()
		// Without --yes and without a terminal, prompting would fail differently.
		err := runUninstall(t.Context(), &options{io: ios, executor: expectNoJob(t), goos: "linux", settingsPathOverride: path})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "the agent session hooks were not removed", "records that are kept don't make it prompt")
		assert.DirExists(t, gaig.StateDir())
		assert.NotContains(t, stdout.String(), "Local records of your agent sessions removed")
	})

	t.Run("the rest is removed and the error says what is left", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("HOME", dir)
		t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
		path := writeCorruptSettings(t, dir)
		require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"codex"}))
		setups := fallbacksync.AgentSetups{"claude-code": {SetUpAt: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)}}
		require.NoError(t, fallbacksync.SetAgentSetups(setups))

		ios, _, stdout, stderr := cmdtest.TestIOStreams()
		err := runUninstall(t.Context(), &options{io: ios, executor: expectNoJob(t), goos: "linux", yes: true, settingsPathOverride: path})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "run 'glab minder setup --uninstall' again")
		assert.Contains(t, stderr.String(), "its hooks will not be removed", "the user is warned before confirming")
		assert.Contains(t, stdout.String(), "Stopped syncing codex sessions.")

		agents, err := fallbacksync.DiscoveredAgents()
		require.NoError(t, err)
		assert.Empty(t, agents)
		kept, err := fallbacksync.ReadAgentSetups()
		require.NoError(t, err)
		assert.Equal(t, setups, kept, "hooks that stay installed still upload only from when they were set up")
	})
}

func TestRunUninstall_UnreadableOpenCodePlugin(t *testing.T) {
	setUp := func(t *testing.T) {
		t.Helper()
		dir := t.TempDir()
		t.Setenv("HOME", dir)
		t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
		require.NoError(t, os.MkdirAll(filepath.Join(dir, ".config", "opencode", "plugins", opencodeplugin.FileName), 0o755))
	}
	expectNoJob := func(t *testing.T) *cmdtest.MockExecutor {
		t.Helper()
		mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
		mExec.EXPECT().
			ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "is-active", "--quiet", "glab-govern-audit-sync.timer"}, nil).
			Return(nil, &exec.ExitError{})
		return mExec
	}

	t.Run("nothing else to remove fails", func(t *testing.T) {
		setUp(t)

		ios, _, stdout, stderr := cmdtest.TestIOStreams()
		err := runUninstall(t.Context(), &options{io: ios, executor: expectNoJob(t), goos: "linux"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "the OpenCode plugin may still be installed")
		assert.NotContains(t, stdout.String(), "Nothing to uninstall")
		assert.Empty(t, stderr.String(), "the error says it once")
	})

	t.Run("the rest is removed and the error says what is left", func(t *testing.T) {
		setUp(t)
		require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"codex"}))

		ios, _, stdout, stderr := cmdtest.TestIOStreams()
		err := runUninstall(t.Context(), &options{io: ios, executor: expectNoJob(t), goos: "linux", yes: true})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "the OpenCode plugin may still be installed")
		assert.Contains(t, stderr.String(), "so it will not be removed", "the user is warned before confirming")
		assert.Contains(t, stdout.String(), "Stopped syncing codex sessions.")
	})
}

// writeSessionState writes the records that syncing leaves in glab's
// configuration directory.
func writeSessionState(t *testing.T) {
	t.Helper()
	for _, name := range []string{"cursors/sess-1", "cursors/sess-1.meta", "identities/42_claude-code.json", "fallback-sync-status.json"} {
		path := filepath.Join(gaig.StateDir(), name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("{}"), 0o600))
	}
}

func TestRunUninstall_OnlySessionState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
	writeSessionState(t)

	mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
	mExec.EXPECT().
		ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "is-active", "--quiet", "glab-govern-audit-sync.timer"}, nil).
		Return(nil, &exec.ExitError{})

	ios, _, stdout, _ := cmdtest.TestIOStreams()
	opts := &options{io: ios, executor: mExec, goos: "linux", yes: true}

	require.NoError(t, runUninstall(t.Context(), opts))

	assert.NotContains(t, stdout.String(), "Nothing to uninstall", "records left by an earlier uninstall are still removed")
	assert.NoDirExists(t, gaig.StateDir())
}

func TestRunUninstall_UnreadableSessionStateFails(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory that can't be listed")
	}
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
	// An execute-only directory can't be listed, but files in it can be opened,
	// so only the records check fails.
	require.NoError(t, os.MkdirAll(gaig.StateDir(), 0o700))
	require.NoError(t, os.Chmod(gaig.StateDir(), 0o100))
	t.Cleanup(func() { _ = os.Chmod(gaig.StateDir(), 0o700) })

	mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
	mExec.EXPECT().
		ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "is-active", "--quiet", "glab-govern-audit-sync.timer"}, nil).
		Return(nil, &exec.ExitError{})

	ios, _, stdout, _ := cmdtest.TestIOStreams()
	opts := &options{io: ios, executor: mExec, goos: "linux"}

	err := runUninstall(t.Context(), opts)

	require.ErrorContains(t, err, "glab's local records of your agent sessions were not removed")
	assert.NotContains(t, stdout.String(), "Nothing to uninstall")
	assert.DirExists(t, gaig.StateDir())
}

func TestRunUninstall_NothingInstalled(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))

	mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
	mExec.EXPECT().
		ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "is-active", "--quiet", "glab-govern-audit-sync.timer"}, nil).
		Return(nil, &exec.ExitError{})

	ios, _, stdout, _ := cmdtest.TestIOStreams()
	opts := &options{
		io:       ios,
		executor: mExec,
		goos:     "linux",
		// Without --yes and without a terminal, prompting would fail.
		yes: false,
	}

	require.NoError(t, runUninstall(t.Context(), opts), "nothing to remove means nothing to confirm")
	assert.Contains(t, stdout.String(), "Nothing to uninstall")
}

func TestRunUninstall_LoadedJobWithoutFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))

	mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
	gomock.InOrder(
		mExec.EXPECT().
			ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "is-active", "--quiet", "glab-govern-audit-sync.timer"}, nil).
			Return(nil, nil),
		mExec.EXPECT().
			ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "show", "glab-govern-audit-sync.service", "--property=ExecMainStartTimestampMonotonic,ExecMainStatus"}, nil).
			Return([]byte("ExecMainStartTimestampMonotonic=0\nExecMainStatus=0\n"), nil),
		mExec.EXPECT().
			ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "disable", "--now", "glab-govern-audit-sync.timer"}, nil).
			Return(nil, nil),
	)

	ios, _, _, _ := cmdtest.TestIOStreams()
	opts := &options{io: ios, executor: mExec, goos: "linux", yes: true}

	require.NoError(t, runUninstall(t.Context(), opts), "a loaded job is unloaded even when its files are gone")
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
	t.Setenv("HOME", dir)
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

func TestInstallClaudeHooks_PreservesExistingHookFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude", "settings.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	existing := `{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "~/.claude/hooks/print-mr-link.sh",
            "if": "Bash(git push*)",
            "statusMessage": "Checking for MR link"
          }
        ]
      }
    ]
  }
}`
	require.NoError(t, os.WriteFile(path, []byte(existing), 0o644))

	ios, _, _, _ := cmdtest.TestIOStreams()
	require.NoError(t, installClaudeHooks(&options{io: ios, settingsPathOverride: path}))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var settings struct {
		Hooks map[string][]struct {
			Matcher string           `json:"matcher"`
			Hooks   []map[string]any `json:"hooks"`
		} `json:"hooks"`
	}
	require.NoError(t, json.Unmarshal(data, &settings))

	entry := settings.Hooks["PostToolUse"][0].Hooks[0]
	assert.Equal(t, "Bash(git push*)", entry["if"], "dropping if would run the hook after every Bash call")
	assert.Equal(t, "Checking for MR link", entry["statusMessage"])
	assert.Len(t, settings.Hooks["Stop"], 1)
	assert.Contains(t, string(data), `>/dev/null 2>&1 &`)
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

func TestSetupPrompt(t *testing.T) {
	t.Parallel()

	assert.Equal(t,
		"glab will install agent session hooks in ~/.claude/settings.json and a systemd user timer (~/.config/systemd/user/glab-govern-audit-sync.timer) that syncs sessions in the background every 30 minutes, replacing any existing job. "+
			"Agent sessions will be uploaded to the GitLab project of the repository they run in, including your prompts and each tool call's arguments, such as commands and the contents of files the agent writes. "+
			"When the agent works in more than one repository, a session is split across their projects that are on the same GitLab instance and in the same top-level group as the project the session started in, and its activity in other repositories is not uploaded. "+
			"Common token and key formats are replaced first, but other secrets in them are uploaded as they are. "+
			"glab also keeps a local record of each agent session in its configuration directory, with the session ID, the agent, the transcript path, how far it was synced, and the folders the hooks ran in and their projects, including sessions outside GitLab repositories, which are never uploaded. "+
			"Only activity from now on will be uploaded, not earlier sessions or the earlier turns of sessions you resume. Do you wish to continue?",
		setupPrompt("linux", false, plan{claudeHooks: true, uploads: uploadScope{fromNow: []string{"claude-code"}}}), "every setup says what is uploaded and what is kept, not only --agents")

	withAgents := setupPrompt("linux", false, plan{claudeHooks: true, discovered: []string{"codex", "cursor"}, uploads: uploadScope{fromNow: []string{"claude-code", "codex", "cursor"}}})
	assert.Contains(t, withAgents, "including your prompts and each tool call's arguments, such as commands and the contents of files the agent writes. When the agent works in more than one repository, a session is split across their projects that are on the same GitLab instance and in the same top-level group as the project the session started in, and its activity in other repositories is not uploaded. Common token and key formats are replaced first, but other secrets in them are uploaded as they are")
	assert.Contains(t, withAgents, "with the session ID, the agent, the transcript path, how far it was synced, and the folders the hooks ran in and their projects,")
	assert.Contains(t, withAgents, "The job will also upload your codex and cursor sessions the same way. Only activity from now on will be uploaded, not earlier sessions or the earlier turns of sessions you resume. Do you wish to continue?")

	hooksOnly := setupPrompt("linux", true, plan{claudeHooks: true, discovered: []string{"codex"}})
	assert.Contains(t, hooksOnly, "including your prompts and each tool call's arguments")
	assert.NotContains(t, hooksOnly, "codex", "without the job, enabled agents are not synced")

	assert.Contains(t, setupPrompt("linux", false, plan{claudeHooks: true, openCodePlugin: "/h/.config/opencode/plugins/glab-govern.js"}),
		"glab will install agent session hooks in ~/.claude/settings.json, an OpenCode plugin in /h/.config/opencode/plugins/glab-govern.js that runs for all your OpenCode sessions, and a systemd user timer")
	assert.Contains(t, setupPrompt("linux", true, plan{openCodePlugin: "/p.js"}),
		"glab will install an OpenCode plugin in /p.js that runs for all your OpenCode sessions. Agent sessions")
}

func TestUninstallPrompt(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "glab will remove the agent session hooks from ~/.claude/settings.json and the fallback periodic sync job. Do you wish to continue?", uninstallPrompt(true, "", true, nil, false))
	assert.Equal(t, "glab will remove the fallback periodic sync job. Do you wish to continue?", uninstallPrompt(false, "", true, nil, false))
	assert.Equal(t, "glab will remove the agent session hooks from ~/.claude/settings.json and the fallback periodic sync job. It will also stop syncing codex and cursor sessions. Do you wish to continue?",
		uninstallPrompt(true, "", true, []string{"codex", "cursor"}, false))
	assert.Equal(t, "glab will remove the agent session hooks from ~/.claude/settings.json, the OpenCode plugin at /p.js, and the fallback periodic sync job. It will also stop syncing codex sessions. Do you wish to continue?",
		uninstallPrompt(true, "/p.js", true, []string{"codex"}, false))
	assert.Equal(t, "glab will remove the fallback periodic sync job and glab's local records of your agent sessions. Do you wish to continue?",
		uninstallPrompt(false, "", true, nil, true))
	assert.Equal(t, "glab will remove glab's local records of your agent sessions. Do you wish to continue?",
		uninstallPrompt(false, "", false, nil, true), "a job that isn't installed isn't listed")
	assert.Equal(t, "glab will stop syncing codex sessions. Do you wish to continue?",
		uninstallPrompt(false, "", false, []string{"codex"}, false))
	assert.Equal(t, "glab will turn off session discovery. Do you wish to continue?",
		uninstallPrompt(false, "", false, nil, false))
}

func TestRunSetup_NamesAgentsEnabledEarlier(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"cursor"}))

	// Not a terminal and no --yes, so setup stops at the prompt it would show.
	ios, _, _, _ := cmdtest.TestIOStreams()
	err := runSetup(t.Context(), &options{io: ios, goos: "linux", settingsPathOverride: filepath.Join(dir, ".claude", "settings.json")})
	require.Error(t, err)

	agents, err := fallbacksync.DiscoveredAgents()
	require.NoError(t, err)
	assert.Equal(t, []string{"cursor"}, agents, "a plain setup keeps the earlier choice, which its prompt names")
}

func TestRunUninstall_StopsDiscovery(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"codex", "cursor"}))

	mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
	mExec.EXPECT().
		ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "is-active", "--quiet", "glab-govern-audit-sync.timer"}, nil).
		Return(nil, &exec.ExitError{})

	ios, _, stdout, _ := cmdtest.TestIOStreams()
	require.NoError(t, runUninstall(t.Context(), &options{io: ios, executor: mExec, goos: "linux", yes: true}))

	agents, err := fallbacksync.DiscoveredAgents()
	require.NoError(t, err)
	assert.Empty(t, agents, "a later setup does not bring discovery back on its own")
	assert.Contains(t, stdout.String(), "Stopped syncing codex, cursor sessions.")
}

func TestRunUninstall_CorruptAgentList(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	configDir := filepath.Join(dir, "glab")
	t.Setenv("GLAB_CONFIG_DIR", configDir)
	opts := &options{executor: expectLinuxInstall(t, "/usr/bin/glab"), buildInfo: testBuild, goos: "linux", yes: true}
	ios, _, stdout, _ := cmdtest.TestIOStreams()
	opts.io = ios
	opts.settingsPathOverride = filepath.Join(dir, ".claude", "settings.json")
	require.NoError(t, runSetup(t.Context(), opts))

	agentsFile := filepath.Join(configDir, "gaig", "discovered-agents.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(agentsFile), 0o755))
	require.NoError(t, os.WriteFile(agentsFile, []byte("{not json"), 0o600))

	mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
	mExec.EXPECT().ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "disable", "--now", "glab-govern-audit-sync.timer"}, nil).Return(nil, nil)
	mExec.EXPECT().ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "daemon-reload"}, nil).Return(nil, nil)
	opts.executor = mExec

	require.NoError(t, runUninstall(t.Context(), opts), "a corrupt agent list does not block uninstall")

	assert.Contains(t, stdout.String(), "Fallback periodic sync removed")
	agents, err := fallbacksync.DiscoveredAgents()
	require.NoError(t, err, "the list is reset")
	assert.Empty(t, agents)
}

// hookOnlyOptions returns options for a setup without the fallback periodic
// sync, for the agents given, or for the earlier choice when there are none.
func hookOnlyOptions(ios *iostreams.IOStreams, dir string, agents ...string) *options {
	return &options{
		io:                   ios,
		yes:                  true,
		noFallbackSync:       true,
		agents:               agents,
		agentsChanged:        len(agents) > 0,
		settingsPathOverride: filepath.Join(dir, ".claude", "settings.json"),
	}
}

func TestRunSetup_InstallsOpenCodePlugin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))

	ios, _, stdout, _ := cmdtest.TestIOStreams()
	require.NoError(t, runSetup(t.Context(), hookOnlyOptions(ios, dir, "opencode")))

	plugin := filepath.Join(dir, ".config", "opencode", "plugins", opencodeplugin.FileName)
	state, err := opencodeplugin.Check(plugin)
	require.NoError(t, err)
	assert.Equal(t, opencodeplugin.Current, state)
	assert.Contains(t, stdout.String(), "OpenCode plugin installed in ~/.config/opencode/plugins/glab-govern.js")
	assert.NoFileExists(t, filepath.Join(dir, ".claude", "settings.json"), "Claude Code was not chosen")
}

func TestRunSetup_DoesNotInstallOpenCodePluginByDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".config", "opencode"), 0o755))

	ios, _, stdout, _ := cmdtest.TestIOStreams()
	require.NoError(t, runSetup(t.Context(), hookOnlyOptions(ios, dir)))

	assert.NoDirExists(t, filepath.Join(dir, ".config", "opencode", "plugins"), "OpenCode is synced only when chosen")
	assert.NotContains(t, stdout.String(), "OpenCode")
	assert.FileExists(t, filepath.Join(dir, ".claude", "settings.json"))
}

func TestRunSetup_AgentsAddToEarlierSetup(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
	settings := filepath.Join(dir, ".claude", "settings.json")
	plugin := filepath.Join(dir, ".config", "opencode", "plugins", opencodeplugin.FileName)

	ios, _, _, _ := cmdtest.TestIOStreams()
	require.NoError(t, runSetup(t.Context(), hookOnlyOptions(ios, dir)))
	installed, err := claudehooks.SyncHookInstalled(settings)
	require.NoError(t, err)
	require.True(t, installed)

	ios, _, _, _ = cmdtest.TestIOStreams()
	require.NoError(t, runSetup(t.Context(), hookOnlyOptions(ios, dir, "opencode")))
	installed, err = claudehooks.SyncHookInstalled(settings)
	require.NoError(t, err)
	assert.True(t, installed, "setting up another agent keeps the Claude Code hooks")
	assert.FileExists(t, plugin)

	ios, _, stdout, _ := cmdtest.TestIOStreams()
	require.NoError(t, runSetup(t.Context(), hookOnlyOptions(ios, dir)))
	assert.Contains(t, stdout.String(), "Hooks installed", "a plain setup keeps both agents")
	assert.Contains(t, stdout.String(), "OpenCode plugin installed")
}

func TestRunSetup_OnlyTheAgentsPassed(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))

	ios, _, _, _ := cmdtest.TestIOStreams()
	require.NoError(t, runSetup(t.Context(), hookOnlyOptions(ios, dir, "opencode")))
	ios, _, stdout, _ := cmdtest.TestIOStreams()
	require.NoError(t, runSetup(t.Context(), hookOnlyOptions(ios, dir)))

	assert.NoFileExists(t, filepath.Join(dir, ".claude", "settings.json"), "Claude Code was never set up")
	assert.Contains(t, stdout.String(), "OpenCode plugin installed")
}

func TestRunSetup_KeepsHooksFromOlderGlab(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
	settings := filepath.Join(dir, ".claude", "settings.json")

	// An older glab installed the hooks without recording its choice.
	ios, _, _, _ := cmdtest.TestIOStreams()
	require.NoError(t, installClaudeHooks(hookOnlyOptions(ios, dir)))

	require.NoError(t, runSetup(t.Context(), hookOnlyOptions(ios, dir, "opencode")))
	hookAgents, _, err := fallbacksync.RecordedHookAgents()
	require.NoError(t, err)
	assert.Equal(t, []string{"claude-code", "opencode"}, hookAgents)
	installed, err := claudehooks.SyncHookInstalled(settings)
	require.NoError(t, err)
	assert.True(t, installed)
}

func TestRunSetup_ForeignOpenCodePlugin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
	plugin := filepath.Join(dir, ".config", "opencode", "plugins", opencodeplugin.FileName)
	require.NoError(t, os.MkdirAll(filepath.Dir(plugin), 0o755))
	require.NoError(t, os.WriteFile(plugin, []byte("// the user's own plugin\n"), 0o644))

	ios, _, _, _ := cmdtest.TestIOStreams()
	err := runSetup(t.Context(), hookOnlyOptions(ios, dir, "opencode"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "~/.config/opencode/plugins/glab-govern.js was not installed by glab")

	data, err := os.ReadFile(plugin)
	require.NoError(t, err)
	assert.Equal(t, "// the user's own plugin\n", string(data))

	_, recorded, err := fallbacksync.RecordedHookAgents()
	require.NoError(t, err)
	assert.False(t, recorded, "a failed setup records nothing")
}

func TestRunUninstall_RemovesOpenCodePlugin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
	plugins := filepath.Join(dir, ".config", "opencode", "plugins")
	require.NoError(t, os.MkdirAll(plugins, 0o755))
	other := filepath.Join(plugins, "other.js")
	require.NoError(t, os.WriteFile(other, []byte("// another plugin\n"), 0o644))

	ios, _, stdout, _ := cmdtest.TestIOStreams()
	opts := hookOnlyOptions(ios, dir, "claude-code", "opencode")
	require.NoError(t, runSetup(t.Context(), opts))
	require.FileExists(t, filepath.Join(plugins, opencodeplugin.FileName))

	require.NoError(t, runUninstall(t.Context(), opts))

	assert.NoFileExists(t, filepath.Join(plugins, opencodeplugin.FileName))
	assert.FileExists(t, other, "other OpenCode plugins are kept")
	assert.Contains(t, stdout.String(), "OpenCode plugin removed from ~/.config/opencode/plugins/glab-govern.js")
	_, recorded, err := fallbacksync.RecordedHookAgents()
	require.NoError(t, err)
	assert.False(t, recorded, "uninstall forgets the chosen agents")
}

func TestRunUninstall_OnlyOpenCodePlugin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
	plugin := filepath.Join(dir, ".config", "opencode", "plugins", opencodeplugin.FileName)
	_, err := opencodeplugin.Install(plugin)
	require.NoError(t, err)

	ios, _, stdout, _ := cmdtest.TestIOStreams()
	require.NoError(t, runUninstall(t.Context(), hookOnlyOptions(ios, dir)))

	assert.NoFileExists(t, plugin)
	assert.Contains(t, stdout.String(), "OpenCode plugin removed from ~/.config/opencode/plugins/glab-govern.js")
}

func TestRunSetup_UpdatesOutdatedOpenCodePlugin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
	plugin := filepath.Join(dir, ".config", "opencode", "plugins", opencodeplugin.FileName)
	require.NoError(t, os.MkdirAll(filepath.Dir(plugin), 0o755))
	require.NoError(t, os.WriteFile(plugin, []byte("// Installed by glab govern setup.\n// an older version\n"), 0o644))

	ios, _, _, _ := cmdtest.TestIOStreams()
	require.NoError(t, runSetup(t.Context(), hookOnlyOptions(ios, dir)))

	data, err := os.ReadFile(plugin)
	require.NoError(t, err)
	assert.Equal(t, opencodeplugin.Source, data, "a plain setup keeps and updates a plugin glab installed")
}

func TestRunSetup_NoOpenCodePluginOnWindows(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))

	ios, _, _, _ := cmdtest.TestIOStreams()
	opts := hookOnlyOptions(ios, dir, "opencode")
	opts.goos = "windows"
	err := runSetup(t.Context(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not supported on Windows")
}

func TestRunUninstall_KeepsForeignOpenCodePlugin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
	plugin := filepath.Join(dir, ".config", "opencode", "plugins", opencodeplugin.FileName)
	require.NoError(t, os.MkdirAll(filepath.Dir(plugin), 0o755))
	require.NoError(t, os.WriteFile(plugin, []byte("// the user's own plugin\n"), 0o644))

	ios, _, stdout, _ := cmdtest.TestIOStreams()
	require.NoError(t, runUninstall(t.Context(), hookOnlyOptions(ios, dir)))

	assert.FileExists(t, plugin)
	assert.Contains(t, stdout.String(), "Nothing to uninstall")
}

func TestRunSetup_DoesNotAddClaudeCodeForCodexOnlySetup(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
	// An older glab set up Codex, and the user then removed the Claude Code hooks.
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"codex"}))

	hookAgents, discovered, err := chosenAgents(hookOnlyOptions(nil, dir))
	require.NoError(t, err)
	assert.Empty(t, hookAgents)
	assert.Equal(t, []string{"codex"}, discovered)

	ios, _, _, _ := cmdtest.TestIOStreams()
	err = runSetup(t.Context(), hookOnlyOptions(ios, dir))
	require.Error(t, err, "with --no-fallback-sync there is nothing to set up")
	assert.Contains(t, err.Error(), "codex sessions are only synced by the fallback periodic sync")
}

func TestRunSetup_DiscoveredAgentsAdd(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))

	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"codex"}))
	opts := hookOnlyOptions(nil, dir, "cursor", "opencode", "opencode")
	hookAgents, discovered, err := chosenAgents(opts)
	require.NoError(t, err)
	assert.Equal(t, []string{"codex", "cursor"}, discovered)
	assert.Equal(t, []string{"opencode"}, hookAgents, "an agent passed twice is set up once")
}

func TestRunSetup_UnreadableAgentsSuggestsUninstall(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
	path := filepath.Join(dir, "glab", "gaig", "hook-agents.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))

	for _, agents := range [][]string{nil, {"opencode"}} {
		ios, _, _, _ := cmdtest.TestIOStreams()
		err := runSetup(t.Context(), hookOnlyOptions(ios, dir, agents...))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "run 'glab minder setup --uninstall' to start again")
	}
}

func TestRunSetup_UnreadableClaudeSettingsIsNotRecorded(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
	settings := filepath.Join(dir, ".claude", "settings.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(settings), 0o755))
	require.NoError(t, os.WriteFile(settings, []byte("{not json"), 0o644))

	ios, _, _, _ := cmdtest.TestIOStreams()
	err := runSetup(t.Context(), hookOnlyOptions(ios, dir, "opencode"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), settings)
	assert.Contains(t, err.Error(), "fix it, then run 'glab minder setup' again")
	assert.NotContains(t, err.Error(), "--uninstall", "uninstalling doesn't change the settings file, so it doesn't help")
	_, recorded, err := fallbacksync.RecordedHookAgents()
	require.NoError(t, err)
	assert.False(t, recorded, "Claude Code must not be recorded as not set up when its settings cannot be read")
}

func TestRunSetup_ClaudeCodeOnWindowsFailsBeforePrompt(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))

	// Not a terminal and no --yes, so reaching the prompt would fail differently.
	ios, _, _, _ := cmdtest.TestIOStreams()
	opts := hookOnlyOptions(ios, dir)
	opts.yes = false
	opts.goos = "windows"
	err := runSetup(t.Context(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hook installation is not supported on Windows")
}

func TestRunSetup_DiscoveredAgentsWithoutJobFailBeforePrompt(t *testing.T) {
	tests := map[string]struct {
		goos     string
		agents   []string
		recorded []string
	}{
		"Windows":                  {goos: "windows", agents: []string{"codex", "cursor"}},
		"FreeBSD":                  {goos: "freebsd", agents: []string{"codex", "cursor"}},
		"rerun with recorded list": {goos: "windows", recorded: []string{"codex", "cursor"}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("HOME", dir)
			t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
			if tc.recorded != nil {
				require.NoError(t, fallbacksync.SetDiscoveredAgents(tc.recorded))
			}

			// Not a terminal and no --yes, so reaching the prompt would fail differently.
			ios, _, _, _ := cmdtest.TestIOStreams()
			opts := hookOnlyOptions(ios, dir, tc.agents...)
			opts.yes = false
			opts.noFallbackSync = false
			opts.goos = tc.goos
			err := runSetup(t.Context(), opts)
			require.ErrorIs(t, err, fallbacksync.ErrUnsupportedOS)
			assert.Contains(t, err.Error(), "codex and cursor sessions need the fallback periodic sync: fallback periodic sync is supported only on macOS and Linux")
		})
	}
}

func TestSetupPrompt_JobOnly(t *testing.T) {
	t.Parallel()

	assert.Contains(t, setupPrompt("linux", false, plan{discovered: []string{"codex"}}),
		"glab will install a systemd user timer (~/.config/systemd/user/glab-govern-audit-sync.timer) that syncs sessions")
}

func TestUploadScopePrompt(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		scope uploadScope
		want  string
	}{
		"first setup": {
			scope: uploadScope{fromNow: []string{"claude-code", "codex"}},
			want:  ". Only activity from now on will be uploaded, not earlier sessions or the earlier turns of sessions you resume",
		},
		"setup again": {
			scope: uploadScope{fromEarlier: []string{"claude-code"}},
			want:  ". Only activity from when you first set up each agent will be uploaded, not earlier sessions or the earlier turns of sessions you resume",
		},
		"history": {
			scope: uploadScope{history: []string{"claude-code", "cursor"}},
			want:  ". Activity from the last 89 days and from now on will be uploaded, including earlier sessions and the earlier turns of sessions you resume",
		},
		"agent added to an earlier setup": {
			scope: uploadScope{fromNow: []string{"codex"}, fromEarlier: []string{"claude-code"}},
			want: ". For codex, only activity from now on will be uploaded, not earlier sessions or the earlier turns of sessions you resume" +
				". For claude-code, only activity from when you first set it up will be uploaded, not earlier sessions or the earlier turns of sessions you resume",
		},
		"agents added with history to an earlier setup": {
			scope: uploadScope{fromEarlier: []string{"claude-code", "opencode"}, history: []string{"codex", "cursor"}},
			want: ". For claude-code and opencode, only activity from when you first set them up will be uploaded, not earlier sessions or the earlier turns of sessions you resume" +
				". For codex and cursor, activity from the last 89 days and from now on will be uploaded, including earlier sessions and the earlier turns of sessions you resume",
		},
		"all three": {
			scope: uploadScope{fromNow: []string{"codex"}, fromEarlier: []string{"claude-code"}, history: []string{"opencode"}},
			want: ". For codex, only activity from now on will be uploaded, not earlier sessions or the earlier turns of sessions you resume" +
				". For claude-code, only activity from when you first set it up will be uploaded, not earlier sessions or the earlier turns of sessions you resume" +
				". For opencode, activity from the last 89 days and from now on will be uploaded, including earlier sessions and the earlier turns of sessions you resume",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.scope.prompt())
		})
	}
}

func TestPlanUploads(t *testing.T) {
	t.Parallel()

	earlier := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

	tests := map[string]struct {
		recorded    fallbacksync.AgentSetups
		agents      []string
		history     bool
		wantUploads uploadScope
		wantSetups  fallbacksync.AgentSetups
	}{
		"first setup": {
			agents:      []string{"claude-code", "codex"},
			wantUploads: uploadScope{fromNow: []string{"claude-code", "codex"}},
			wantSetups:  fallbacksync.AgentSetups{"claude-code": {SetUpAt: now}, "codex": {SetUpAt: now}},
		},
		"first setup with history": {
			agents:      []string{"claude-code"},
			history:     true,
			wantUploads: uploadScope{history: []string{"claude-code"}},
			wantSetups:  fallbacksync.AgentSetups{"claude-code": {SetUpAt: now, History: true}},
		},
		"setup again keeps when an agent was first set up": {
			recorded:    fallbacksync.AgentSetups{"claude-code": {SetUpAt: earlier}},
			agents:      []string{"claude-code", "cursor"},
			wantUploads: uploadScope{fromNow: []string{"cursor"}, fromEarlier: []string{"claude-code"}},
			wantSetups:  fallbacksync.AgentSetups{"claude-code": {SetUpAt: earlier}, "cursor": {SetUpAt: now}},
		},
		"history opted in to earlier is kept": {
			recorded:    fallbacksync.AgentSetups{"claude-code": {SetUpAt: earlier, History: true}},
			agents:      []string{"claude-code"},
			wantUploads: uploadScope{history: []string{"claude-code"}},
			wantSetups:  fallbacksync.AgentSetups{"claude-code": {SetUpAt: earlier, History: true}},
		},
		"history applies only to agents set up for the first time": {
			recorded:    fallbacksync.AgentSetups{"claude-code": {SetUpAt: earlier}},
			agents:      []string{"claude-code", "codex"},
			history:     true,
			wantUploads: uploadScope{fromEarlier: []string{"claude-code"}, history: []string{"codex"}},
			wantSetups:  fallbacksync.AgentSetups{"claude-code": {SetUpAt: earlier}, "codex": {SetUpAt: now, History: true}},
		},
		"agents this setup does not sync are kept": {
			recorded:    fallbacksync.AgentSetups{"codex": {SetUpAt: earlier}},
			agents:      []string{"claude-code"},
			wantUploads: uploadScope{fromNow: []string{"claude-code"}},
			wantSetups:  fallbacksync.AgentSetups{"claude-code": {SetUpAt: now}, "codex": {SetUpAt: earlier}},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			uploads := planUploads(tc.recorded, tc.agents, tc.history)
			assert.Equal(t, tc.wantUploads, uploads)
			assert.Equal(t, tc.wantSetups, uploads.setups(tc.recorded, now))
		})
	}
}

func TestSetup_IncludeHistoryOnARerun(t *testing.T) {
	earlier := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
	require.NoError(t, fallbacksync.SetAgentSetups(fallbacksync.AgentSetups{"claude-code": {SetUpAt: earlier}}))

	ios, _, _, _ := cmdtest.TestIOStreams()
	opts := hookOnlyOptions(ios, dir)
	opts.includeHistory = true
	err := runSetup(t.Context(), opts)
	require.ErrorContains(t, err, "--include-history applies only to agents set up for the first time, and every agent this setup syncs (claude-code) is set up already")
	assert.Contains(t, err.Error(), "run 'glab minder setup --uninstall'")
	assert.NoFileExists(t, filepath.Join(dir, ".claude", "settings.json"), "nothing is installed")

	ios, _, _, _ = cmdtest.TestIOStreams()
	opts = hookOnlyOptions(ios, dir, "opencode")
	opts.includeHistory = true
	require.NoError(t, runSetup(t.Context(), opts))
	setups, err := fallbacksync.ReadAgentSetups()
	require.NoError(t, err)
	assert.Equal(t, fallbacksync.AgentSetup{SetUpAt: earlier}, setups["claude-code"], "an agent set up earlier keeps what it uploads")
	assert.True(t, setups["opencode"].History)
}

func TestSetup_RecordsAgentSetups(t *testing.T) {
	earlier := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)

	tests := map[string]struct {
		recorded fallbacksync.AgentSetups
		// olderGlab installs the Claude Code hooks without recording which
		// agents are set up, as glab versions before setups were recorded did.
		olderGlab bool
		args      string
		// want lists the agents and whether they upload history. Agents with
		// a time in wantEarlier keep it, and the others are set up now.
		want        map[string]bool
		wantEarlier map[string]time.Time
	}{
		"first setup": {
			args: "",
			want: map[string]bool{"claude-code": false},
		},
		"history": {
			args: "--include-history",
			want: map[string]bool{"claude-code": true},
		},
		"discovered agents": {
			args: "--agents codex,cursor",
			want: map[string]bool{"codex": false, "cursor": false},
		},
		"setup again keeps when an agent was first set up": {
			recorded:    fallbacksync.AgentSetups{"claude-code": {SetUpAt: earlier}},
			args:        "--agents opencode",
			want:        map[string]bool{"claude-code": false, "opencode": false},
			wantEarlier: map[string]time.Time{"claude-code": earlier},
		},
		"set up with an older glab": {
			olderGlab: true,
			args:      "",
			want:      map[string]bool{"claude-code": false},
		},
		"without the fallback sync, discovered agents are not set up": {
			recorded:    fallbacksync.AgentSetups{"codex": {SetUpAt: earlier}},
			args:        "--no-fallback-sync --agents claude-code",
			want:        map[string]bool{"claude-code": false, "codex": false},
			wantEarlier: map[string]time.Time{"codex": earlier},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("HOME", dir)
			t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
			if tc.recorded != nil {
				require.NoError(t, fallbacksync.SetAgentSetups(tc.recorded))
			}
			if tc.olderGlab {
				require.NoError(t, installClaudeHooks(&options{}))
			}
			mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
			mExec.EXPECT().LookPath("glab").Return("", errors.New("not found")).AnyTimes()
			mExec.EXPECT().ExecWithCombinedOutput(gomock.Any(), gomock.Any(), gomock.Any(), nil).Return(nil, nil).AnyTimes()
			exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithExecutor(mExec))

			before := time.Now()
			_, err := exec(strings.TrimSpace("--yes " + tc.args))
			require.NoError(t, err)
			after := time.Now()

			setups, err := fallbacksync.ReadAgentSetups()
			require.NoError(t, err)
			require.Len(t, setups, len(tc.want))
			for agent, history := range tc.want {
				setup, ok := setups[agent]
				require.True(t, ok, agent)
				assert.Equal(t, history, setup.History, agent)
				if at, ok := tc.wantEarlier[agent]; ok {
					assert.True(t, at.Equal(setup.SetUpAt), "%s keeps when it was first set up", agent)
					continue
				}
				assert.False(t, setup.SetUpAt.Before(before) || setup.SetUpAt.After(after), "%s is set up now, at %s", agent, setup.SetUpAt)
			}
		})
	}
}

func TestSetup_CancelledRecordsNoAgentSetups(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))

	// Not a terminal and no --yes, so setup stops at the prompt.
	ios, _, _, _ := cmdtest.TestIOStreams()
	require.Error(t, runSetup(t.Context(), &options{io: ios, goos: "linux", includeHistory: true, settingsPathOverride: filepath.Join(dir, ".claude", "settings.json")}))

	setups, err := fallbacksync.ReadAgentSetups()
	require.NoError(t, err)
	assert.Empty(t, setups)
}

func TestSetup_UnreadableAgentSetupsSuggestsUninstall(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
	path := filepath.Join(dir, "glab", "gaig", "agent-setups.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))

	ios, _, _, _ := cmdtest.TestIOStreams()
	err := runSetup(t.Context(), hookOnlyOptions(ios, dir))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "run 'glab minder setup --uninstall' to start again")
	assert.NoFileExists(t, filepath.Join(dir, ".claude", "settings.json"), "nothing is installed")
}

func TestSetup_IncludeHistoryConflictsWithUninstall(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
	_, err := exec("--uninstall --include-history")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "none of the others can be")
}

// cursorTurn is a line of a Cursor transcript.
const cursorTurn = `{"role":"user"}` + "\n"

// writeCursorTranscript writes a Cursor transcript with one turn under home,
// and returns its path.
func writeCursorTranscript(t *testing.T, home, id string) string {
	t.Helper()
	path := filepath.Join(home, ".cursor", "projects", "work-repo", "agent-transcripts", id, id+".jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(cursorTurn), 0o644))
	return path
}

func assertSyncStartsAt(t *testing.T, sessionID string, want int64) {
	t.Helper()
	cursor, err := syncstate.ReadPosition(sessionID)
	require.NoError(t, err)
	assert.Equal(t, want, cursor)
	uploaded, err := syncstate.HasUploads(sessionID, cursor)
	require.NoError(t, err)
	assert.False(t, uploaded)
}

func TestSetup_RecordsWhereCursorSessionsEnd(t *testing.T) {
	tests := map[string]struct {
		runs []string
		// want is where syncing starts, or 0 when setup records nothing.
		want int64
	}{
		"first setup":               {runs: []string{"--agents cursor"}, want: int64(len(cursorTurn))},
		"setup again":               {runs: []string{"--agents cursor", "", "--agents codex"}, want: int64(len(cursorTurn))},
		"history":                   {runs: []string{"--agents cursor --include-history"}},
		"history, then setup again": {runs: []string{"--agents cursor --include-history", ""}},
		"added to an earlier setup": {runs: []string{"", "--agents cursor"}, want: int64(2 * len(cursorTurn))},
		"without the fallback sync": {runs: []string{"--no-fallback-sync"}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("HOME", dir)
			t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
			path := writeCursorTranscript(t, dir, "conv-1")

			for i, args := range tc.runs {
				mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
				mExec.EXPECT().LookPath("glab").Return("", errors.New("not found")).AnyTimes()
				mExec.EXPECT().ExecWithCombinedOutput(gomock.Any(), gomock.Any(), gomock.Any(), nil).Return(nil, nil).AnyTimes()
				exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithExecutor(mExec))
				_, err := exec(strings.TrimSpace("--yes " + args))
				require.NoError(t, err)
				if i == 0 {
					// Turns added after the first setup are uploaded, so a
					// later setup must not skip them.
					f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
					require.NoError(t, err)
					_, err = f.WriteString(cursorTurn)
					require.NoError(t, errors.Join(err, f.Close()))
				}
			}

			if tc.want == 0 {
				cp, err := syncstate.PositionPath("conv-1")
				require.NoError(t, err)
				assert.NoFileExists(t, cp, "the whole transcript may be uploaded")
				return
			}
			assertSyncStartsAt(t, "conv-1", tc.want)
		})
	}
}

func TestRunUninstall_RemovesWhereCursorSessionsEnd(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
	writeCursorTranscript(t, dir, "conv-1")

	ios, _, _, _ := cmdtest.TestIOStreams()
	opts := &options{
		io:                   ios,
		executor:             expectLinuxInstall(t, "/usr/bin/glab"),
		buildInfo:            testBuild,
		goos:                 "linux",
		yes:                  true,
		agents:               []string{"cursor"},
		agentsChanged:        true,
		settingsPathOverride: filepath.Join(dir, ".claude", "settings.json"),
	}
	require.NoError(t, runSetup(t.Context(), opts))
	cp, err := syncstate.PositionPath("conv-1")
	require.NoError(t, err)
	require.FileExists(t, cp)

	mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
	mExec.EXPECT().ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "disable", "--now", "glab-govern-audit-sync.timer"}, nil).Return(nil, nil)
	mExec.EXPECT().ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "daemon-reload"}, nil).Return(nil, nil)
	opts.executor = mExec
	opts.agents, opts.agentsChanged = nil, false

	require.NoError(t, runUninstall(t.Context(), opts))

	assert.NoFileExists(t, cp)
	start, err := syncstate.StartPath("conv-1")
	require.NoError(t, err)
	assert.NoFileExists(t, start)
}

// setUpCursor runs setup for Cursor, with args added.
func setUpCursor(t *testing.T, args string) error {
	t.Helper()
	mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
	mExec.EXPECT().LookPath("glab").Return("", errors.New("not found")).AnyTimes()
	mExec.EXPECT().ExecWithCombinedOutput(gomock.Any(), gomock.Any(), gomock.Any(), nil).Return(nil, nil).AnyTimes()
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithExecutor(mExec))
	_, err := exec(strings.TrimSpace("--yes --agents cursor " + args))
	return err
}

func TestSetup_HistoryRecordsWhereOnlyOldCursorSessionsEnd(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
	recent := writeCursorTranscript(t, dir, "conv-recent")
	old := writeCursorTranscript(t, dir, "conv-old")
	long := time.Now().Add(-100 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(old, long, long))

	require.NoError(t, setUpCursor(t, "--include-history"))

	cp, err := syncstate.PositionPath("conv-recent")
	require.NoError(t, err)
	assert.NoFileExists(t, cp, "the history of %s is uploaded", recent)
	assertSyncStartsAt(t, "conv-old", int64(len(cursorTurn)))
}

func TestSetup_FollowsSymlinkedCursorWorkspaces(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
	elsewhere := filepath.Join(t.TempDir(), "workspace")
	path := filepath.Join(elsewhere, "agent-transcripts", "conv-1", "conv-1.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(cursorTurn), 0o644))
	projects := filepath.Join(dir, ".cursor", "projects")
	require.NoError(t, os.MkdirAll(projects, 0o755))
	require.NoError(t, os.Symlink(elsewhere, filepath.Join(projects, "work-repo")))

	require.NoError(t, setUpCursor(t, ""))

	assertSyncStartsAt(t, "conv-1", int64(len(cursorTurn)))
}

func TestSetup_UnreadableCursorWorkspaceFails(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory that can't be listed")
	}
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GLAB_CONFIG_DIR", filepath.Join(dir, "glab"))
	path := writeCursorTranscript(t, dir, "conv-1")
	transcripts := filepath.Dir(filepath.Dir(path))
	require.NoError(t, os.Chmod(transcripts, 0o100))
	t.Cleanup(func() { _ = os.Chmod(transcripts, 0o755) })

	err := setUpCursor(t, "")
	require.ErrorContains(t, err, "failed to record how far your Cursor sessions go")

	setups, err := fallbacksync.ReadAgentSetups()
	require.NoError(t, err)
	assert.Empty(t, setups, "Cursor is not set up, so a later setup tries again")
	agents, err := fallbacksync.DiscoveredAgents()
	require.NoError(t, err)
	assert.Empty(t, agents, "Cursor sessions are not synced")
}
