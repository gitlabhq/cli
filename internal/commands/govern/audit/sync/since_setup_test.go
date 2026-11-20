//go:build !integration

package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/fallbacksync"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/syncstate"
)

// claudeTurns returns a Claude Code transcript of sess-1 with a prompt and a tool call
// at each time, numbered from 0.
func claudeTurns(t *testing.T, at ...time.Time) []byte {
	t.Helper()
	const sessionID = "sess-1"
	var records []any
	for i, ts := range at {
		stamp := ts.UTC().Format(time.RFC3339Nano)
		records = append(records,
			map[string]any{
				"type": "user", "sessionId": sessionID, "uuid": fmt.Sprintf("u-%d", i), "promptId": fmt.Sprintf("p-%d", i),
				"timestamp": stamp, "message": map[string]any{"role": "user", "content": fmt.Sprintf("prompt %d", i)},
			},
			map[string]any{
				"type": "assistant", "sessionId": sessionID, "timestamp": stamp,
				"message": map[string]any{"content": []map[string]any{{"type": "tool_use", "id": fmt.Sprintf("toolu_%d", i), "name": "Bash"}}},
			},
		)
	}
	return []byte(jsonLines(t, records...))
}

// uploadedActivity names the prompts and tool calls in events.
func uploadedActivity(events []auditEventRequest) []string {
	var got []string
	for _, e := range events {
		switch e.EventName {
		case eventUserInput:
			got = append(got, fmt.Sprint("prompt: ", e.Details["content"]))
		case eventToolInvoked:
			got = append(got, fmt.Sprint("tool: ", e.Details["tool_call_id"]))
		}
	}
	return got
}

func setUpAgent(t *testing.T, agent string, setup fallbacksync.AgentSetup) {
	t.Helper()
	require.NoError(t, fallbacksync.SetAgentSetups(fallbacksync.AgentSetups{agent: setup}))
}

func TestDropBefore(t *testing.T) {
	t.Parallel()

	since := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	before, after := since.Add(-time.Hour), since.Add(time.Hour)
	full := func() *sessionData {
		return &sessionData{
			Goal:      "first",
			StartedAt: before,
			Prompts:   []prompt{{ID: "p-0", Text: "first", Timestamp: before}, {ID: "p-1", Text: "second", Timestamp: after}},
			ToolCalls: []toolCall{{ID: "toolu_0", Timestamp: before}, {ID: "toolu_1", Timestamp: since}},
			// toolu_0 spans since: it is called before since and ends after.
			ToolResults: []toolResult{{CallID: "toolu_0", Timestamp: after}, {CallID: "toolu_1", Timestamp: after}},
			Responses:   []llmResponse{{ID: "msg_0", Timestamp: before}, {ID: "msg_1", Timestamp: after}},
		}
	}

	tests := map[string]struct {
		data  *sessionData
		since time.Time
		want  *sessionData
	}{
		"no limit": {data: full(), want: full()},
		"session started before since": {
			data:  full(),
			since: since,
			want: &sessionData{
				Prompts:     []prompt{{ID: "p-1", Text: "second", Timestamp: after}},
				ToolCalls:   []toolCall{{ID: "toolu_1", Timestamp: since}},
				ToolResults: []toolResult{{CallID: "toolu_1", Timestamp: after}},
				Responses:   []llmResponse{{ID: "msg_1", Timestamp: after}},
			},
		},
		"nothing after since": {
			data:  &sessionData{Goal: "first", StartedAt: before, Prompts: []prompt{{ID: "p-0", Text: "first", Timestamp: before}}},
			since: since,
			want:  &sessionData{Prompts: []prompt{}},
		},
		"goal without prompts, as from OpenCode": {
			data:  &sessionData{Goal: "fix the flaky test", StartedAt: before},
			since: since,
			want:  &sessionData{},
		},
		"session started after since": {
			data:  &sessionData{Goal: "second", StartedAt: after, Prompts: []prompt{{ID: "p-1", Text: "second", Timestamp: after}}},
			since: since,
			want:  &sessionData{Goal: "second", StartedAt: after, Prompts: []prompt{{ID: "p-1", Text: "second", Timestamp: after}}},
		},
		"goal kept on a later read, which sets none": {
			data:  &sessionData{Prompts: []prompt{{ID: "p-0", Text: "first", Timestamp: before}, {ID: "p-1", Text: "second", Timestamp: after}}},
			since: since,
			want:  &sessionData{Prompts: []prompt{{ID: "p-1", Text: "second", Timestamp: after}}},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tc.data.dropBefore(tc.since)
			assert.Equal(t, tc.want.Goal, tc.data.Goal)
			assert.Equal(t, tc.want.StartedAt, tc.data.StartedAt)
			assert.ElementsMatch(t, tc.want.Prompts, tc.data.Prompts)
			assert.ElementsMatch(t, tc.want.ToolCalls, tc.data.ToolCalls)
			assert.ElementsMatch(t, tc.want.ToolResults, tc.data.ToolResults)
			assert.ElementsMatch(t, tc.want.Responses, tc.data.Responses)
		})
	}
}

func TestRunSync_UploadsClaudeCodeActivityFromSetup(t *testing.T) {
	now := time.Now()

	tests := map[string]struct {
		setup *fallbacksync.AgentSetup
		turns []time.Time
		args  string
		want  []string
		// wantGoal is empty when no session is created.
		wantGoal string
	}{
		"session started after setup": {
			setup:    &fallbacksync.AgentSetup{SetUpAt: now.Add(-2 * time.Hour)},
			turns:    []time.Time{now.Add(-time.Hour), now.Add(-time.Minute)},
			want:     []string{"prompt: prompt 0", "prompt: prompt 1", "tool: toolu_0", "tool: toolu_1"},
			wantGoal: "prompt 0",
		},
		"session from before setup, resumed": {
			setup:    &fallbacksync.AgentSetup{SetUpAt: now.Add(-time.Hour)},
			turns:    []time.Time{now.Add(-3 * time.Hour), now.Add(-time.Minute)},
			want:     []string{"prompt: prompt 1", "tool: toolu_1"},
			wantGoal: "prompt 1",
		},
		"history": {
			setup:    &fallbacksync.AgentSetup{SetUpAt: now.Add(-time.Hour), History: true},
			turns:    []time.Time{now.Add(-3 * time.Hour), now.Add(-time.Minute)},
			want:     []string{"prompt: prompt 0", "prompt: prompt 1", "tool: toolu_0", "tool: toolu_1"},
			wantGoal: "prompt 0",
		},
		"set up before glab recorded when": {
			turns:    []time.Time{now.Add(-3 * time.Hour), now.Add(-time.Minute)},
			want:     []string{"prompt: prompt 0", "prompt: prompt 1", "tool: toolu_0", "tool: toolu_1"},
			wantGoal: "prompt 0",
		},
		"session from before setup, ended": {
			setup: &fallbacksync.AgentSetup{SetUpAt: now.Add(-time.Hour)},
			turns: []time.Time{now.Add(-3 * time.Hour)},
			args:  " --complete",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			env := newSyncAllEnv(t, true)
			t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-1")
			if tc.setup != nil {
				setUpAgent(t, "claude-code", *tc.setup)
			}
			cwd, err := currentCWD()
			require.NoError(t, err)
			path, err := transcriptPath("sess-1", cwd)
			require.NoError(t, err)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			require.NoError(t, os.WriteFile(path, claudeTurns(t, tc.turns...), 0o644))

			_, err = env.exec("-R my-group/my-project --silent" + tc.args)
			require.NoError(t, err)

			assert.ElementsMatch(t, tc.want, uploadedActivity(env.fake.events))
			if tc.wantGoal == "" {
				assert.Empty(t, env.fake.sessionRequests, "a session with nothing to upload is not created on GitLab")
				assert.Zero(t, env.fake.completed)
				return
			}
			require.Len(t, env.fake.sessionRequests, 1)
			assert.Equal(t, tc.wantGoal, env.fake.sessionRequests[0].Goal)
		})
	}
}

func TestRunSync_UnreadableAgentSetupsUploadNothing(t *testing.T) {
	env := newSyncAllEnv(t, true)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-1")
	writeHookTranscript(t, "sess-1", "toolu_1")
	path := filepath.Join(env.home, "glab", "gaig", "agent-setups.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))

	_, err := env.exec("-R my-group/my-project --silent")
	require.ErrorContains(t, err, "could not tell which claude-code activity to upload")
	assert.Empty(t, env.fake.sessionRequests)

	_, err = env.exec("--all")
	require.Error(t, err)
	assert.Empty(t, env.fake.sessionRequests)
	assert.Contains(t, readStatus(t).Errors[0], "could not tell which activity to upload")
}

func TestSyncAll_UploadsClaudeCodeActivityFromSetup(t *testing.T) {
	env := newSyncAllEnv(t, true)
	now := time.Now()
	setUpAgent(t, "claude-code", fallbacksync.AgentSetup{SetUpAt: now.Add(-time.Hour)})
	path := env.writeClaudeSession(t, "sess-1", "my-group/my-project")
	require.NoError(t, os.WriteFile(path, claudeTurns(t, now.Add(-3*time.Hour), now.Add(-time.Minute)), 0o644))

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"prompt: prompt 1", "tool: toolu_1"}, uploadedActivity(env.fake.events))
	assertCursorAtEnd(t, path)
}

func TestSyncAll_UploadsCodexActivityFromSetup(t *testing.T) {
	now := time.Now()
	remote := "git@" + testHost + ":my-group/my-project.git"
	call := func(at time.Time, id string) map[string]any {
		return codexItem(at.UTC().Format(time.RFC3339), map[string]any{"type": "function_call", "name": "exec_command", "call_id": id})
	}

	t.Run("session that runs across setup", func(t *testing.T) {
		env := newSyncAllEnv(t, true)
		require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"codex"}))
		setUpAgent(t, "codex", fallbacksync.AgentSetup{SetUpAt: now.Add(-time.Hour)})
		writeCodexRollout(t, env.home, "th-1", jsonLines(t,
			codexSessionMetaLine("th-1", "/work", remote),
			call(now.Add(-3*time.Hour), "call_before"),
			call(now.Add(-time.Minute), "call_after"),
		))

		_, err := env.exec("--all")
		require.NoError(t, err)

		assert.Equal(t, []string{"tool: call_after"}, uploadedActivity(env.fake.events))
	})

	t.Run("session from before setup", func(t *testing.T) {
		env := newSyncAllEnv(t, true)
		require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"codex"}))
		setUpAgent(t, "codex", fallbacksync.AgentSetup{SetUpAt: now.Add(-24 * time.Hour)})
		old := now.Add(-3 * 24 * time.Hour)
		path := writeCodexRollout(t, env.home, "th-1", jsonLines(t,
			codexSessionMetaLine("th-1", "/work", remote),
			call(old, "call_before"),
		))
		require.NoError(t, os.Chtimes(path, old, old))

		for range 2 {
			_, err := env.exec("--all")
			require.NoError(t, err)
		}

		assert.Empty(t, env.fake.sessionRequests, "an idle session with only activity from before setup is not created on GitLab, to be completed")
		meta, err := readSessionMeta("th-1")
		require.NoError(t, err)
		assert.Nil(t, meta.CompletedAt, "a session from before setup is kept, in case the user resumes it")
		assertCursorAt(t, "th-1", fileSize(t, path))

		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
		require.NoError(t, err)
		_, err = f.WriteString(jsonLines(t,
			map[string]any{"timestamp": now.UTC().Format(time.RFC3339), "type": "event_msg", "payload": map[string]any{"type": "user_message", "message": "a prompt after setup"}},
			call(now, "call_resumed"),
		))
		require.NoError(t, errors.Join(err, f.Close()))

		_, err = env.exec("--all")
		require.NoError(t, err)

		assert.Equal(t, []string{"prompt: a prompt after setup", "tool: call_resumed"}, uploadedActivity(env.fake.events), "the session uploads what the user does after resuming it")
		require.Len(t, env.fake.sessionRequests, 1)
		assert.Equal(t, "a prompt after setup", env.fake.sessionRequests[0].Goal, "the goal is the first prompt after setup")
		assert.Empty(t, env.fake.sessionRequests[0].StartedAt, "the session started before setup")
	})

	t.Run("session from before setup that GitLab would not accept", func(t *testing.T) {
		env := newSyncAllEnv(t, true)
		require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"codex"}))
		setUpAgent(t, "codex", fallbacksync.AgentSetup{SetUpAt: now.Add(-24 * time.Hour)})
		old := now.Add(-100 * 24 * time.Hour)
		path := writeCodexRollout(t, env.home, "th-1", jsonLines(t, codexSessionMetaLine("th-1", "/work", remote)))
		require.NoError(t, os.Chtimes(path, old, old))

		_, err := env.exec("--all")
		require.NoError(t, err)

		meta, err := readSessionMeta("th-1")
		require.NoError(t, err)
		assert.NotNil(t, meta.CompletedAt, "it is not checked again, as without setup")
	})
}

func TestSyncAll_UploadsOpenCodeActivityFromSetup(t *testing.T) {
	env := newSyncAllEnv(t, true)
	now := time.UnixMilli(time.Now().UnixMilli())
	setUpAgent(t, "opencode", fallbacksync.AgentSetup{SetUpAt: now.Add(-time.Hour)})

	tool := func(id string, at time.Time) map[string]any {
		return map[string]any{
			"id": "prt_" + id, "type": "tool", "callID": id, "tool": "bash",
			"state": map[string]any{"status": "completed", "time": map[string]any{"start": at.UnixMilli(), "end": at.UnixMilli()}},
		}
	}
	export, err := json.Marshal(map[string]any{
		"info": map[string]any{"id": "ses_1", "time": map[string]any{"created": now.Add(-3 * time.Hour).UnixMilli(), "updated": now.UnixMilli()}},
		"messages": []map[string]any{
			{
				"info":  map[string]any{"role": "user", "time": map[string]any{"created": now.Add(-3 * time.Hour).UnixMilli()}},
				"parts": []map[string]any{{"id": "prt_goal", "type": "text", "text": "a prompt from before setup"}},
			},
			{
				"info":  map[string]any{"role": "assistant", "time": map[string]any{"created": now.UnixMilli()}},
				"parts": []map[string]any{tool("call_before", now.Add(-3*time.Hour)), tool("call_after", now)},
			},
		},
	})
	require.NoError(t, err)
	binary := filepath.Join(t.TempDir(), "opencode")
	require.NoError(t, os.WriteFile(binary, nil, 0o755))
	require.NoError(t, writeSessionMeta("ses_1", sessionMeta{AgentType: "opencode", PathWithNamespace: "my-group/my-project", Host: testHost, Transcript: binary}))
	env.mExec.EXPECT().
		ExecWithIO(gomock.Any(), binary, []string{"session", "export", "--standalone", "ses_1"}, nil, nil, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _, _ []string, _ io.Reader, stdout, _ io.Writer) error {
			_, err := stdout.Write(export)
			return err
		})

	_, err = env.exec("--all")
	require.NoError(t, err)

	assert.Equal(t, []string{"tool: call_after"}, uploadedActivity(env.fake.events))
	require.Len(t, env.fake.sessionRequests, 1)
	assert.Empty(t, env.fake.sessionRequests[0].Goal, "the goal is a prompt from before setup")
	assertCursorAt(t, "ses_1", 2)
}

func TestSyncAll_UploadsCursorActivityFromSetup(t *testing.T) {
	now := time.Now()
	promptLine := func(text string) string {
		return jsonLines(t, map[string]any{"role": "user", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "<user_query>" + text + "</user_query>"}}}})
	}

	tests := map[string]struct {
		setUpAt time.Time
		// snapshot starts the session where its transcript ends, as setup
		// does for the Cursor sessions that exist when it runs.
		snapshot bool
		want     []string
	}{
		"session created after setup": {
			setUpAt: now.Add(-time.Hour),
			want:    []string{"prompt: earlier turn", "prompt: later turn"},
		},
		"session from before setup, resumed": {
			setUpAt:  now.Add(-time.Hour),
			snapshot: true,
			want:     []string{"prompt: later turn"},
		},
		"session last active before setup, which setup did not see": {
			setUpAt: now.Add(time.Minute),
			want:    []string{"prompt: later turn"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			env := newSyncAllEnv(t, true)
			require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"cursor"}))
			setUpAgent(t, "cursor", fallbacksync.AgentSetup{SetUpAt: tc.setUpAt})
			path := filepath.Join(t.TempDir(), "conv-1", "conv-1.jsonl")
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			require.NoError(t, os.WriteFile(path, []byte(promptLine("earlier turn")), 0o644))
			if tc.snapshot {
				require.NoError(t, syncstate.StartAt("conv-1", fileSize(t, path)))
			}
			require.NoError(t, writeSessionMeta("conv-1", sessionMeta{AgentType: "cursor", PathWithNamespace: "my-group/my-project", Host: testHost, Transcript: path}))

			_, err := env.exec("--all")
			require.NoError(t, err)

			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
			require.NoError(t, err)
			_, err = f.WriteString(promptLine("later turn"))
			require.NoError(t, errors.Join(err, f.Close()))
			later := now.Add(3 * time.Minute)
			require.NoError(t, os.Chtimes(path, later, later))

			_, err = env.exec("--all")
			require.NoError(t, err)

			assert.Equal(t, tc.want, uploadedActivity(env.fake.events))
			assertCursorAt(t, "conv-1", fileSize(t, path))
		})
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	return info.Size()
}

func TestRunSync_SkipsClaudeCodeCompactionSummaries(t *testing.T) {
	env := newSyncAllEnv(t, true)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-1")
	now := time.Now()
	setUpAgent(t, "claude-code", fallbacksync.AgentSetup{SetUpAt: now.Add(-time.Hour)})
	summary := func(field string) map[string]any {
		return map[string]any{
			"type": "user", "sessionId": "sess-1", "uuid": "u-" + field, "promptId": "p-" + field, field: true,
			"timestamp": now.Add(-time.Minute).UTC().Format(time.RFC3339Nano),
			"message":   map[string]any{"role": "user", "content": "This session continues a conversation from before setup."},
		}
	}
	cwd, err := currentCWD()
	require.NoError(t, err)
	path, err := transcriptPath("sess-1", cwd)
	require.NoError(t, err)
	content := append(claudeTurns(t, now.Add(-3*time.Hour)), jsonLines(t, summary("isCompactSummary"), summary("isVisibleInTranscriptOnly"))...)
	content = append(content, jsonLines(t, map[string]any{
		"type": "user", "sessionId": "sess-1", "uuid": "u-after", "promptId": "p-after",
		"timestamp": now.UTC().Format(time.RFC3339Nano), "message": map[string]any{"role": "user", "content": "a prompt after setup"},
	})...)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, content, 0o644))

	_, err = env.exec("-R my-group/my-project --silent")
	require.NoError(t, err)

	assert.Equal(t, []string{"prompt: a prompt after setup"}, uploadedActivity(env.fake.events), "a summary of the conversation before setup is not uploaded")
	require.Len(t, env.fake.sessionRequests, 1)
	assert.Equal(t, "a prompt after setup", env.fake.sessionRequests[0].Goal)
}

func TestRunSync_CompleteOnlySessionsWithUploads(t *testing.T) {
	tests := map[string]struct {
		uploaded      bool
		wantCompleted int
	}{
		"nothing uploaded": {},
		"uploaded before":  {uploaded: true, wantCompleted: 1},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			env := newSyncAllEnv(t, true)
			t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-1")
			setUpAgent(t, "claude-code", fallbacksync.AgentSetup{SetUpAt: time.Now().Add(-time.Hour)})
			cwd, err := currentCWD()
			require.NoError(t, err)
			path, err := transcriptPath("sess-1", cwd)
			require.NoError(t, err)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			line := jsonLines(t, map[string]any{"type": "system", "sessionId": "sess-1", "timestamp": time.Now().UTC().Format(time.RFC3339Nano)})
			require.NoError(t, os.WriteFile(path, []byte(line), 0o644))
			if tc.uploaded {
				require.NoError(t, syncstate.WritePosition("sess-1", int64(len(line))))
			}

			_, err = env.exec("-R my-group/my-project --silent --complete")
			require.NoError(t, err)

			assert.Equal(t, tc.wantCompleted, env.fake.completed)
			assert.Len(t, env.fake.sessionRequests, tc.wantCompleted, "a session with nothing uploaded is not created on GitLab")
		})
	}
}

func TestRunSync_CursorHookSessionFromBeforeSetup(t *testing.T) {
	env := newSyncAllEnv(t, true)
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"cursor"}))
	setUpAgent(t, "cursor", fallbacksync.AgentSetup{SetUpAt: time.Now().Add(-time.Hour)})
	path := writeCursorTranscript(t, env.home, "conv-1")
	require.NoError(t, syncstate.StartAt("conv-1", fileSize(t, path)))
	t.Setenv(hookAgentEnv, "cursor")
	t.Setenv(hookSessionEnv, "conv-1")
	t.Setenv(hookTranscriptEnv, path)

	_, err := env.exec("--silent -R " + testHost + "/my-group/my-project")
	require.NoError(t, err)
	assert.Empty(t, env.fake.sessionRequests, "the turns from before setup are not uploaded")

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(jsonLines(t, map[string]any{"role": "user", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "<user_query>a turn after setup</user_query>"}}}}))
	require.NoError(t, errors.Join(err, f.Close()))

	_, err = env.exec("--silent -R " + testHost + "/my-group/my-project")
	require.NoError(t, err)
	assert.Equal(t, []string{"prompt: a turn after setup"}, uploadedActivity(env.fake.events))
	require.Len(t, env.fake.sessionRequests, 1)
	assert.Equal(t, "a turn after setup", env.fake.sessionRequests[0].Goal, "the goal is the first prompt after setup")
}

func TestRunSync_UnreadableCursorUploadsNothing(t *testing.T) {
	env := newSyncAllEnv(t, true)
	require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"cursor"}))
	setUpAgent(t, "cursor", fallbacksync.AgentSetup{SetUpAt: time.Now().Add(-time.Hour)})
	path := writeCursorTranscript(t, env.home, "conv-1")
	cp, err := syncstate.PositionPath("conv-1")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(cp), 0o755))
	require.NoError(t, os.WriteFile(cp, []byte("not a number"), 0o644))
	t.Setenv(hookAgentEnv, "cursor")
	t.Setenv(hookSessionEnv, "conv-1")
	t.Setenv(hookTranscriptEnv, path)

	_, err = env.exec("--silent -R " + testHost + "/my-group/my-project")
	require.ErrorContains(t, err, "could not read the sync position")
	assert.Empty(t, env.fake.sessionRequests, "the transcript is not uploaded again from its beginning")
}

func TestRunSync_ClaudeCodeSessionFromBeforeSetupTakesItsGoalAfterSetup(t *testing.T) {
	env := newSyncAllEnv(t, true)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-1")
	now := time.Now()
	setUpAgent(t, "claude-code", fallbacksync.AgentSetup{SetUpAt: now.Add(-time.Hour)})
	cwd, err := currentCWD()
	require.NoError(t, err)
	path, err := transcriptPath("sess-1", cwd)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, claudeTurns(t, now.Add(-3*time.Hour)), 0o644))
	old := now.Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(path, old, old))

	_, err = env.exec("-R my-group/my-project --silent")
	require.NoError(t, err)
	require.Empty(t, env.fake.sessionRequests)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(jsonLines(t, map[string]any{
		"type": "user", "sessionId": "sess-1", "uuid": "u-after", "promptId": "p-after",
		"timestamp": now.UTC().Format(time.RFC3339Nano), "message": map[string]any{"role": "user", "content": "a prompt after setup"},
	}))
	require.NoError(t, errors.Join(err, f.Close()))

	_, err = env.exec("-R my-group/my-project --silent")
	require.NoError(t, err)

	assert.Equal(t, []string{"prompt: a prompt after setup"}, uploadedActivity(env.fake.events))
	require.Len(t, env.fake.sessionRequests, 1)
	assert.Equal(t, "a prompt after setup", env.fake.sessionRequests[0].Goal)
}

// A call that starts before setup and ends after it, such as the agent
// running glab minder setup, uploads neither the call nor its result.
func TestSync_LeavesOutResultsOfCallsFromBeforeSetup(t *testing.T) {
	now := time.Now()
	setUpAt := now.Add(-time.Hour)
	before, after := setUpAt.Add(-time.Minute), setUpAt.Add(time.Minute)

	t.Run("claude-code", func(t *testing.T) {
		env := newSyncAllEnv(t, true)
		t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-1")
		setUpAgent(t, "claude-code", fallbacksync.AgentSetup{SetUpAt: setUpAt})
		call := func(id string) map[string]any {
			return map[string]any{
				"type": "assistant", "sessionId": "sess-1", "timestamp": before.UTC().Format(time.RFC3339Nano),
				"message": map[string]any{"content": []map[string]any{{"type": "tool_use", "id": id, "name": "Bash"}}},
			}
		}
		result := func(id string, failed bool) map[string]any {
			return map[string]any{
				"type": "user", "sessionId": "sess-1", "timestamp": after.UTC().Format(time.RFC3339Nano),
				"message": map[string]any{"role": "user", "content": []map[string]any{{"type": "tool_result", "tool_use_id": id, "is_error": failed, "content": "tool output"}}},
			}
		}
		cwd, err := currentCWD()
		require.NoError(t, err)
		path, err := transcriptPath("sess-1", cwd)
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(jsonLines(t, call("toolu_fails"), call("toolu_succeeds"), result("toolu_fails", true), result("toolu_succeeds", false))), 0o644))

		_, err = env.exec("-R my-group/my-project --silent")
		require.NoError(t, err)

		assert.Empty(t, env.fake.events)
		assert.Empty(t, env.fake.sessionRequests, "no session is created for the results alone")
	})

	t.Run("codex", func(t *testing.T) {
		env := newSyncAllEnv(t, true)
		require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"codex"}))
		setUpAgent(t, "codex", fallbacksync.AgentSetup{SetUpAt: setUpAt})
		writeCodexRollout(t, env.home, "th-1", jsonLines(t,
			codexSessionMetaLine("th-1", "/work", "git@"+testHost+":my-group/my-project.git"),
			codexItem(before.UTC().Format(time.RFC3339), map[string]any{"type": "function_call", "name": "exec_command", "call_id": "call_1"}),
			codexItem(after.UTC().Format(time.RFC3339), map[string]any{"type": "function_call_output", "call_id": "call_1"}),
		))

		_, err := env.exec("--all")
		require.NoError(t, err)

		assert.Empty(t, env.fake.events)
		assert.Empty(t, env.fake.sessionRequests, "no session is created for the result alone")
	})
}

// appendLines appends JSONL records to the transcript at path.
func appendLines(t *testing.T, path string, records ...any) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(jsonLines(t, records...))
	require.NoError(t, errors.Join(err, f.Close()))
}

// A call still running when glab skips a transcript from before setup ends in
// a later read, which must not upload its result without the call.
func TestSync_LeavesOutResultsOfCallsSkippedBeforeSetup(t *testing.T) {
	now := time.Now()
	setUpAt := now.Add(-24 * time.Hour)
	old := now.Add(-3 * 24 * time.Hour)
	stamp := func(at time.Time) string { return at.UTC().Format(time.RFC3339Nano) }

	tests := map[string]struct {
		agent     string
		sessionID string
		// write writes a transcript from before setup with a running call,
		// and returns its path.
		write func(t *testing.T, env *syncAllEnv) string
		// result and prompt are the records the agent adds after setup.
		result, prompt any
		args           string
	}{
		"claude-code": {
			agent:     "claude-code",
			sessionID: "sess-1",
			write: func(t *testing.T, env *syncAllEnv) string {
				t.Helper()
				t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-1")
				cwd, err := currentCWD()
				require.NoError(t, err)
				path, err := transcriptPath("sess-1", cwd)
				require.NoError(t, err)
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
				return path
			},
			result: map[string]any{
				"type": "user", "sessionId": "sess-1", "timestamp": stamp(now),
				"message": map[string]any{"role": "user", "content": []map[string]any{{"type": "tool_result", "tool_use_id": "toolu_0", "content": "tool output"}}},
			},
			prompt: map[string]any{
				"type": "user", "sessionId": "sess-1", "uuid": "u-after", "promptId": "p-after",
				"timestamp": stamp(now), "message": map[string]any{"role": "user", "content": "a prompt after setup"},
			},
			args: "-R my-group/my-project --silent",
		},
		"codex": {
			agent:     "codex",
			sessionID: "th-1",
			write: func(t *testing.T, env *syncAllEnv) string {
				t.Helper()
				require.NoError(t, fallbacksync.SetDiscoveredAgents([]string{"codex"}))
				return writeCodexRollout(t, env.home, "th-1", jsonLines(t, codexSessionMetaLine("th-1", "/work", "git@"+testHost+":my-group/my-project.git")))
			},
			result: codexItem(stamp(now), map[string]any{"type": "function_call_output", "call_id": "toolu_0"}),
			prompt: map[string]any{"timestamp": stamp(now), "type": "event_msg", "payload": map[string]any{"type": "user_message", "message": "a prompt after setup"}},
			args:   "--all",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			env := newSyncAllEnv(t, true)
			setUpAgent(t, tc.agent, fallbacksync.AgentSetup{SetUpAt: setUpAt})
			path := tc.write(t, env)
			if tc.agent == "claude-code" {
				require.NoError(t, os.WriteFile(path, claudeTurns(t, old), 0o644))
			} else {
				appendLines(t, path, codexItem(stamp(old), map[string]any{"type": "function_call", "name": "exec_command", "call_id": "toolu_0"}))
			}
			require.NoError(t, os.Chtimes(path, old, old))

			_, err := env.exec(tc.args)
			require.NoError(t, err)
			assertCursorAt(t, tc.sessionID, fileSize(t, path))

			appendLines(t, path, tc.result)
			_, err = env.exec(tc.args)
			require.NoError(t, err)
			assert.Empty(t, env.fake.sessionRequests, "no session is created for the result alone")

			appendLines(t, path, tc.prompt)
			_, err = env.exec(tc.args)
			require.NoError(t, err)
			assert.Equal(t, []string{eventUserInput}, eventNames(env.fake.events))
		})
	}
}
