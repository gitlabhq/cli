//go:build !integration

package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Not parallel: it clears the state-directory variable.
func TestNewStateDirIsPrivateAndPerRun(t *testing.T) {
	t.Setenv(StateDirEnv, "")
	a, err := NewStateDir()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(a) })
	b, err := NewStateDir()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(b) })

	assert.NotEqual(t, a, b, "concurrent runs must not share a state directory")
	fi, err := os.Stat(a)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), fi.Mode().Perm())
}

// Not parallel: it sets the state-directory variable.
func TestNewStateDirHonorsAPinnedDir(t *testing.T) {
	pinned := filepath.Join(t.TempDir(), "state")
	t.Setenv(StateDirEnv, pinned)
	got, err := NewStateDir()
	require.NoError(t, err)
	assert.Equal(t, pinned, got)
	assert.DirExists(t, pinned)
}
