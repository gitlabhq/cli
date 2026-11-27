//go:build !integration

package setup

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.sr.ht/~timofurrer/ugh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/claudehooks"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// expectNoLinuxJob returns an executor that reports no loaded fallback job
// and, if asked, /usr/bin/glab on PATH.
func expectNoLinuxJob(t *testing.T) *cmdtest.MockExecutor {
	t.Helper()
	mExec := cmdtest.NewMockExecutor(gomock.NewController(t))
	mExec.EXPECT().
		ExecWithCombinedOutput(gomock.Any(), "systemctl", []string{"--user", "is-active", "--quiet", "glab-govern-audit-sync.timer"}, nil).
		Return(nil, &exec.ExitError{}).AnyTimes()
	mExec.EXPECT().LookPath("glab").Return("/usr/bin/glab", nil).AnyTimes()
	mExec.EXPECT().
		ExecWithIO(gomock.Any(), "/usr/bin/glab", []string{"version"}, nil, nil, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _, _ []string, _ io.Reader, stdout, _ io.Writer) error {
			_, err := io.WriteString(stdout, "glab 1.121.0 (abc1234)\n")
			return err
		}).AnyTimes()
	return mExec
}

func TestRunSetup_NamesTheSettingsFileUnderClaudeConfigDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude-work"))

	ios, _, stdout, _ := cmdtest.TestIOStreams()
	require.NoError(t, runSetup(t.Context(), &options{io: ios, executor: expectNoLinuxJob(t), buildInfo: testBuild, goos: "linux", yes: true, noFallbackSync: true}))
	assert.FileExists(t, filepath.Join(dir, "claude-work", "settings.json"))
	assert.Contains(t, stdout.String(), "Hooks installed in ~/claude-work/settings.json")
	assert.NotContains(t, stdout.String(), ".claude/settings.json")

	stdout.Reset()
	require.NoError(t, runUninstall(t.Context(), &options{io: ios, executor: expectNoLinuxJob(t), goos: "linux", yes: true}))
	assert.Contains(t, stdout.String(), "Hooks removed from ~/claude-work/settings.json")
	assert.NotContains(t, stdout.String(), ".claude/settings.json")
}

// The prompt is the consent step, so it names the file that is edited.
func TestRunSetupAndUninstall_PromptsNameTheSettingsFileUnderClaudeConfigDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude-work"))
	settings := filepath.Join(dir, "claude-work", "settings.json")

	// Without the expected prompt, the deadline ends the wait for an answer.
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	c := ugh.New(t)
	c.Expect(ugh.Confirm("agent session hooks in ~/claude-work/settings.json")).Do(ugh.Reject)
	ios, cleanup := cmdtest.TestIOStreamsWithConsole(t, c)
	t.Cleanup(cleanup)
	require.NoError(t, runSetup(ctx, &options{io: ios, executor: expectNoLinuxJob(t), buildInfo: testBuild, goos: "linux", noFallbackSync: true}))
	assert.NoFileExists(t, settings, "declining installs nothing")

	plain, _, _, _ := cmdtest.TestIOStreams()
	require.NoError(t, runSetup(t.Context(), &options{io: plain, executor: expectNoLinuxJob(t), buildInfo: testBuild, goos: "linux", yes: true, noFallbackSync: true}))
	before, err := os.ReadFile(settings)
	require.NoError(t, err)

	c = ugh.New(t)
	c.Expect(ugh.Confirm("hooks from ~/claude-work/settings.json")).Do(ugh.Reject)
	ios, cleanup = cmdtest.TestIOStreamsWithConsole(t, c)
	t.Cleanup(cleanup)
	require.NoError(t, runUninstall(ctx, &options{io: ios, executor: expectNoLinuxJob(t), goos: "linux", uninstall: true}))
	after, err := os.ReadFile(settings)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "declining changes nothing")
}

func TestRunUninstall_CorruptSettingsUnderClaudeConfigDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude-work"))
	path := filepath.Join(dir, "claude-work", "settings.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o644))

	ios, _, _, _ := cmdtest.TestIOStreams()
	err := runUninstall(t.Context(), &options{io: ios, executor: expectNoLinuxJob(t), goos: "linux"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fix ~/claude-work/settings.json and run")
}

func TestSetupAndUninstall_MatchGlabHooksByTheirArguments(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	path := filepath.Join(dir, ".claude", "settings.json")
	stop := "/opt/glab/bin/glab " + claudehooks.StopHookArgs
	sessionEnd := "'/Users/me/My Tools/glab' " + claudehooks.SessionEndHookArgs
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"`+stop+`"}]}],"SessionEnd":[{"hooks":[{"type":"command","command":"`+sessionEnd+`"}]}]}}`), 0o644))

	ios, _, _, _ := cmdtest.TestIOStreams()
	opts := &options{io: ios, executor: expectNoLinuxJob(t), goos: "linux", yes: true, noFallbackSync: true, settingsPathOverride: path}
	present, err := claudeHooksInstalled(opts)
	require.NoError(t, err)
	assert.True(t, present, "doctor counts these hooks, so setup and uninstall do too")

	require.NoError(t, installClaudeHooks(opts))
	raw, err := claudehooks.LoadRawSettings(path)
	require.NoError(t, err)
	hooks, err := claudehooks.HooksFromRaw(raw)
	require.NoError(t, err)
	require.Len(t, hooks["Stop"], 1, "no second Stop hook is added")
	require.Len(t, hooks["SessionEnd"], 1, "no second SessionEnd hook is added")

	require.NoError(t, runUninstall(t.Context(), opts))
	raw, err = claudehooks.LoadRawSettings(path)
	require.NoError(t, err)
	assert.NotContains(t, raw, "hooks", "uninstall removes hooks that run glab by another path")
}

func TestSetupAndUninstall_EveryInstalledForm(t *testing.T) {
	legacy := func(args string) string { return strings.Replace(args, "minder ", "govern ", 1) }
	forms := map[string]func(args string) string{
		"relative glab govern": func(args string) string { return "glab " + legacy(args) },
		"relative glab minder": func(args string) string { return "glab " + args },
		"absolute glab govern": func(args string) string { return "/x/y/glab " + legacy(args) },
		"absolute glab minder": func(args string) string { return "/x/y/glab " + args },
	}
	all := func(args string) []string {
		var commands []string
		for _, form := range forms {
			commands = append(commands, form(args))
		}
		return commands
	}
	cases := map[string]func(args string) []string{"every form at once": all}
	for name, form := range forms {
		cases[name] = func(args string) []string { return []string{form(args)} }
	}

	for name, commands := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("HOME", dir)
			path := filepath.Join(dir, ".claude", "settings.json")
			hooks := map[string][]claudehooks.HookGroup{}
			claudehooks.AddHook(hooks, "Stop", "echo user-hook")
			for _, c := range commands(claudehooks.StopHookArgs) {
				claudehooks.AddHook(hooks, "Stop", c)
			}
			for _, c := range commands(claudehooks.SessionEndHookArgs) {
				claudehooks.AddHook(hooks, "SessionEnd", c)
			}
			hooksJSON, err := claudehooks.MarshalHooks(hooks)
			require.NoError(t, err)
			require.NoError(t, claudehooks.WriteSettings(path, map[string]json.RawMessage{"hooks": hooksJSON}))

			ios, _, _, _ := cmdtest.TestIOStreams()
			opts := &options{io: ios, executor: expectNoLinuxJob(t), goos: "linux", yes: true, noFallbackSync: true, settingsPathOverride: path}
			require.NoError(t, installClaudeHooks(opts))
			hooks = readHooks(t, path)
			for event, args := range map[string]string{"Stop": claudehooks.StopHookArgs, "SessionEnd": claudehooks.SessionEndHookArgs} {
				var glab []string
				for _, group := range hooks[event] {
					for _, hook := range group.Hooks {
						if hook.Command != "echo user-hook" {
							glab = append(glab, hook.Command)
						}
					}
				}
				require.Len(t, glab, 1, "exactly one %s hook is left: %v", event, glab)
				assert.True(t, claudehooks.MatchesGlabCommand(glab[0], args), "the %s hook runs glab minder: %s", event, glab[0])
			}
			assert.False(t, claudehooks.LegacyHookPresent(hooks))

			require.NoError(t, runUninstall(t.Context(), opts))
			hooks = readHooks(t, path)
			assert.False(t, claudehooks.GlabHookPresent(hooks, "Stop"))
			assert.False(t, claudehooks.GlabHookPresent(hooks, "SessionEnd"))
			assert.True(t, claudehooks.HookPresent(hooks, "Stop", "echo user-hook"), "other hooks are kept")
		})
	}
}

func readHooks(t *testing.T, path string) map[string][]claudehooks.HookGroup {
	t.Helper()
	raw, err := claudehooks.LoadRawSettings(path)
	require.NoError(t, err)
	hooks, err := claudehooks.HooksFromRaw(raw)
	require.NoError(t, err)
	return hooks
}
