//go:build !integration

package gaig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMachineFingerprint(t *testing.T) {
	t.Parallel()

	t.Run("returns a non-empty string", func(t *testing.T) {
		t.Parallel()
		fp, err := MachineFingerprint()
		require.NoError(t, err)
		assert.NotEmpty(t, fp)
	})

	t.Run("returns consistent value", func(t *testing.T) {
		t.Parallel()
		fp1, err := MachineFingerprint()
		require.NoError(t, err)
		fp2, err := MachineFingerprint()
		require.NoError(t, err)
		assert.Equal(t, fp1, fp2)
	})

	t.Run("returns a 64-character hex string", func(t *testing.T) {
		t.Parallel()
		fp, err := MachineFingerprint()
		require.NoError(t, err)
		assert.Len(t, fp, 64)
		assert.Regexp(t, `^[0-9a-f]{64}$`, fp)
	})
}

func TestSaveCachedIdentity(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	identity := &Identity{
		ID:        42,
		AgentType: "claude-code",
		CachedAt:  time.Now(),
	}

	err := SaveCachedIdentity(1000, "claude-code", identity)
	require.NoError(t, err)

	// Verify file was written
	path, err := identityCachePath(1000, "claude-code")
	require.NoError(t, err)
	_, err = os.Stat(path)
	assert.NoError(t, err, "cache file should exist")
}

func TestLoadCachedIdentity(t *testing.T) {
	t.Run("returns nil for missing identity", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("HOME", dir)

		loaded, err := LoadCachedIdentity(9999, "claude-code")
		require.NoError(t, err)
		assert.Nil(t, loaded)
	})

	t.Run("loads a valid cached identity", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("HOME", dir)

		identity := &Identity{
			ID:        42,
			AgentType: "claude-code",
			CachedAt:  time.Now(),
		}
		require.NoError(t, SaveCachedIdentity(1000, "claude-code", identity))

		loaded, err := LoadCachedIdentity(1000, "claude-code")
		require.NoError(t, err)
		require.NotNil(t, loaded)
		assert.Equal(t, identity.ID, loaded.ID)
		assert.Equal(t, identity.AgentType, loaded.AgentType)
	})

	t.Run("returns nil for expired identity", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("HOME", dir)

		expiredIdentity := &Identity{
			ID:        99,
			AgentType: "claude-code",
			CachedAt:  time.Now().Add(-25 * time.Hour),
		}
		path, err := identityCachePath(2000, "claude-code")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		data, err := json.Marshal(expiredIdentity)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, data, 0o600))

		loaded, err := LoadCachedIdentity(2000, "claude-code")
		require.NoError(t, err)
		assert.Nil(t, loaded, "expired identity should return nil")
	})

	t.Run("returns nil for revoked identity", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("HOME", dir)

		revokedAt := time.Now().Format(time.RFC3339)
		revokedIdentity := &Identity{
			ID:        88,
			AgentType: "claude-code",
			CachedAt:  time.Now(),
			RevokedAt: &revokedAt,
		}
		path, err := identityCachePath(3000, "claude-code")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		data, err := json.Marshal(revokedIdentity)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, data, 0o600))

		loaded, err := LoadCachedIdentity(3000, "claude-code")
		require.NoError(t, err)
		assert.Nil(t, loaded, "revoked identity should return nil")
	})
}
