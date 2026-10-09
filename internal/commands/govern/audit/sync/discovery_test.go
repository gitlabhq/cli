//go:build !integration

package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/fallbacksync"
	"gitlab.com/gitlab-org/cli/internal/config"
)

// jsonLines encodes records as JSONL.
func jsonLines(t *testing.T, records ...any) string {
	t.Helper()
	var b strings.Builder
	for _, r := range records {
		line, err := json.Marshal(r)
		require.NoError(t, err)
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func codexSessionMetaLine(id, cwd, remote string) map[string]any {
	payload := map[string]any{"id": id, "session_id": id, "timestamp": "2026-10-09T01:00:00Z", "cwd": cwd}
	if remote != "" {
		payload["git"] = map[string]any{"repository_url": remote}
	}
	return map[string]any{"timestamp": "2026-10-09T01:00:00Z", "type": "session_meta", "payload": payload}
}

// recentTimestamp is within the 90 days GitLab accepts events from, for
// tests that sync through --all.
func recentTimestamp() string {
	return time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
}

func codexItem(ts string, payload map[string]any) map[string]any {
	return map[string]any{"timestamp": ts, "type": "response_item", "payload": payload}
}

// writeCodexRollout writes a Codex rollout under HOME/.codex/sessions and
// returns its path.
func writeCodexRollout(t *testing.T, home, id, content string) string {
	t.Helper()
	path := filepath.Join(home, ".codex", "sessions", "2026", "10", "09", "rollout-2026-10-09T01-00-00-"+id+".jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func TestCodexTranscript_Read(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	complete := jsonLines(t,
		codexSessionMetaLine("th-1", "/work", ""),
		codexItem("2026-10-09T01:00:01Z", map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "# AGENTS.md instructions"}}}),
		map[string]any{"timestamp": "2026-10-09T01:00:01Z", "type": "event_msg", "payload": map[string]any{"type": "user_message", "message": "fix the flaky test"}},
		codexItem("2026-10-09T01:00:02Z", map[string]any{"type": "function_call", "name": "exec_command", "call_id": "call_1", "arguments": "{}"}),
		codexItem("2026-10-09T01:00:03Z", map[string]any{"type": "function_call_output", "call_id": "call_1"}),
		codexItem("2026-10-09T01:00:04Z", map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "call_2", "input": ""}),
		codexItem("2026-10-09T01:00:05Z", map[string]any{"type": "local_shell_call", "id": "ls_1", "status": "completed"}),
	)
	partial := `{"timestamp":"2026-10-09T01:00:06Z","type":"response_item","payload":{"type":"function_call","name":"later","call_id":"call_3"`
	require.NoError(t, os.WriteFile(path, []byte(complete+partial), 0o644))

	src := codexTranscript{path: path}
	data, offset, err := src.read(t.Context(), 0)
	require.NoError(t, err)
	assert.Equal(t, "fix the flaky test", data.Goal, "the typed prompt, not injected instructions")
	assert.Equal(t, time.Date(2026, 10, 9, 1, 0, 0, 0, time.UTC), data.StartedAt)
	require.Len(t, data.ToolCalls, 3)
	assert.Equal(t, toolCall{ID: "call_1", Name: "exec_command", Input: json.RawMessage(`{}`), Timestamp: time.Date(2026, 10, 9, 1, 0, 2, 0, time.UTC)}, data.ToolCalls[0])
	assert.Equal(t, "apply_patch", data.ToolCalls[1].Name)
	assert.Equal(t, toolCall{ID: "ls_1", Name: "shell", Timestamp: time.Date(2026, 10, 9, 1, 0, 5, 0, time.UTC)}, data.ToolCalls[2])
	assert.Equal(t, int64(len(complete)), offset, "a line still being written is left for the next read")

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString("}}\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	data, _, err = src.read(t.Context(), offset)
	require.NoError(t, err)
	require.Len(t, data.ToolCalls, 1)
	assert.Equal(t, "call_3", data.ToolCalls[0].ID)
}

func TestCursorTranscript_Read(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "conv-1.jsonl")
	content := jsonLines(t,
		map[string]any{"role": "user", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "<timestamp>Thursday</timestamp>\n<user_query>\nlist the files\n</user_query>"}}}},
		map[string]any{"role": "assistant", "message": map[string]any{"content": []any{
			map[string]any{"type": "text", "text": "Listing."},
			map[string]any{"type": "tool_use", "name": "Shell", "input": map[string]any{"command": "ls"}},
			map[string]any{"type": "tool_use", "name": "ReadFile", "input": map[string]any{}},
		}}},
		map[string]any{"type": "turn_ended", "status": "success"},
	)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	modified := time.Date(2026, 10, 9, 2, 0, 0, 0, time.UTC)
	require.NoError(t, os.Chtimes(path, modified, modified))

	src := cursorTranscript{sessionID: "conv-1", path: path}
	data, offset, err := src.read(t.Context(), 0)
	require.NoError(t, err)
	assert.Equal(t, "list the files", data.Goal)
	require.Len(t, data.ToolCalls, 2)
	assert.Equal(t, "Shell", data.ToolCalls[0].Name)
	assert.Equal(t, "ReadFile", data.ToolCalls[1].Name)
	assert.NotEqual(t, data.ToolCalls[0].ID, data.ToolCalls[1].ID)
	assert.True(t, modified.Equal(data.ToolCalls[0].Timestamp), "Cursor records no timestamps, so the transcript's modification time is used")
	assert.Equal(t, int64(len(content)), offset)

	again, _, err := src.read(t.Context(), 0)
	require.NoError(t, err)
	assert.Equal(t, data.ToolCalls[0].ID, again.ToolCalls[0].ID, "IDs are stable, so re-posting is idempotent")
}

func TestWorkspaceCandidates(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, dir := range []string{"Users/jean/code/ai-tracking", "Users/jean/code/my.repo_v2", "Users/jean/other"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0o755))
	}

	got := workspaceCandidates(root, "Users-jean-code-ai-tracking")
	require.Len(t, got, 1)
	assert.Equal(t, filepath.Join(root, "Users", "jean", "code", "ai-tracking"), got[0])

	got = workspaceCandidates(root, "Users-jean-code-my-repo-v2")
	require.Len(t, got, 1, "dots and underscores are encoded as dashes")
	assert.Equal(t, filepath.Join(root, "Users", "jean", "code", "my.repo_v2"), got[0])

	assert.Empty(t, workspaceCandidates(root, "Users-jean-code-missing"))
}

func TestWorkspaceCandidates_FollowsSymlinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "private", "var", "repo"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join(root, "private", "var"), filepath.Join(root, "var")))

	got := workspaceCandidates(root, "var-repo")
	require.Len(t, got, 1, "/var is a symlink on macOS")
	assert.Equal(t, filepath.Join(root, "var", "repo"), got[0])
}

func TestSyncAll_DiscoversCodexSession(t *testing.T) {
	env := newSyncAllEnv(t, true)
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"codex"}))
	path := writeCodexRollout(t, env.home, "th-1", jsonLines(t,
		codexSessionMetaLine("th-1", "/work", "git@"+testHost+":my-group/my-project.git"),
		codexItem(recentTimestamp(), map[string]any{"type": "function_call", "name": "exec_command", "call_id": "call_1"}),
	))

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Equal(t, []string{"th-1"}, env.fake.sessionsCreated)
	assert.Equal(t, []string{"codex"}, env.fake.agentTypes)
	assert.Equal(t, 1, env.fake.eventsPosted)
	meta, err := readSessionMeta("th-1")
	require.NoError(t, err)
	assert.Equal(t, sessionMeta{AgentType: "codex", PathWithNamespace: "my-group/my-project", Host: testHost, Transcript: path}, *meta)

	_, err = env.exec("--all")
	require.NoError(t, err)
	assert.Len(t, env.fake.sessionsCreated, 1, "a discovered session is synced once")
}

func TestSyncAll_CodexSessionWithoutRemoteUsesGit(t *testing.T) {
	env := newSyncAllEnv(t, true)
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"codex"}))
	for _, id := range []string{"th-1", "th-2"} {
		writeCodexRollout(t, env.home, id, jsonLines(t,
			codexSessionMetaLine(id, "/work/repo", ""),
			codexItem(recentTimestamp(), map[string]any{"type": "function_call", "name": "exec_command", "call_id": "call_" + id}),
		))
	}
	env.mExec.EXPECT().
		ExecWithCombinedOutput(gomock.Any(), "git", []string{"-C", "/work/repo", "config", "--get", "remote.origin.url"}, nil).
		Return([]byte("https://"+testHost+"/my-group/my-project.git\n"), nil).
		Times(1)

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"th-1", "th-2"}, env.fake.sessionsCreated, "git runs once for sessions in the same directory")
}

func TestSyncAll_SkipsDiscoveredSessionsOnOtherHosts(t *testing.T) {
	env := newSyncAllEnv(t, true)
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"codex"}))
	writeCodexRollout(t, env.home, "th-gh", jsonLines(t,
		codexSessionMetaLine("th-gh", "/work", "git@github.com:someone/repo.git"),
		codexItem(recentTimestamp(), map[string]any{"type": "function_call", "name": "exec_command", "call_id": "call_1"}),
	))

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Empty(t, env.fake.projectLookups, "a repository on a host glab is not logged in to is not uploaded")
	_, err = readSessionMeta("th-gh")
	require.ErrorIs(t, err, errNoSessionMeta)
	assert.Empty(t, readStatus(t).Errors)
}

func TestSyncAll_SkipsCodexSubagentThreads(t *testing.T) {
	env := newSyncAllEnv(t, true)
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"codex"}))
	meta := codexSessionMetaLine("th-child", "/work", "git@"+testHost+":my-group/my-project.git")
	meta["payload"].(map[string]any)["parent_thread_id"] = "th-parent" //nolint:forcetypeassert // built above
	writeCodexRollout(t, env.home, "th-child", jsonLines(t, meta))

	_, err := env.exec("--all")
	require.NoError(t, err)

	_, err = readSessionMeta("th-child")
	require.ErrorIs(t, err, errNoSessionMeta)
}

func TestSyncAll_DoesNotDiscoverAgentsThatAreNotEnabled(t *testing.T) {
	env := newSyncAllEnv(t, true)
	writeCodexRollout(t, env.home, "th-1", jsonLines(t,
		codexSessionMetaLine("th-1", "/work", "git@"+testHost+":my-group/my-project.git"),
		codexItem(recentTimestamp(), map[string]any{"type": "function_call", "name": "exec_command", "call_id": "call_1"}),
	))

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Empty(t, env.fake.sessionsCreated)
	_, err = readSessionMeta("th-1")
	require.ErrorIs(t, err, errNoSessionMeta)
}

func TestSyncAll_PausesDiscoveredAgentOnceDisabled(t *testing.T) {
	env := newSyncAllEnv(t, true)
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"codex"}))
	writeCodexRollout(t, env.home, "th-1", jsonLines(t,
		codexSessionMetaLine("th-1", "/work", "git@"+testHost+":my-group/my-project.git"),
	))
	_, err := env.exec("--all")
	require.NoError(t, err)
	_, err = readSessionMeta("th-1")
	require.NoError(t, err, "recorded on the first run")

	require.NoError(t, fallbacksync.SetDiscoveredAgents(nil))
	_, err = env.exec("--all")
	require.NoError(t, err)

	assert.Equal(t, []string{"codex: codex is not enabled; run 'glab govern setup --agents codex' to enable it"}, readStatus(t).Paused)
}

func TestSyncAll_DiscoversCursorSession(t *testing.T) {
	env := newSyncAllEnv(t, true)
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"cursor"}))

	workspace := filepath.Join(t.TempDir(), "my.repo")
	require.NoError(t, os.MkdirAll(workspace, 0o755))
	slug := nonAlphanumeric.ReplaceAllString(strings.TrimPrefix(workspace, string(filepath.Separator)), "-")
	path := filepath.Join(env.home, ".cursor", "projects", slug, "agent-transcripts", "conv-1", "conv-1.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Join(filepath.Dir(path), "subagents"), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(jsonLines(t,
		map[string]any{"role": "user", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "<user_query>list the files</user_query>"}}}},
		map[string]any{"role": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "name": "Shell"}}}},
	)), 0o644))
	env.mExec.EXPECT().
		ExecWithCombinedOutput(gomock.Any(), "git", gomock.Any(), nil).
		DoAndReturn(func(_ context.Context, _ string, args, _ []string) ([]byte, error) {
			resolved, err := filepath.EvalSymlinks(args[1])
			if err != nil {
				return nil, err
			}
			want, err := filepath.EvalSymlinks(workspace)
			if err != nil || resolved != want {
				return nil, errors.New("not the workspace")
			}
			return []byte("git@" + testHost + ":my-group/my-project.git\n"), nil
		})

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Equal(t, []string{"conv-1"}, env.fake.sessionsCreated)
	assert.Equal(t, []string{"cursor"}, env.fake.agentTypes)
	assert.Equal(t, []string{eventUserInput, eventToolInvoked}, eventNames(env.fake.events))
}

func TestProjectForRemote(t *testing.T) {
	t.Parallel()

	cfg := config.NewFromString("hosts:\n  gitlab.example.com:\n    token: x\n")
	hosts := []string{"gitlab.example.com"}

	for _, remote := range []string{
		"git@gitlab.example.com:group/sub/project.git",
		"https://gitlab.example.com/group/sub/project.git",
		"ssh://git@gitlab.example.com:2222/group/sub/project.git",
	} {
		host, path, err := projectForRemote(remote, hosts, cfg)
		require.NoError(t, err, remote)
		assert.Equal(t, "gitlab.example.com", host, remote)
		assert.Equal(t, "group/sub/project", path, remote)
	}

	_, _, err := projectForRemote("git@github.com:someone/repo.git", hosts, cfg)
	require.ErrorIs(t, err, errNotGitLab)

	_, _, err = projectForRemote("", hosts, cfg)
	require.Error(t, err)
}

func TestCodexTranscript_RichEvents(t *testing.T) {
	t.Parallel()

	ts := func(s int) string { return time.Date(2026, 10, 9, 1, 0, s, 0, time.UTC).Format(time.RFC3339) }
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(jsonLines(t,
		codexSessionMetaLine("th-1", "/work", ""),
		map[string]any{"timestamp": ts(0), "type": "turn_context", "payload": map[string]any{"model": "gpt-5.5-codex"}},
		map[string]any{"timestamp": ts(1), "type": "event_msg", "payload": map[string]any{"type": "user_message", "message": "run the tests"}},
		codexItem(ts(2), map[string]any{"type": "function_call", "name": "exec_command", "call_id": "call_1", "arguments": `{"cmd":"go test ./..."}`}),
		codexItem(ts(5), map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "ok"}),
		codexItem(ts(6), map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "call_2", "input": "*** Begin Patch"}),
		map[string]any{"timestamp": ts(7), "type": "event_msg", "payload": map[string]any{"type": "token_count", "info": map[string]any{
			"last_token_usage": map[string]any{"input_tokens": 120, "cached_input_tokens": 100, "output_tokens": 30},
		}}},
		map[string]any{"timestamp": ts(8), "type": "event_msg", "payload": map[string]any{"type": "token_count", "info": nil}},
	)), 0o644))

	data, _, err := codexTranscript{sessionID: "th-1", path: path}.read(t.Context(), 0)
	require.NoError(t, err)

	require.Len(t, data.Prompts, 1)
	assert.Equal(t, "run the tests", data.Prompts[0].Text)
	require.Len(t, data.ToolCalls, 2)
	assert.JSONEq(t, `{"cmd":"go test ./..."}`, string(data.ToolCalls[0].Input))
	assert.JSONEq(t, `{"input":"*** Begin Patch"}`, string(data.ToolCalls[1].Input), "custom tool input is free text")
	require.Len(t, data.ToolResults, 1)
	assert.Equal(t, toolResult{
		CallID: "call_1", Name: "exec_command", Outcome: "completed",
		Duration: 3 * time.Second, Timestamp: time.Date(2026, 10, 9, 1, 0, 5, 0, time.UTC),
	}, data.ToolResults[0], "Codex does not record whether a call succeeded")
	require.Len(t, data.Responses, 1, "a token count without usage is not a response")
	assert.Equal(t, "gpt-5.5-codex", data.Responses[0].Model)
	assert.Equal(t, tokenUsage{InputTokens: 120, OutputTokens: 30, CacheReadInputTokens: 100}, data.Responses[0].Usage)
}

func TestCodexTranscript_PaginatedPrompts(t *testing.T) {
	t.Parallel()

	meta := codexSessionMetaLine("th-1", "/work", "")
	meta["payload"].(map[string]any)["history_mode"] = "paginated" //nolint:forcetypeassert // built above
	user := func(text string) map[string]any {
		return codexItem("2026-10-09T01:00:01Z", map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": text}}})
	}
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(jsonLines(t,
		meta,
		user("# AGENTS.md instructions for /work"),
		user("<environment_context>\n<cwd>/work</cwd>\n</environment_context>"),
		user("fix the flaky test"),
	)), 0o644))

	data, _, err := codexTranscript{sessionID: "th-1", path: path}.read(t.Context(), 0)
	require.NoError(t, err)

	require.Len(t, data.Prompts, 1, "paginated rollouts record prompts as user messages, after injected context")
	assert.Equal(t, "fix the flaky test", data.Prompts[0].Text)
	assert.Equal(t, "fix the flaky test", data.Goal)
}

func TestCursorTranscript_RichEvents(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "conv-1.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(jsonLines(t,
		map[string]any{"role": "user", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "<user_query>list the files</user_query>"}}}},
		map[string]any{"role": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "name": "Shell", "input": map[string]any{"command": "ls"}}}}},
		map[string]any{"role": "user", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "<user_query>now count them</user_query>"}}}},
	)), 0o644))

	data, _, err := cursorTranscript{sessionID: "conv-1", path: path}.read(t.Context(), 0)
	require.NoError(t, err)

	require.Len(t, data.Prompts, 2)
	assert.Equal(t, "list the files", data.Prompts[0].Text)
	assert.Equal(t, "now count them", data.Prompts[1].Text)
	assert.NotEqual(t, data.Prompts[0].ID, data.Prompts[1].ID)
	require.Len(t, data.ToolCalls, 1)
	assert.JSONEq(t, `{"command":"ls"}`, string(data.ToolCalls[0].Input))
	assert.Empty(t, data.ToolResults, "Cursor records no tool results")
}

func TestSyncAll_CompletesOldSyncedSessionOnGitLab(t *testing.T) {
	env := newSyncAllEnv(t, true)
	path := env.writeClaudeSession(t, "sess-1", "my-group/my-project", "toolu_1", "toolu_2")
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	firstLine := int64(bytes.IndexByte(content, '\n') + 1)
	require.NoError(t, writeCursor("sess-1", firstLine))
	old := time.Now().Add(-100 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(path, old, old))

	_, err = env.exec("--all")
	require.NoError(t, err)

	assert.Equal(t, 1, env.fake.completed, "a session that already exists on GitLab is still completed there")
}

func TestSyncAll_SkipsSessionsTooOldForGitLab(t *testing.T) {
	env := newSyncAllEnv(t, true)
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"codex"}))
	old := time.Now().Add(-100 * 24 * time.Hour)
	path := writeCodexRollout(t, env.home, "th-old", jsonLines(t,
		codexSessionMetaLine("th-old", "/work", "git@"+testHost+":my-group/my-project.git"),
		codexItem(old.Format(time.RFC3339), map[string]any{"type": "function_call", "name": "exec_command", "call_id": "call_1"}),
	))
	require.NoError(t, os.Chtimes(path, old, old))

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Empty(t, env.fake.sessionsCreated, "a session GitLab would accept no events for is not created empty")
	meta, err := readSessionMeta("th-old")
	require.NoError(t, err)
	assert.NotNil(t, meta.CompletedAt, "it is not checked again")
}

func TestWorkspaceCandidates_DotAndUnderscoreDirectories(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "Users", "jean", ".config", "proj"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "Users", "jean", "_work", "app"), 0o755))

	got := workspaceCandidates(root, "Users-jean--config-proj")
	require.Len(t, got, 1, "a leading dot inside the path is encoded as a dash")
	assert.Equal(t, filepath.Join(root, "Users", "jean", ".config", "proj"), got[0])

	got = workspaceCandidates(root, "Users-jean--work-app")
	require.Len(t, got, 1, "so is a leading underscore")
	assert.Equal(t, filepath.Join(root, "Users", "jean", "_work", "app"), got[0])
}

func TestWorkspaceCandidates_Ambiguous(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "code", "cli", "docs"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "code", "cli-docs"), 0o755))

	got := workspaceCandidates(root, "code-cli-docs")
	assert.ElementsMatch(t, []string{filepath.Join(root, "code", "cli", "docs"), filepath.Join(root, "code", "cli-docs")}, got)
}

// writeAmbiguousCursorSession creates code/cli/docs and code/cli-docs, which
// Cursor encodes to the same folder name, and a session in that folder whose
// tool call reads readPath. It returns the sibling directory, code/cli-docs.
func (e *syncAllEnv) writeAmbiguousCursorSession(t *testing.T, readPath func(base string) string) string {
	t.Helper()
	base := t.TempDir()
	nested := filepath.Join(base, "code", "cli", "docs")
	sibling := filepath.Join(base, "code", "cli-docs")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	require.NoError(t, os.MkdirAll(sibling, 0o755))
	resolvedBase, err := filepath.EvalSymlinks(base)
	require.NoError(t, err)
	slug := nonAlphanumeric.ReplaceAllString(strings.TrimPrefix(filepath.Join(resolvedBase, "code", "cli-docs"), string(filepath.Separator)), "-")

	path := filepath.Join(e.home, ".cursor", "projects", slug, "agent-transcripts", "conv-1", "conv-1.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(jsonLines(t,
		map[string]any{"role": "user", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "<user_query>update the docs</user_query>"}}}},
		map[string]any{"role": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "name": "Read", "input": map[string]any{"path": readPath(resolvedBase)}}}}},
	)), 0o644))
	return filepath.Join(resolvedBase, "code", "cli-docs")
}

func TestSyncAll_SkipsAmbiguousCursorWorkspace(t *testing.T) {
	env := newSyncAllEnv(t, true)
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"cursor"}))
	env.writeAmbiguousCursorSession(t, func(string) string { return "README.md" })

	// The executor has no expectations: no remote is read for an ambiguous workspace.
	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Empty(t, env.fake.sessionsCreated, "a session must not be uploaded to a project that may not be its own")
	status := readStatus(t)
	assert.Empty(t, status.Errors)
	require.Len(t, status.Skipped, 1)
	assert.Contains(t, status.Skipped[0], "matches 2 directories")
}

func TestSyncAll_ConfirmsAmbiguousCursorWorkspaceFromToolPaths(t *testing.T) {
	env := newSyncAllEnv(t, true)
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"cursor"}))
	sibling := env.writeAmbiguousCursorSession(t, func(base string) string {
		return filepath.Join(base, "code", "cli-docs", "README.md")
	})
	env.mExec.EXPECT().
		ExecWithCombinedOutput(gomock.Any(), "git", []string{"-C", sibling, "config", "--get", "remote.origin.url"}, nil).
		Return([]byte("git@"+testHost+":my-group/my-project.git\n"), nil)

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Equal(t, []string{"conv-1"}, env.fake.sessionsCreated, "a path the session read confirms which directory it ran in")
}

func TestSyncAll_AgentTypeNotSupportedByGitLab(t *testing.T) {
	env := newSyncAllEnv(t, true)
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"codex"}))
	// What GitLab versions without the agent type answer.
	env.fake.identityStatus = http.StatusBadRequest
	env.fake.identityBody = `{"error":"agent_type does not have a valid value"}`
	for _, id := range []string{"th-1", "th-2"} {
		writeCodexRollout(t, env.home, id, jsonLines(t,
			codexSessionMetaLine(id, "/work", "git@"+testHost+":my-group/my-project.git"),
			codexItem(recentTimestamp(), map[string]any{"type": "function_call", "name": "exec_command", "call_id": "call_" + id}),
		))
	}

	_, err := env.exec("--all")
	require.NoError(t, err)

	status := readStatus(t)
	assert.Empty(t, status.Errors, "an agent the GitLab version doesn't support yet is expected, not a failure")
	assert.Equal(t, []string{"codex on " + testHost}, status.AgentTypeNotSupported)
	assert.Equal(t, 1, env.fake.identityRequests)
	for _, id := range []string{"th-1", "th-2"} {
		meta, err := readSessionMeta(id)
		require.NoError(t, err)
		assert.NotNil(t, meta.CompletedAt, "the session is not retried")
	}

	_, err = env.exec("--all")
	require.NoError(t, err)
	assert.Equal(t, 1, env.fake.identityRequests, "and not uploaded in a burst after an upgrade")
}

// writeCursorSessionFor writes a Cursor session for the workspace slug of
// workspace, whose tool call reads readPath, after creating existing.
func (e *syncAllEnv) writeCursorSessionFor(t *testing.T, workspace, existing, readPath string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(existing, 0o755))
	slug := nonAlphanumeric.ReplaceAllString(strings.TrimPrefix(workspace, string(filepath.Separator)), "-")
	path := filepath.Join(e.home, ".cursor", "projects", slug, "agent-transcripts", "conv-1", "conv-1.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(jsonLines(t,
		map[string]any{"role": "user", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "<user_query>update the docs</user_query>"}}}},
		map[string]any{"role": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "name": "Read", "input": map[string]any{"path": readPath}}}}},
	)), 0o644))
}

func TestSyncAll_SkipsCursorWorkspaceThatMoved(t *testing.T) {
	env := newSyncAllEnv(t, true)
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"cursor"}))
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	// code/cli-docs was moved away; code/cli/docs, which encodes the same, remains.
	moved := filepath.Join(base, "code", "cli-docs")
	env.writeCursorSessionFor(t, moved, filepath.Join(base, "code", "cli", "docs"), filepath.Join(moved, "README.md"))

	// The executor has no expectations: the remaining directory's remote is not read.
	_, err = env.exec("--all")
	require.NoError(t, err)

	assert.Empty(t, env.fake.sessionsCreated, "the only matching directory is not the one the session used")
	status := readStatus(t)
	require.Len(t, status.Skipped, 1)
	assert.Contains(t, status.Skipped[0], "may have moved")
}

func TestSyncAll_SyncsCursorWorkspaceConfirmedByToolPaths(t *testing.T) {
	env := newSyncAllEnv(t, true)
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"cursor"}))
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	workspace := filepath.Join(base, "code", "cli-docs")
	env.writeCursorSessionFor(t, workspace, workspace, filepath.Join(workspace, "README.md"))
	env.mExec.EXPECT().
		ExecWithCombinedOutput(gomock.Any(), "git", []string{"-C", workspace, "config", "--get", "remote.origin.url"}, nil).
		Return([]byte("git@"+testHost+":my-group/my-project.git\n"), nil)

	_, err = env.exec("--all")
	require.NoError(t, err)

	assert.Equal(t, []string{"conv-1"}, env.fake.sessionsCreated)
}

func TestCodexTranscript_ObjectArguments(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(jsonLines(t,
		codexSessionMetaLine("th-1", "/work", ""),
		codexItem("2026-10-09T01:00:02Z", map[string]any{"type": "tool_search_call", "call_id": "call_1", "execution": "client", "arguments": map[string]any{"query": "deploy"}}),
	)), 0o644))

	data, _, err := codexTranscript{sessionID: "th-1", path: path}.read(t.Context(), 0)
	require.NoError(t, err)

	require.Len(t, data.ToolCalls, 1, "a call whose arguments are an object is not dropped")
	assert.Equal(t, "tool_search", data.ToolCalls[0].Name)
	assert.JSONEq(t, `{"query":"deploy"}`, string(data.ToolCalls[0].Input))
}

func TestSyncAll_CodexDiscoverySkipsUnreadableDirectories(t *testing.T) {
	env := newSyncAllEnv(t, true)
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"codex"}))
	writeCodexRollout(t, env.home, "th-1", jsonLines(t,
		codexSessionMetaLine("th-1", "/work", "git@"+testHost+":my-group/my-project.git"),
		codexItem(recentTimestamp(), map[string]any{"type": "function_call", "name": "exec_command", "call_id": "call_1"}),
	))
	unreadable := filepath.Join(env.home, ".codex", "sessions", "2026", "10", "08")
	require.NoError(t, os.MkdirAll(unreadable, 0o755))
	require.NoError(t, os.Chmod(unreadable, 0o000))
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o755) })

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.Equal(t, []string{"th-1"}, env.fake.sessionsCreated, "one unreadable directory does not stop discovery")
	assert.Empty(t, readStatus(t).Errors)
}
