package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

// unwritableDir returns a path whose parent is a regular file, so MkdirAll
// fails with ENOTDIR. Chmod is ignored when tests run as root, as in CI.
func unwritableDir(t *testing.T) string {
	t.Helper()

	blocker := filepath.Join(t.TempDir(), "blocker")
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))
	return filepath.Join(blocker, "glab-cli")
}

func TestCredentialWriteProbe_PassesForAWritableDirectory(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)

	dir := t.TempDir()
	require.NoError(t, CredentialWriteProbe(NewBlankConfigInDir(dir), "gitlab.com"))

	_, err := os.Stat(filepath.Join(dir, writeProbeFilename))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestCredentialWriteProbe_FailsForAnUnwritableDirectory(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)

	err := CredentialWriteProbe(NewBlankConfigInDir(unwritableDir(t)), "gitlab.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not accepting writes")
}

// Write() is already a no-op for an in-memory config, so the probe must not
// invent a failure the real write would not hit.
func TestCredentialWriteProbe_PassesForAnInMemoryConfig(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)

	cfg := NewFromString(`
---
hosts:
  gitlab.com:
    token: token1
`)
	require.NoError(t, CredentialWriteProbe(cfg, "gitlab.com"))
}

// The directory is fine, but the refresh would write the rotated tokens to the
// keyring, so a keyring that rejects writes must still fail the probe.
func TestCredentialWriteProbe_FailsWhenKeyringModeHostHasNoWritableKeyring(t *testing.T) {
	keyring.MockInitWithError(assert.AnError)
	t.Cleanup(keyring.MockInit)

	cfg := NewFromStringInDir(`
---
hosts:
  gitlab.com:
    use_keyring: "true"
    token: token1
`, t.TempDir())

	err := CredentialWriteProbe(cfg, "gitlab.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "keyring")
}

// A failed use_keyring read must not be reported as "not keyring mode": that
// skips the keyring check and reports the write will succeed.
func TestCredentialWriteProbe_SurfacesUseKeyringReadFailure(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)

	err := CredentialWriteProbe(unreadableConfig{}, "gitlab.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "keyring")
	assert.ErrorIs(t, err, assert.AnError)
}

type unreadableConfig struct {
	Config
}

func (unreadableConfig) Get(string, string) (string, error) {
	return "", assert.AnError
}

// A file-mode host must not be blocked by an unavailable keyring: headless
// Linux and CI runners have none and refresh through the file fine.
func TestCredentialWriteProbe_IgnoresKeyringForAFileModeHost(t *testing.T) {
	keyring.MockInitWithError(assert.AnError)
	t.Cleanup(keyring.MockInit)

	cfg := NewFromStringInDir(`
---
hosts:
  gitlab.com:
    token: token1
`, t.TempDir())

	require.NoError(t, CredentialWriteProbe(cfg, "gitlab.com"))
}
