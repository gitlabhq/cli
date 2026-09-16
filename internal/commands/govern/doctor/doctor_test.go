//go:build !integration

package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/claudehooks"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestCheckClaudeHooks_HooksPresent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	existingHooks := map[string][]claudehooks.HookGroup{
		"Stop": {
			{Hooks: []claudehooks.HookEntry{{Type: "command", Command: claudehooks.StopHookCommand}}},
		},
		"SessionEnd": {
			{Hooks: []claudehooks.HookEntry{{Type: "command", Command: claudehooks.SessionEndHookCommand}}},
		},
	}

	hooksJSON, err := json.Marshal(existingHooks)
	require.NoError(t, err)

	raw := map[string]json.RawMessage{"hooks": hooksJSON}
	require.NoError(t, claudehooks.WriteSettings(path, raw))

	// Override the settings path for testing
	t.Setenv("HOME", dir)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".claude"), 0o755))
	require.NoError(t, claudehooks.WriteSettings(filepath.Join(dir, ".claude", "settings.json"), raw))

	result := checkClaudeHooks()

	assert.True(t, result.ok)
	assert.Equal(t, "Stop and SessionEnd hooks installed", result.message)
}

func TestCheckClaudeHooks_HooksMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	// No settings file exists
	result := checkClaudeHooks()

	assert.True(t, result.ok, "missing Claude Code should be advisory")
	assert.Contains(t, result.message, "not detected")
}

func TestCheckClaudeHooks_StopHookMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	// Only SessionEnd hook present
	existingHooks := map[string][]claudehooks.HookGroup{
		"SessionEnd": {
			{Hooks: []claudehooks.HookEntry{{Type: "command", Command: claudehooks.SessionEndHookCommand}}},
		},
	}

	hooksJSON, err := json.Marshal(existingHooks)
	require.NoError(t, err)

	raw := map[string]json.RawMessage{"hooks": hooksJSON}
	require.NoError(t, claudehooks.WriteSettings(filepath.Join(dir, ".claude", "settings.json"), raw))

	result := checkClaudeHooks()

	assert.False(t, result.ok)
	assert.Contains(t, result.message, "Stop")
}

func TestCheckGlabInPath_NotFound(t *testing.T) {
	t.Setenv("PATH", "")

	result := checkGlabInPath()

	assert.False(t, result.ok)
	assert.Contains(t, result.message, "not found")
	assert.NotEmpty(t, result.fix)
}

func TestNewCmd_DoctorCommand(t *testing.T) {
	t.Parallel()
	ios, _, _, _ := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(ios)
	cmd := NewCmd(f)

	assert.Equal(t, "doctor", cmd.Name())
	assert.NotNil(t, cmd.Args)
	assert.NotNil(t, cmd.RunE)
}

func TestRunDoctor_ExitCodeOnFailure(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	ios, _, _, _ := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(ios)

	opts := &options{
		io:     ios,
		config: f.Config,
		apiClient: func(repoHost string) (*api.Client, error) {
			return nil, fmt.Errorf("not authenticated")
		},
		hostname: "gitlab.com",
	}

	err := runDoctor(t.Context(), opts)
	assert.ErrorIs(t, err, cmdutils.SilentError)
}
