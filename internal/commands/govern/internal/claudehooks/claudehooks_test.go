//go:build !integration

package claudehooks

import (
	"encoding/json"
	"os"
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
