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

	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o644))
	_, err = SyncHookInstalled(path)
	require.Error(t, err)
}
