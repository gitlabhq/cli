//go:build !integration

package sync

import (
	"encoding/json"
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

// runHookWithInput runs the hook for agent with stdin, and returns what it
// printed and the glab it started, if any.
func runHookWithInput(t *testing.T, agent, stdin string) (string, *detachedStart) {
	t.Helper()
	t.Setenv("GLAB_CONFIG_DIR", t.TempDir())
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{agent}))
	return runHookEnabled(t, agent, stdin)
}

// runHookEnabled runs agent's hook with the agents that setup enabled left as
// they are.
func runHookEnabled(t *testing.T, agent, stdin string) (string, *detachedStart) {
	t.Helper()

	ios, _, stdout, stderr := cmdtest.TestIOStreams()
	ios.In = io.NopCloser(strings.NewReader(stdin))
	var started *detachedStart
	opts := &options{io: ios, hook: agent, startDetached: func(dir string, env []string, args ...string) error {
		started = &detachedStart{dir: dir, env: env, args: args}
		return nil
	}}

	require.NoError(t, runSync(t.Context(), opts))
	return stdout.String() + stderr.String(), started
}

func TestRunHook_StartsSyncForCodexSession(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "claude-1")
	t.Setenv("OPENCODE_SESSION_ID", "ses_1")

	out, started := runHookWithInput(t, "codex", `{"session_id":"th-1","turn_id":"turn-1","transcript_path":"/home/me/.codex/sessions/rollout-th-1.jsonl","cwd":"/work/repo","hook_event_name":"Stop","last_assistant_message":"done"}`)

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
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"cursor"}))

	out, started := runHookEnabled(t, "codex", `{"session_id":"th-1","transcript_path":"/home/me/.codex/sessions/rollout-th-1.jsonl","cwd":"/work/repo"}`)

	assert.Empty(t, out)
	assert.Nil(t, started, "--all skips the sessions of an agent that is not enabled, so the hook does too")
}

func TestRunHook_StartsSyncForCursorSession(t *testing.T) {
	transcript := "/home/me/.cursor/projects/work-repo/agent-transcripts/conv-1/conv-1.jsonl"
	for _, event := range []string{"stop", "sessionEnd"} {
		t.Run(event, func(t *testing.T) {
			out, started := runHookWithInput(t, "cursor", `{"conversation_id":"conv-1","session_id":"other-id","status":"completed","hook_event_name":"`+event+`","workspace_roots":["/work/docs","/work/repo"],"transcript_path":"`+transcript+`"}`)

			assert.Empty(t, out, "Cursor reads a hook's output as a follow-up message")
			require.NotNil(t, started)
			assert.Equal(t, "/work/repo", started.dir, "the root the transcript's workspace slug names, not the first one")
			assert.Subset(t, started.env, []string{
				"GLAB_MINDER_HOOK_AGENT=cursor",
				"GLAB_MINDER_HOOK_SESSION_ID=conv-1",
				"GLAB_MINDER_HOOK_TRANSCRIPT=" + transcript,
			}, "the session ID matches the one discovery uses")
		})
	}
}

func TestRunHook_SkipsACursorWorkspaceItCannotPlace(t *testing.T) {
	transcript := "/home/me/.cursor/projects/work-both-code-workspace/agent-transcripts/conv-1/conv-1.jsonl"

	out, started := runHookWithInput(t, "cursor", `{"hook_event_name":"stop","workspace_roots":["/work/docs","/work/repo"],"transcript_path":"`+transcript+`"}`)

	assert.Empty(t, out)
	assert.Nil(t, started, "uploading to the wrong root would send the session to another project")
}

func TestCursorWorkspaceRoot(t *testing.T) {
	transcript := func(slug string) string {
		return "/home/me/.cursor/projects/" + slug + "/agent-transcripts/conv-1/conv-1.jsonl"
	}
	tests := []struct {
		name  string
		path  string
		roots []string
		want  string
	}{
		{"the root the slug names", transcript("work-repo"), []string{"/work/docs", "/work/repo"}, "/work/repo"},
		{"a slug with a leading dash", transcript("-work-repo"), []string{"/work/docs", "/work/repo"}, "/work/repo"},
		{"the only root", transcript("work-both-code-workspace"), []string{"/work/repo"}, "/work/repo"},
		{"no root the slug names", transcript("work-both-code-workspace"), []string{"/work/docs", "/work/repo"}, ""},
		{"two roots the slug names", transcript("work-a-b"), []string{"/work/a-b", "/work/a_b"}, ""},
		{"a transcript outside agent-transcripts", "/elsewhere/conv-1.jsonl", []string{"/work/docs", "/work/repo"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, cursorWorkspaceRoot(tt.path, tt.roots))
		})
	}
}

func TestCursorWorkspaceRoot_UsesTheToolCallPaths(t *testing.T) {
	work := t.TempDir()
	docs, repo := filepath.Join(work, "docs"), filepath.Join(work, "repo")
	roots := []string{docs, repo}
	tests := []struct {
		name  string
		slug  string
		reads []string
		want  string
	}{
		{"a workspace file slug", "work-both-code-workspace", []string{filepath.Join(repo, "main.go")}, repo},
		{"a slug the tool calls contradict", nonAlphanumeric.ReplaceAllString(docs, "-"), []string{filepath.Join(repo, "main.go")}, ""},
		{"a slug the tool calls confirm", nonAlphanumeric.ReplaceAllString(docs, "-"), []string{filepath.Join(docs, "a.md"), filepath.Join(repo, "main.go")}, docs},
		{"both roots used, the slug decides", nonAlphanumeric.ReplaceAllString(repo, "-"), []string{filepath.Join(docs, "a.md"), filepath.Join(repo, "main.go")}, repo},
		{"both roots used and no slug match", "work-both-code-workspace", []string{filepath.Join(docs, "a.md"), filepath.Join(repo, "main.go")}, ""},
		{"only paths outside the roots", nonAlphanumeric.ReplaceAllString(repo, "-"), []string{"/tmp/scratch.txt"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "projects", tt.slug, "agent-transcripts", "conv-1")
			require.NoError(t, os.MkdirAll(dir, 0o755))
			var lines []byte
			for _, p := range tt.reads {
				line, err := json.Marshal(map[string]any{"role": "assistant", "message": map[string]any{"content": []any{
					map[string]any{"type": "tool_use", "name": "Read", "input": map[string]any{"path": p}},
				}}})
				require.NoError(t, err)
				lines = append(append(lines, line...), '\n')
			}
			path := filepath.Join(dir, "conv-1.jsonl")
			require.NoError(t, os.WriteFile(path, lines, 0o600))

			assert.Equal(t, tt.want, cursorWorkspaceRoot(path, roots))
		})
	}
}

func TestRunHook_CodexDefaultsToHookDirectory(t *testing.T) {
	_, started := runHookWithInput(t, "codex", `{"session_id":"th-1","transcript_path":"/rollout-th-1.jsonl"}`)

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
		_, started := runHookWithInput(t, "codex", `{"session_id":"th-1","transcript_path":"`+path+`","cwd":"/work/repo"}`)

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
			out, started := runHookWithInput(t, "codex", stdin)

			assert.Empty(t, out)
			assert.Nil(t, started)
		})
	}

	cursorTests := map[string]string{
		"aborted turn with no transcript": `{"conversation_id":"conv-1","status":"aborted","transcript_path":null,"workspace_roots":["/work/repo"]}`,
		"no workspace":                    `{"conversation_id":"conv-1","transcript_path":"/t/conv-1/conv-1.jsonl","workspace_roots":[]}`,
	}
	for name, stdin := range cursorTests {
		t.Run(name, func(t *testing.T) {
			out, started := runHookWithInput(t, "cursor", stdin)

			assert.Empty(t, out)
			assert.Nil(t, started)
		})
	}
}

// chdirToSessionFolder moves to a new folder, as the glab a Codex or Cursor
// hook starts runs in the session's folder, and returns the folder.
func chdirToSessionFolder(t *testing.T) string {
	t.Helper()
	t.Chdir(t.TempDir())
	dir, err := currentCWD()
	require.NoError(t, err)
	return dir
}

func TestRunSync_CodexHookSession(t *testing.T) {
	env := newSyncAllEnv(t, true)
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"codex"}))
	dir := chdirToSessionFolder(t)
	path := writeCodexRollout(t, env.home, "th-1", jsonLines(t,
		codexSessionMetaLine("th-1", dir, ""),
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
	project := projectRef{Host: testHost, PathWithNamespace: "my-group/my-project"}
	assert.Equal(t, sessionMeta{
		AgentType: "codex", PathWithNamespace: "my-group/my-project", Host: testHost, Transcript: path,
		Parts:        []sessionPart{{projectRef: project, Key: "th-1-42", ProjectID: 42, SessionID: 99}},
		FirstProject: project,
		Folders:      map[string]projectRef{dir: project},
		PendingCalls: map[string]projectRef{"call_1": project},
	}, *meta, "a call without its result is remembered, so its result goes to the same project")
	require.Len(t, env.fake.sessionRequests, 1)
	assert.Equal(t, "th-1-42", env.fake.sessionRequests[0].IdempotencyKey)
	assert.Equal(t, "th-1", env.fake.sessionRequests[0].ParentIdempotencyKey)
}

func TestSyncAll_CodexHookSessionIsNotDiscoveredAgain(t *testing.T) {
	env := newSyncAllEnv(t, true)
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"codex"}))
	dir := chdirToSessionFolder(t)
	path := writeCodexRollout(t, env.home, "th-1", jsonLines(t,
		codexSessionMetaLine("th-1", dir, "git@"+testHost+":my-group/my-project.git"),
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

// writeCursorTranscript writes a Cursor transcript with one tool call, and
// returns its path.
func writeCursorTranscript(t *testing.T, home, id string) string {
	t.Helper()
	path := filepath.Join(home, ".cursor", "projects", "work-repo", "agent-transcripts", id, id+".jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(jsonLines(t,
		map[string]any{"role": "user", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "<user_query>list the files</user_query>"}}}},
		map[string]any{"role": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "name": "Shell"}}}},
	)), 0o644))
	return path
}

func TestRunSync_CursorHookSession(t *testing.T) {
	env := newSyncAllEnv(t, true)
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"cursor"}))
	dir := chdirToSessionFolder(t)
	path := writeCursorTranscript(t, env.home, "conv-1")
	t.Setenv(hookAgentEnv, "cursor")
	t.Setenv(hookSessionEnv, "conv-1")
	t.Setenv(hookTranscriptEnv, path)

	out, err := env.exec("--silent -R " + testHost + "/my-group/my-project")
	require.NoError(t, err)

	assert.Empty(t, out.String())
	assert.Equal(t, []string{"conv-1"}, env.fake.sessionsCreated)
	assert.Equal(t, []string{"cursor"}, env.fake.agentTypes)
	assert.Equal(t, []string{"hook"}, env.fake.syncTypes)
	assert.Equal(t, 2, env.fake.eventsPosted)
	assert.Zero(t, env.fake.completed, "sessionEnd fires after every cursor-agent -p run, so the session stays open")
	meta, err := readSessionMeta("conv-1")
	require.NoError(t, err)
	project := projectRef{Host: testHost, PathWithNamespace: "my-group/my-project"}
	assert.Equal(t, sessionMeta{
		AgentType: "cursor", PathWithNamespace: "my-group/my-project", Host: testHost, Transcript: path,
		Parts:        []sessionPart{{projectRef: project, Key: "conv-1-42", ProjectID: 42, SessionID: 99}},
		FirstProject: project,
		Folders:      map[string]projectRef{dir: project},
	}, *meta)
}

func TestRunSync_CursorHookSkipsSubagentTranscripts(t *testing.T) {
	env := newSyncAllEnv(t, true)
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"cursor"}))
	path := filepath.Join(env.home, ".cursor", "projects", "work-repo", "agent-transcripts", "conv-1", "subagents", "sub-1.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(""), 0o644))
	t.Setenv(hookAgentEnv, "cursor")
	t.Setenv(hookSessionEnv, "sub-1")
	t.Setenv(hookTranscriptEnv, path)

	_, err := env.exec("--silent -R " + testHost + "/my-group/my-project")
	require.Error(t, err)

	assert.Empty(t, env.fake.sessionsCreated)
}
