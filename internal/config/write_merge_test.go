//go:build !integration

package config

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

func reloadHostValue(t *testing.T, dir, host, key string) string {
	t.Helper()
	cfg, err := ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)
	v, _ := cfg.Get(host, key)
	return v
}

// TestWrite_AdoptsFresherCredentialsFromDisk reproduces the #8390 clobber: a
// process holding a stale in-memory copy writes for an unrelated reason and must
// not roll back credentials another process rotated on disk in the meantime.
func TestWrite_AdoptsFresherCredentialsFromDisk(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)

	dir := t.TempDir()
	past := time.Now().Add(-time.Hour).Format(time.RFC3339)
	future := time.Now().Add(time.Hour).Format(time.RFC3339)

	// Seed plaintext credentials on disk (no use_keyring => stored in the file).
	seed := NewBlankConfigInDir(dir)
	require.NoError(t, seed.Set("gitlab.com", "token", "access-old"))
	require.NoError(t, seed.Set("gitlab.com", "oauth2_refresh_token", "refresh-old"))
	require.NoError(t, seed.Set("gitlab.com", "oauth2_expiry_date", past))
	require.NoError(t, seed.Write())

	// A stale reader loads the config...
	stale, err := ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)

	// ...then another process rotates the token on disk.
	winner, err := ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)
	require.NoError(t, winner.Set("gitlab.com", "token", "access-new"))
	require.NoError(t, winner.Set("gitlab.com", "oauth2_refresh_token", "refresh-new"))
	require.NoError(t, winner.Set("gitlab.com", "oauth2_expiry_date", future))
	require.NoError(t, winner.Write())

	// The stale reader now writes for an unrelated reason.
	require.NoError(t, stale.Set("", "last_update_check_timestamp", future))
	require.NoError(t, stale.Write())

	// The rotated credentials survive, and the unrelated change is persisted.
	assert.Equal(t, "access-new", reloadHostValue(t, dir, "gitlab.com", "token"))
	assert.Equal(t, "refresh-new", reloadHostValue(t, dir, "gitlab.com", "oauth2_refresh_token"))
	assert.Equal(t, future, reloadHostValue(t, dir, "gitlab.com", "oauth2_expiry_date"))
	assert.Equal(t, future, reloadHostValue(t, dir, "", "last_update_check_timestamp"))
}

// TestWrite_KeepsNewerInMemoryCredentials is the converse: when this process
// holds the fresher credentials (it just rotated), Write must not regress to
// older on-disk values.
func TestWrite_KeepsNewerInMemoryCredentials(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)

	dir := t.TempDir()
	past := time.Now().Add(-time.Hour).Format(time.RFC3339)
	future := time.Now().Add(time.Hour).Format(time.RFC3339)

	seed := NewBlankConfigInDir(dir)
	require.NoError(t, seed.Set("gitlab.com", "token", "access-old"))
	require.NoError(t, seed.Set("gitlab.com", "oauth2_expiry_date", past))
	require.NoError(t, seed.Write())

	fresh, err := ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)
	require.NoError(t, fresh.Set("gitlab.com", "token", "access-new"))
	require.NoError(t, fresh.Set("gitlab.com", "oauth2_expiry_date", future))
	require.NoError(t, fresh.Write())

	assert.Equal(t, "access-new", reloadHostValue(t, dir, "gitlab.com", "token"))
	assert.Equal(t, future, reloadHostValue(t, dir, "gitlab.com", "oauth2_expiry_date"))
}

// TestWrite_KeyringHostAdoptsFresherExpiryWithoutPlaintextToken checks that for
// a keyring-backed host (token/refresh live in the keyring, absent from the
// file) the merge adopts only the fresher expiry and never writes a plaintext
// token into config.yml.
func TestWrite_KeyringHostAdoptsFresherExpiryWithoutPlaintextToken(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)

	dir := t.TempDir()
	past := time.Now().Add(-time.Hour).Format(time.RFC3339)
	future := time.Now().Add(time.Hour).Format(time.RFC3339)

	seed := NewBlankConfigInDir(dir)
	require.NoError(t, seed.Set("gitlab.com", "use_keyring", "true"))
	require.NoError(t, seed.Set("gitlab.com", "token", "access-old")) // -> keyring
	require.NoError(t, seed.Set("gitlab.com", "oauth2_expiry_date", past))
	require.NoError(t, seed.Write())

	stale, err := ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)

	winner, err := ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)
	require.NoError(t, winner.Set("gitlab.com", "token", "access-new")) // -> keyring
	require.NoError(t, winner.Set("gitlab.com", "oauth2_expiry_date", future))
	require.NoError(t, winner.Write())

	require.NoError(t, stale.Set("", "last_update_check_timestamp", future))
	require.NoError(t, stale.Write())

	// Fresher expiry adopted; the keyring token is current; the file never gains a
	// plaintext token node (Get resolves it from the keyring, so assert the raw
	// document instead).
	assert.Equal(t, future, reloadHostValue(t, dir, "gitlab.com", "oauth2_expiry_date"))
	storedToken, err := keyring.Get("glab:gitlab.com:token", "")
	require.NoError(t, err)
	assert.Equal(t, "access-new", storedToken)

	onDisk, err := ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)
	fileCfg, ok := onDisk.(*fileConfig)
	require.True(t, ok)
	hostCfg, err := fileCfg.configForHost("gitlab.com")
	require.NoError(t, err)
	_, err = hostCfg.GetStringValue("token")
	assert.Error(t, err, "config.yml should not contain a plaintext token node for a keyring host")
}

// TestWrite_DoesNotReadoptClearedCredentials guards the logout / credential-clear
// path: when a process clears the OAuth fields in memory but keeps the host
// entry (as `glab auth logout` does), the merge must not treat the now-empty
// in-memory expiry as "stale" and re-adopt the still-present on-disk credentials,
// which would silently undo the clear.
func TestWrite_DoesNotReadoptClearedCredentials(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)

	dir := t.TempDir()
	future := time.Now().Add(time.Hour).Format(time.RFC3339)

	// A file-backed host with credentials on disk.
	seed := NewBlankConfigInDir(dir)
	require.NoError(t, seed.Set("gitlab.com", "is_oauth2", "true"))
	require.NoError(t, seed.Set("gitlab.com", "token", "access-old"))
	require.NoError(t, seed.Set("gitlab.com", "oauth2_refresh_token", "refresh-old"))
	require.NoError(t, seed.Set("gitlab.com", "oauth2_expiry_date", future))
	require.NoError(t, seed.Write())

	// Clear the credential fields in memory (mirrors authutils.ClearAuthFields),
	// keeping the host entry, then persist.
	cfg, err := ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)
	for _, key := range []string{"token", "oauth2_refresh_token", "oauth2_expiry_date"} {
		require.NoError(t, cfg.Set("gitlab.com", key, ""))
	}
	require.NoError(t, cfg.Write())

	// The credentials must be gone from disk, not re-adopted by the merge.
	onDisk, err := ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)
	fileCfg, ok := onDisk.(*fileConfig)
	require.True(t, ok)
	hostCfg, err := fileCfg.configForHost("gitlab.com")
	require.NoError(t, err, "the host entry itself should be preserved")
	for _, key := range []string{"token", "oauth2_refresh_token", "oauth2_expiry_date"} {
		_, err := hostCfg.GetStringValue(key)
		assert.Error(t, err, "%s should stay cleared, not be re-adopted from disk", key)
	}
}
