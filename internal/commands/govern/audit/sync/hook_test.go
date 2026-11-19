//go:build !integration

package sync

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/fallbacksync"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

type detachedStart struct {
	dir  string
	env  []string
	args []string
}

// runHookWithInput runs the Codex hook with stdin, with Codex enabled, and
// returns what it printed and the glab it started, if any.
func runHookWithInput(t *testing.T, stdin string) (string, *detachedStart) {
	t.Helper()
	t.Setenv("GLAB_CONFIG_DIR", t.TempDir())
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"codex"}))
	return runHookEnabled(t, stdin)
}

// runHookEnabled runs the Codex hook with the agents that setup enabled left as
// they are.
func runHookEnabled(t *testing.T, stdin string) (string, *detachedStart) {
	t.Helper()

	ios, _, stdout, stderr := cmdtest.TestIOStreams()
	ios.In = io.NopCloser(strings.NewReader(stdin))
	var started *detachedStart
	opts := &options{io: ios, hook: "codex", startDetached: func(dir string, env []string, args ...string) error {
		started = &detachedStart{dir: dir, env: env, args: args}
		return nil
	}}

	require.NoError(t, runSync(t.Context(), opts))
	return stdout.String() + stderr.String(), started
}

func TestRunHook_StartsSyncForCodexSession(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "claude-1")
	t.Setenv("OPENCODE_SESSION_ID", "ses_1")

	out, started := runHookWithInput(t, `{"session_id":"th-1","turn_id":"turn-1","transcript_path":"/home/me/.codex/sessions/rollout-th-1.jsonl","cwd":"/work/repo","hook_event_name":"Stop","last_assistant_message":"done"}`)

	assert.Empty(t, out, "Codex reads a Stop hook's output as a reason to continue")
	require.NotNil(t, started)
	assert.Equal(t, "/work/repo", started.dir, "the project comes from the session's directory")
	assert.Equal(t, []string{"minder", "audit", "sync", "--silent"}, started.args)
	assert.Subset(t, started.env, []string{
		"GLAB_MINDER_HOOK_AGENT=codex",
		"GLAB_MINDER_HOOK_SESSION_ID=th-1",
		"GLAB_MINDER_HOOK_TRANSCRIPT=/home/me/.codex/sessions/rollout-th-1.jsonl",
	})
	assert.False(t, slices.ContainsFunc(started.env, func(kv string) bool {
		return strings.HasPrefix(kv, "CLAUDE_CODE_SESSION_ID=") || strings.HasPrefix(kv, "OPENCODE_SESSION_ID=")
	}), "a Claude Code or OpenCode session that started Codex is not synced in its place")
}

func TestRunHook_SkipsAgentThatIsNotEnabled(t *testing.T) {
	t.Setenv("GLAB_CONFIG_DIR", t.TempDir())

	out, started := runHookEnabled(t, `{"session_id":"th-1","transcript_path":"/home/me/.codex/sessions/rollout-th-1.jsonl","cwd":"/work/repo"}`)

	assert.Empty(t, out)
	assert.Nil(t, started, "--all skips the sessions of an agent that is not enabled, so the hook does too")
}

func TestRunHook_CodexDefaultsToHookDirectory(t *testing.T) {
	_, started := runHookWithInput(t, `{"session_id":"th-1","transcript_path":"/rollout-th-1.jsonl"}`)

	require.NotNil(t, started)
	assert.Equal(t, ".", started.dir)
}

func TestRunHook_PassesAnAbsoluteTranscriptPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd, err := os.Getwd()
	require.NoError(t, err)

	for path, want := range map[string]string{
		"~/.codex/sessions/rollout-th-1.jsonl": filepath.Join(home, ".codex", "sessions", "rollout-th-1.jsonl"),
		"rollout-th-1.jsonl":                   filepath.Join(cwd, "rollout-th-1.jsonl"),
	} {
		_, started := runHookWithInput(t, `{"session_id":"th-1","transcript_path":"`+path+`","cwd":"/work/repo"}`)

		require.NotNil(t, started, path)
		assert.Contains(t, started.env, "GLAB_MINDER_HOOK_TRANSCRIPT="+want, "the glab started in the session's directory reopens it")
	}
}

func TestRunHook_IgnoresInputWithoutSession(t *testing.T) {
	tests := map[string]string{
		"not JSON":        "not json",
		"empty":           "",
		"no session ID":   `{"transcript_path":"/rollout.jsonl","cwd":"/work"}`,
		"no transcript":   `{"session_id":"th-1","cwd":"/work"}`,
		"null transcript": `{"session_id":"th-1","transcript_path":null,"cwd":"/work"}`,
	}
	for name, stdin := range tests {
		t.Run(name, func(t *testing.T) {
			out, started := runHookWithInput(t, stdin)

			assert.Empty(t, out)
			assert.Nil(t, started)
		})
	}
}

func TestRunSync_CodexHookSession(t *testing.T) {
	env := newSyncAllEnv(t, true)
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"codex"}))
	path := writeCodexRollout(t, env.home, "th-1", jsonLines(t,
		codexSessionMetaLine("th-1", "/work", ""),
		codexItem(recentTimestamp(), map[string]any{"type": "function_call", "name": "exec_command", "call_id": "call_1"}),
	))
	t.Setenv(hookAgentEnv, "codex")
	t.Setenv(hookSessionEnv, "th-1")
	t.Setenv(hookTranscriptEnv, path)

	out, err := env.exec("--silent -R " + testHost + "/my-group/my-project")
	require.NoError(t, err)

	assert.Empty(t, out.String())
	assert.Equal(t, []string{"th-1"}, env.fake.sessionsCreated)
	assert.Equal(t, []string{"codex"}, env.fake.agentTypes)
	assert.Equal(t, []string{"hook"}, env.fake.syncTypes)
	assert.Equal(t, 1, env.fake.eventsPosted)
	meta, err := readSessionMeta("th-1")
	require.NoError(t, err)
	assert.Equal(t, sessionMeta{AgentType: "codex", PathWithNamespace: "my-group/my-project", Host: testHost, Transcript: path}, *meta)
}

func TestSyncAll_CodexHookSessionIsNotDiscoveredAgain(t *testing.T) {
	env := newSyncAllEnv(t, true)
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"codex"}))
	path := writeCodexRollout(t, env.home, "th-1", jsonLines(t,
		codexSessionMetaLine("th-1", "/work", "git@"+testHost+":my-group/my-project.git"),
		codexItem(recentTimestamp(), map[string]any{"type": "function_call", "name": "exec_command", "call_id": "call_1"}),
	))
	t.Setenv(hookAgentEnv, "codex")
	t.Setenv(hookSessionEnv, "th-1")
	t.Setenv(hookTranscriptEnv, path)
	_, err := env.exec("--silent -R " + testHost + "/my-group/my-project")
	require.NoError(t, err)
	t.Setenv(hookAgentEnv, "")

	_, err = env.exec("--all")
	require.NoError(t, err)

	assert.Equal(t, []string{"th-1"}, env.fake.sessionsCreated)
	assert.Equal(t, 1, env.fake.eventsPosted, "--all sends only what the hook has not")
}

func TestRunSync_CodexHookSessionIDMustMatchRollout(t *testing.T) {
	env := newSyncAllEnv(t, true)
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"codex"}))
	path := writeCodexRollout(t, env.home, "th-1", jsonLines(t, codexSessionMetaLine("th-1", "/work", "")))
	t.Setenv(hookAgentEnv, "codex")
	t.Setenv(hookSessionEnv, "th-other")
	t.Setenv(hookTranscriptEnv, path)

	_, err := env.exec("--silent -R " + testHost + "/my-group/my-project")
	require.Error(t, err)

	assert.Empty(t, env.fake.sessionsCreated)
}

func TestRunSync_CodexHookSkipsSubagentThreads(t *testing.T) {
	env := newSyncAllEnv(t, true)
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"codex"}))
	meta := codexSessionMetaLine("th-child", "/work", "")
	meta["payload"].(map[string]any)["parent_thread_id"] = "th-parent" //nolint:forcetypeassert // built above
	path := writeCodexRollout(t, env.home, "th-child", jsonLines(t, meta))
	t.Setenv(hookAgentEnv, "codex")
	t.Setenv(hookSessionEnv, "th-child")
	t.Setenv(hookTranscriptEnv, path)

	_, err := env.exec("--silent -R my-group/my-project")
	require.Error(t, err)

	assert.Empty(t, env.fake.sessionsCreated)
	_, err = readSessionMeta("th-child")
	require.ErrorIs(t, err, errNoSessionMeta)
}

func TestHookFlagExcludesOtherModes(t *testing.T) {
	t.Parallel()

	for _, other := range []string{"--all", "--complete", "-R my-group/my-project"} {
		exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
		_, err := exec("--hook codex " + other)
		require.Error(t, err, other)
	}
}
