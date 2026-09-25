//go:build !integration

package sync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestDeterministicUUID(t *testing.T) {
	t.Parallel()

	t.Run("same input produces same UUID", func(t *testing.T) {
		t.Parallel()
		id := "toolu_01QY5JDwbgF8AuLRsEMrYyyn"
		first := deterministicUUID(id)
		second := deterministicUUID(id)
		assert.Equal(t, first, second)
	})

	t.Run("different inputs produce different UUIDs", func(t *testing.T) {
		t.Parallel()
		assert.NotEqual(t, deterministicUUID("id-1"), deterministicUUID("id-2"))
	})

	t.Run("output is valid UUID format", func(t *testing.T) {
		t.Parallel()
		uuid := deterministicUUID("toolu_01QY5JDwbgF8AuLRsEMrYyyn")
		assert.Regexp(t, `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`, uuid)
	})
}

func TestCwdToProjectHash(t *testing.T) {
	t.Parallel()

	sep := string(filepath.Separator)
	input1 := sep + filepath.Join("Users", "jean", "demo-gaig")
	input2 := sep + filepath.Join("home", "jean", "project")

	assert.Equal(t, strings.ReplaceAll(input1, sep, "-"), cwdToProjectHash(input1))
	assert.Equal(t, strings.ReplaceAll(input2, sep, "-"), cwdToProjectHash(input2))
}

func TestParseTranscript(t *testing.T) {
	t.Parallel()

	makeEntry := func(entryType string, extra map[string]any) []byte {
		entry := map[string]any{"type": entryType}
		maps.Copy(entry, extra)
		data, _ := json.Marshal(entry)
		return data
	}

	t.Run("extracts goal from first user message", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "session.jsonl")

		lines := [][]byte{
			makeEntry("user", map[string]any{
				"promptId":  "p1",
				"sessionId": "sess-123",
				"timestamp": time.Now().Format(time.RFC3339),
				"message": map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": "list the files in this project"},
					},
				},
			}),
			makeEntry("assistant", map[string]any{
				"sessionId": "sess-123",
				"timestamp": time.Now().Format(time.RFC3339),
				"message": map[string]any{
					"content": []map[string]any{
						{"type": "tool_use", "id": "toolu_abc123", "name": "Bash"},
					},
				},
			}),
		}

		var content []byte
		for _, line := range lines {
			content = append(content, line...)
			content = append(content, '\n')
		}
		require.NoError(t, os.WriteFile(path, content, 0o644))

		data, offset, err := parseTranscript(path, 0)
		require.NoError(t, err)

		assert.Equal(t, "list the files in this project", data.Goal)
		assert.Equal(t, "sess-123", data.SessionID)
		assert.Len(t, data.ToolCalls, 1)
		assert.Equal(t, "Bash", data.ToolCalls[0].Name)
		assert.Equal(t, "toolu_abc123", data.ToolCalls[0].ID)
		assert.Positive(t, offset)
	})

	t.Run("skips entries before cursor offset", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "session.jsonl")

		line := makeEntry("assistant", map[string]any{
			"sessionId": "sess-456",
			"timestamp": time.Now().Format(time.RFC3339),
			"message": map[string]any{
				"content": []map[string]any{
					{"type": "tool_use", "id": "toolu_skip", "name": "Read"},
				},
			},
		})
		require.NoError(t, os.WriteFile(path, append(line, '\n'), 0o644))

		// Start after the first line -- should find no tool calls
		data, _, err := parseTranscript(path, int64(len(line)+1))
		require.NoError(t, err)
		assert.Empty(t, data.ToolCalls)
	})

	t.Run("handles empty file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "empty.jsonl")
		require.NoError(t, os.WriteFile(path, []byte{}, 0o644))

		data, offset, err := parseTranscript(path, 0)
		require.NoError(t, err)
		assert.Empty(t, data.ToolCalls)
		assert.Equal(t, int64(0), offset)
	})

	t.Run("skips malformed lines", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "session.jsonl")

		content := []byte("not valid json\n")
		content = append(content, makeEntry("assistant", map[string]any{
			"sessionId": "sess-789",
			"timestamp": time.Now().Format(time.RFC3339),
			"message": map[string]any{
				"content": []map[string]any{
					{"type": "tool_use", "id": "toolu_valid", "name": "Edit"},
				},
			},
		})...)
		content = append(content, '\n')
		require.NoError(t, os.WriteFile(path, content, 0o644))

		data, _, err := parseTranscript(path, 0)
		require.NoError(t, err)
		assert.Len(t, data.ToolCalls, 1)
		assert.Equal(t, "Edit", data.ToolCalls[0].Name)
	})
}

func TestTranscriptSHA256(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	content := []byte(`{"type":"user","message":"hello"}` + "\n")
	require.NoError(t, os.WriteFile(path, content, 0o644))

	hash := sha256.Sum256(content)
	expected := hex.EncodeToString(hash[:])

	actual, err := sha256File(path)
	require.NoError(t, err)
	assert.Equal(t, expected, actual)
}

func TestSyncSession_NoTranscript(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ios, _, stdout, _ := cmdtest.TestIOStreams()
	opts := &options{io: ios}

	err := syncSession(t.Context(), nil, nil, 0, "test-session", filepath.Join(dir, "nonexistent.jsonl"), opts)
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "No transcript found")
}

func TestSyncSession_NoNewEntries(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	require.NoError(t, os.WriteFile(path, []byte{}, 0o644))

	ios, _, stdout, _ := cmdtest.TestIOStreams()
	opts := &options{io: ios}

	err := syncSession(t.Context(), nil, nil, 0, "test-session", path, opts)
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "No new entries to sync")
}

func TestSyncSession_CursorNotAdvancedOnSessionFailure(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	path := filepath.Join(dir, "session.jsonl")

	// Write a transcript with a tool call
	entry := map[string]any{
		"type":      "assistant",
		"sessionId": "sess-test",
		"timestamp": time.Now().Format(time.RFC3339),
		"message": map[string]any{
			"content": []map[string]any{
				{"type": "tool_use", "id": "toolu_abc", "name": "Bash"},
			},
		},
	}
	data, _ := json.Marshal(entry)
	require.NoError(t, os.WriteFile(path, append(data, '\n'), 0o644))

	ios, _, _, _ := cmdtest.TestIOStreams()
	// nil client will cause ensureSession to fail
	opts := &options{io: ios, silent: true}

	_ = syncSession(t.Context(), nil, nil, 0, "sess-test", path, opts)

	// Cursor should NOT have advanced since session creation failed
	cursor, err := readCursor("sess-test")
	require.NoError(t, err)
	assert.Equal(t, int64(0), cursor, "cursor should not advance when session creation fails")
}

func TestMCPDestructiveAnnotation(t *testing.T) {
	t.Parallel()
	ios, _, _, _ := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(ios)
	cmd := NewCmd(f)
	assert.Equal(t, "true", cmd.Annotations[mcpannotations.Destructive])
}
