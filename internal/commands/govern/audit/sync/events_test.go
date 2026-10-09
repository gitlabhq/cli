//go:build !integration

package sync

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// richClaudeTranscript is shaped like a real Claude Code transcript: a typed
// prompt, a response streamed as two entries with the same message ID, a
// failed tool result, and a second response.
func richClaudeTranscript(t *testing.T, base time.Time) []byte {
	t.Helper()
	at := func(seconds int) string { return base.Add(time.Duration(seconds) * time.Second).Format(time.RFC3339) }
	entries := []map[string]any{
		{
			"type": "user", "uuid": "u-1", "promptId": "p-1", "timestamp": at(0),
			"message": map[string]any{"role": "user", "content": "run the tests"},
		},
		{
			"type": "assistant", "timestamp": at(1),
			"message": map[string]any{
				"id": "msg_1", "model": "claude-opus-5-5",
				"content": []any{map[string]any{"type": "text", "text": "Running them."}},
				"usage":   map[string]any{"input_tokens": 10, "output_tokens": 1},
			},
		},
		{
			"type": "assistant", "timestamp": at(2),
			"message": map[string]any{
				"id": "msg_1", "model": "claude-opus-5-5",
				"content": []any{map[string]any{"type": "tool_use", "id": "toolu_1", "name": "Bash", "input": map[string]any{"command": "go test ./..."}}},
				"usage":   map[string]any{"input_tokens": 10, "output_tokens": 42, "cache_read_input_tokens": 500},
			},
		},
		{
			"type": "user", "uuid": "u-2", "promptId": "p-1", "timestamp": at(5),
			"message": map[string]any{"role": "user", "content": []any{map[string]any{
				"type": "tool_result", "tool_use_id": "toolu_1", "is_error": true,
				"content": []any{map[string]any{"type": "text", "text": "exit status 1"}},
			}}},
		},
		{
			"type": "assistant", "timestamp": at(6),
			"message": map[string]any{
				"id": "msg_2", "model": "claude-opus-5-5",
				"content": []any{map[string]any{"type": "text", "text": "A test failed."}},
				"usage":   map[string]any{"input_tokens": 60, "output_tokens": 5},
			},
		},
	}
	var content []byte
	for _, e := range entries {
		line, err := json.Marshal(e)
		require.NoError(t, err)
		content = append(append(content, line...), '\n')
	}
	return content
}

func TestParseTranscript_RichEvents(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.jsonl")
	require.NoError(t, os.WriteFile(path, richClaudeTranscript(t, time.Date(2026, 10, 9, 1, 0, 0, 0, time.UTC)), 0o644))

	data, _, err := parseTranscript(path, 0)
	require.NoError(t, err)

	require.Len(t, data.Prompts, 1)
	assert.Equal(t, prompt{ID: "u-1", Text: "run the tests", Timestamp: time.Date(2026, 10, 9, 1, 0, 0, 0, time.UTC)}, data.Prompts[0])
	assert.Equal(t, "run the tests", data.Goal)

	require.Len(t, data.ToolCalls, 1)
	assert.JSONEq(t, `{"command":"go test ./..."}`, string(data.ToolCalls[0].Input))

	require.Len(t, data.ToolResults, 1)
	assert.Equal(t, toolResult{
		CallID: "toolu_1", Name: "Bash", IsError: true, Error: "exit status 1",
		Duration: 3 * time.Second, Timestamp: time.Date(2026, 10, 9, 1, 0, 5, 0, time.UTC),
	}, data.ToolResults[0])

	require.Len(t, data.Responses, 2, "a response streamed as several entries is one response")
	assert.Equal(t, llmResponse{
		ID: "msg_1", Model: "claude-opus-5-5",
		Usage:     tokenUsage{InputTokens: 10, OutputTokens: 42, CacheReadInputTokens: 500},
		Timestamp: time.Date(2026, 10, 9, 1, 0, 1, 0, time.UTC),
	}, data.Responses[0], "the latest entry's usage, at the time the response started")
	assert.Equal(t, "msg_2", data.Responses[1].ID)
}

func TestAuditEvents(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 9, 1, 0, 0, 0, time.UTC)
	data := &sessionData{
		Prompts:   []prompt{{ID: "u-1", Text: "run the tests", Timestamp: at}},
		ToolCalls: []toolCall{{ID: "toolu_1", Name: "Bash", Input: json.RawMessage(`{"command":"go test ./..."}`), Timestamp: at}},
		ToolResults: []toolResult{
			{CallID: "toolu_1", Name: "Bash", IsError: true, Error: strings.Repeat("e", 300), Duration: 1500 * time.Millisecond, Timestamp: at},
			{CallID: "toolu_0", Timestamp: at},
		},
		Responses: []llmResponse{{ID: "msg_1", Model: "claude-opus-5-5", Usage: tokenUsage{InputTokens: 10, OutputTokens: 42}, Timestamp: at}},
	}

	events := auditEvents("claude-code", data, at.Add(time.Hour))
	require.Len(t, events, 5)
	byName := map[string][]auditEventRequest{}
	for _, e := range events {
		assert.Equal(t, "claude-code", e.Details["agent_name"])
		byName[e.EventName] = append(byName[e.EventName], e)
	}

	invoked := byName[eventToolInvoked][0]
	assert.Equal(t, deterministicUUID("toolu_1"), invoked.CloudEventID, "tool calls keep the ID they had before results were recorded")
	assert.Equal(t, "Bash", invoked.Details["tool_name"])
	assert.Equal(t, "pending", invoked.Details["outcome"])
	assert.Equal(t, true, invoked.Details["agent_initiated"])
	assert.JSONEq(t, `{"command":"go test ./..."}`, string(invoked.Details["arguments"].(json.RawMessage))) //nolint:forcetypeassert // set by auditEvents

	failed := byName[eventToolFailed][0]
	assert.Equal(t, "error", failed.Details["outcome"])
	assert.Len(t, failed.Details["error_message"], errorMessageLimit)
	assert.InDelta(t, 1.5, failed.Details["duration_s"], 0.001)
	assert.NotEqual(t, invoked.CloudEventID, failed.CloudEventID)

	succeeded := byName[eventToolResponse][0]
	assert.Equal(t, "success", succeeded.Details["outcome"])
	assert.NotContains(t, succeeded.Details, "tool_name", "a result whose call was synced earlier has no name")
	assert.NotContains(t, succeeded.Details, "duration_s")

	assert.Equal(t, "run the tests", byName[eventUserInput][0].Details["input"])

	response := byName[eventResponseReceived][0]
	assert.Equal(t, "claude-opus-5-5", response.Details["model"])
	assert.Equal(t, 42, response.Details["output_tokens"])
}

func TestAuditEvents_Limits(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 9, 1, 0, 0, 0, time.UTC)
	huge := `{"content":"` + strings.Repeat("x", maxValueBytes) + `"}`
	data := &sessionData{
		Prompts: []prompt{{ID: "u-1", Text: strings.Repeat("é", maxValueBytes), Timestamp: now}},
		ToolCalls: []toolCall{
			{ID: "toolu_big", Name: "Write", Input: json.RawMessage(huge), Timestamp: now},
			{ID: "toolu_old", Name: "Read", Timestamp: now.Add(-91 * 24 * time.Hour)},
		},
	}

	events := auditEvents("claude-code", data, now)
	require.Len(t, events, 2, "an event older than GitLab accepts is dropped")

	input, ok := events[0].Details["input"].(string)
	require.True(t, ok)
	assert.LessOrEqual(t, len(input), maxValueBytes)
	assert.True(t, strings.HasSuffix(input, "é"), "truncation does not split a character")
	assert.Equal(t, true, events[0].Details["input_truncated"])

	assert.Equal(t, argumentsOmitted, events[1].Details["arguments"])
	for _, e := range events {
		details, err := json.Marshal(e.Details)
		require.NoError(t, err)
		assert.Less(t, len(details), 10*1024, "GitLab rejects details over 10 KB")
	}
}

func TestSyncAll_PostsRichEvents(t *testing.T) {
	env := newSyncAllEnv(t, true)
	path := env.writeClaudeSession(t, "sess-1", "my-group/my-project")
	// Relative to now, because the sync drops events older than GitLab accepts.
	content := richClaudeTranscript(t, time.Now().Add(-time.Hour).Truncate(time.Second))
	require.NoError(t, os.WriteFile(path, content, 0o644))

	_, err := env.exec("--all")
	require.NoError(t, err)

	assert.ElementsMatch(t,
		[]string{eventUserInput, eventToolInvoked, eventToolFailed, eventResponseReceived, eventResponseReceived},
		eventNames(env.fake.events))
	assert.Equal(t, 5, readStatus(t).EventsPosted)
}

func TestAuditEvents_OutcomeOverride(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 9, 1, 0, 0, 0, time.UTC)
	events := auditEvents("codex", &sessionData{
		ToolResults: []toolResult{{CallID: "call_1", Outcome: "completed", Timestamp: at}},
	}, at)

	require.Len(t, events, 1)
	assert.Equal(t, eventToolResponse, events[0].EventName)
	assert.Equal(t, "completed", events[0].Details["outcome"], "agents that do not record success do not claim it")
}

func TestAuditEvents_CapsEncodedSize(t *testing.T) {
	t.Parallel()

	// Each of these encodes to more bytes than it takes: < > & become six
	// bytes as GitLab escapes them, and quotes, backslashes, and newlines two.
	escaped := strings.Repeat(`<a href="x">&\`+"\n", 400)
	require.Less(t, len(escaped), maxValueBytes, "small enough before encoding")
	arguments, err := json.Marshal(map[string]string{"contents": escaped})
	require.NoError(t, err)

	now := time.Date(2026, 10, 9, 1, 0, 0, 0, time.UTC)
	events := auditEvents("claude-code", &sessionData{
		Prompts:   []prompt{{ID: "u-1", Text: escaped + "é", Timestamp: now}},
		ToolCalls: []toolCall{{ID: "toolu_1", Name: "Write", Input: arguments, Timestamp: now}},
	}, now)
	require.Len(t, events, 2)

	input, ok := events[0].Details["input"].(string)
	require.True(t, ok)
	assert.LessOrEqual(t, encodedLen(input), maxValueBytes, "the prompt is capped on its encoded size")
	assert.True(t, utf8.ValidString(input))
	assert.Equal(t, true, events[0].Details["input_truncated"])
	assert.Equal(t, argumentsOmitted, events[1].Details["arguments"], "the arguments are too large once encoded")

	for _, e := range events {
		assert.LessOrEqual(t, encodedLen(e.Details), 10*1024, "GitLab measures the encoded details")
	}
}

func TestTruncateEncoded(t *testing.T) {
	t.Parallel()

	got, truncated := truncateEncoded("short", 100)
	assert.Equal(t, "short", got)
	assert.False(t, truncated)

	got, truncated = truncateEncoded(strings.Repeat("é", 100), 52)
	assert.True(t, truncated)
	assert.Equal(t, strings.Repeat("é", 25), got, "two quotes plus 25 two-byte characters")

	got, truncated = truncateEncoded(strings.Repeat("<", 10), 32)
	assert.True(t, truncated)
	assert.Equal(t, strings.Repeat("<", 5), got, "each < encodes to six bytes")
}
