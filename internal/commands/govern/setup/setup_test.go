//go:build !integration

package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	path := filepath.Join(dir, ".claude", "settings.json")

	ios, _, _, _ := cmdtest.TestIOStreams()
	opts := &options{
		io:                   ios,
		yes:                  true,
		settingsPathOverride: path,
	}

	err := runSetup(t.Context(), opts)
	require.NoError(t, err)

	_, err = os.Stat(path)
	assert.NoError(t, err, "settings file should have been created")
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
