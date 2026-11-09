//go:build !integration

package claudehooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteSettings_PreservesFilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/settings.json"

	// Create file with 0644
	require.NoError(t, os.WriteFile(path, []byte(`{"model":"test"}`), 0o644))
	info, _ := os.Stat(path)
	before := info.Mode()

	raw := map[string]json.RawMessage{"model": json.RawMessage(`"test"`)}
	require.NoError(t, WriteSettings(path, raw))

	info, _ = os.Stat(path)
	after := info.Mode()

	assert.Equal(t, before, after, "file permissions should be preserved")
}

func TestSyncHookInstalled(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "settings.json")

	installed, err := SyncHookInstalled(path)
	require.NoError(t, err)
	assert.False(t, installed, "a missing settings file has no hooks")

	hooks := map[string][]HookGroup{}
	AddHook(hooks, "Stop", StopHookCommand)
	hooksJSON, err := json.Marshal(hooks)
	require.NoError(t, err)
	require.NoError(t, WriteSettings(path, map[string]json.RawMessage{"hooks": hooksJSON}))

	installed, err = SyncHookInstalled(path)
	require.NoError(t, err)
	assert.True(t, installed)

	hooks = map[string][]HookGroup{}
	AddHook(hooks, "Stop", legacyStopHookCommand)
	hooksJSON, err = json.Marshal(hooks)
	require.NoError(t, err)
	require.NoError(t, WriteSettings(path, map[string]json.RawMessage{"hooks": hooksJSON}))

	installed, err = SyncHookInstalled(path)
	require.NoError(t, err)
	assert.True(t, installed, "a Stop hook from before the rename to glab minder still syncs")

	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o644))
	_, err = SyncHookInstalled(path)
	require.Error(t, err)
}

func TestHooks_RoundTripKeepsUnknownFields(t *testing.T) {
	t.Parallel()

	input := `{"PostToolUse":[{"matcher":"Bash","futureGroupField":{"x":1},"hooks":[{"type":"command","command":"~/.claude/hooks/print-mr-link.sh","if":"Bash(git push*)","statusMessage":"Checking for MR link","timeout":0}]}]}`

	var hooks map[string][]HookGroup
	require.NoError(t, json.Unmarshal([]byte(input), &hooks))
	assert.True(t, AddHook(hooks, "Stop", StopHookCommand))

	out, err := MarshalHooks(hooks)
	require.NoError(t, err)

	var got map[string][]struct {
		Matcher     string                       `json:"matcher"`
		FutureField json.RawMessage              `json:"futureGroupField"`
		Hooks       []map[string]json.RawMessage `json:"hooks"`
	}
	require.NoError(t, json.Unmarshal(out, &got))
	require.Len(t, got["PostToolUse"], 1)
	group := got["PostToolUse"][0]
	assert.Equal(t, "Bash", group.Matcher)
	assert.JSONEq(t, `{"x":1}`, string(group.FutureField))
	require.Len(t, group.Hooks, 1)
	entry := group.Hooks[0]
	assert.JSONEq(t, `"Bash(git push*)"`, string(entry["if"]))
	assert.JSONEq(t, `"Checking for MR link"`, string(entry["statusMessage"]))
	assert.JSONEq(t, `0`, string(entry["timeout"]), "an explicit zero value is written back")
	assert.Len(t, got["Stop"], 1)
}

func TestMarshalHooks_KeepsShellCharactersReadable(t *testing.T) {
	t.Parallel()

	hooks := map[string][]HookGroup{}
	AddHook(hooks, "Stop", StopHookCommand)

	out, err := MarshalHooks(hooks)
	require.NoError(t, err)
	assert.Contains(t, string(out), `>/dev/null 2>&1 &`)
	assert.NotContains(t, string(out), `\u003e`)
}

func TestRemoveHook(t *testing.T) {
	t.Parallel()

	input := `{
		"Stop": [
			{"hooks": [{"type": "command", "command": "` + StopHookCommand + `"}]},
			{"matcher": "Bash", "futureGroupField": {"x": 1}, "hooks": [
				{"type": "command", "command": "` + StopHookCommand + `"},
				{"type": "command", "command": "echo user-hook", "if": "Bash(git push*)"}
			]}
		],
		"SessionEnd": [{"hooks": [{"type": "command", "command": "` + SessionEndHookCommand + `"}]}]
	}`
	var hooks map[string][]HookGroup
	require.NoError(t, json.Unmarshal([]byte(input), &hooks))

	assert.True(t, RemoveHook(hooks, "Stop", StopHookCommand))
	assert.True(t, RemoveHook(hooks, "SessionEnd", SessionEndHookCommand))
	assert.False(t, RemoveHook(hooks, "SessionEnd", SessionEndHookCommand), "removing twice changes nothing")
	assert.False(t, RemoveHook(hooks, "PreToolUse", StopHookCommand), "an absent event changes nothing")

	out, err := MarshalHooks(hooks)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"Stop": [{"matcher": "Bash", "futureGroupField": {"x": 1}, "hooks": [
			{"type": "command", "command": "echo user-hook", "if": "Bash(git push*)"}
		]}]
	}`, string(out), "other hooks keep their group, matcher, and unknown fields, and emptied events are dropped")
}

func TestInstallGlabHooks_ReplacesLegacyHooks(t *testing.T) {
	t.Parallel()

	hooks := map[string][]HookGroup{}
	AddHook(hooks, "Stop", legacyStopHookCommand)
	AddHook(hooks, "SessionEnd", legacySessionEndHookCommand)
	AddHook(hooks, "Stop", "echo user-hook")
	assert.True(t, LegacyHookPresent(hooks))
	assert.True(t, GlabHookPresent(hooks, "Stop"), "legacy hooks still sync")

	assert.True(t, InstallGlabHooks(hooks))
	assert.False(t, InstallGlabHooks(hooks), "installing again changes nothing")

	assert.False(t, LegacyHookPresent(hooks))
	assert.True(t, HookPresent(hooks, "Stop", StopHookCommand))
	assert.True(t, HookPresent(hooks, "SessionEnd", SessionEndHookCommand))
	assert.True(t, HookPresent(hooks, "Stop", "echo user-hook"))
	assert.Len(t, hooks["Stop"], 2, "the legacy Stop hook is replaced, not kept next to the new one")
	assert.Len(t, hooks["SessionEnd"], 1)
}

func TestRemoveGlabHooks_RemovesCurrentAndLegacyHooks(t *testing.T) {
	t.Parallel()

	hooks := map[string][]HookGroup{}
	AddHook(hooks, "Stop", StopHookCommand)
	AddHook(hooks, "Stop", legacyStopHookCommand)
	AddHook(hooks, "SessionEnd", legacySessionEndHookCommand)
	AddHook(hooks, "Stop", "echo user-hook")

	assert.True(t, RemoveGlabHooks(hooks))
	assert.False(t, RemoveGlabHooks(hooks), "removing twice changes nothing")

	assert.False(t, GlabHookPresent(hooks, "Stop"))
	assert.False(t, GlabHookPresent(hooks, "SessionEnd"))
	assert.True(t, HookPresent(hooks, "Stop", "echo user-hook"))
}
