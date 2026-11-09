//go:build !integration

package opencodeplugin

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	t.Setenv("XDG_CONFIG_HOME", "")
	path, err := Path()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".config", "opencode", "plugins", "glab-govern.js"), path)

	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	path, err = Path()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(xdg, "opencode", "plugins", "glab-govern.js"), path)
}

func TestDisplay(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	assert.Equal(t, filepath.Join("~", ".config", "opencode"), Display(filepath.Join(home, ".config", "opencode")))
	assert.Equal(t, "/elsewhere/opencode", Display("/elsewhere/opencode"))
}

func TestDetected(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	detected, err := Detected()
	require.NoError(t, err)
	assert.False(t, detected)

	dir, err := ConfigDir()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	detected, err = Detected()
	require.NoError(t, err)
	assert.True(t, detected)
}

func TestSourceStartsWithMarker(t *testing.T) {
	assert.True(t, bytes.HasPrefix(Source, markers[0]), "Check and Remove recognise the plugin by its first line")
}

func TestInstall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugins", FileName)

	changed, err := Install(path)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, Current, check(t, path))

	changed, err = Install(path)
	require.NoError(t, err)
	assert.False(t, changed, "installing the same version again changes nothing")

	for _, marker := range markers {
		require.NoError(t, os.WriteFile(path, append(bytes.Clone(marker), "\n// an older version\n"...), 0o644))
		assert.Equal(t, Outdated, check(t, path))
		changed, err = Install(path)
		require.NoError(t, err)
		assert.True(t, changed, "an older version is replaced")
		assert.Equal(t, Current, check(t, path))
	}
}

func TestInstall_RefusesForeignFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	require.NoError(t, os.WriteFile(path, []byte("// the user's own plugin\n"), 0o644))
	assert.Equal(t, Foreign, check(t, path))

	_, err := Install(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "was not installed by glab")

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "// the user's own plugin\n", string(data))
}

func TestRemove(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)

	removed, err := Remove(path)
	require.NoError(t, err)
	assert.False(t, removed, "nothing to remove")

	_, err = Install(path)
	require.NoError(t, err)
	removed, err = Remove(path)
	require.NoError(t, err)
	assert.True(t, removed)
	assert.NoFileExists(t, path)

	require.NoError(t, os.WriteFile(path, append(bytes.Clone(markers[1]), "\n// an older version\n"...), 0o644))
	removed, err = Remove(path)
	require.NoError(t, err)
	assert.True(t, removed, "a plugin installed before the rename to glab minder is removed")
	assert.NoFileExists(t, path)

	require.NoError(t, os.WriteFile(path, []byte("// the user's own plugin\n"), 0o644))
	removed, err = Remove(path)
	require.NoError(t, err)
	assert.False(t, removed)
	assert.FileExists(t, path, "a file glab did not install is kept")
}

func check(t *testing.T, path string) State {
	t.Helper()
	state, err := Check(path)
	require.NoError(t, err)
	return state
}
