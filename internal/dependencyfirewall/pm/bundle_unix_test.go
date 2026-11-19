//go:build !integration && unix

package pm

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCopyEntryRemovesAPartialCopy(t *testing.T) {
	t.Parallel()
	src, dst := t.TempDir(), t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "plugin", "a"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(src, "plugin", "a", "first"), []byte("x"), 0o600))
	require.NoError(t, syscall.Mkfifo(filepath.Join(src, "plugin", "fifo"), 0o600))

	err := copyEntry(filepath.Join(src, "plugin"), filepath.Join(dst, "plugin"))

	require.Error(t, err)
	assert.NoDirExists(t, filepath.Join(dst, "plugin"), "a failed copy must not leave a partial tree in the user's directory")
}

func TestBundleSetupWritesBackThroughASymlinkedConfig(t *testing.T) {
	p := newBundleProject(t, "")
	original, err := os.ReadFile(p.config)
	require.NoError(t, err)
	target := filepath.Join(p.root, "shared-bundle-config")
	require.NoError(t, os.Rename(p.config, target))
	require.NoError(t, os.Symlink(target, p.config))
	res, _ := bundleSetup(t, p.caPath)
	appConfig := lookupEnv(res.env, "BUNDLE_APP_CONFIG")
	require.NotEmpty(t, appConfig)
	generated, err := os.ReadFile(filepath.Join(appConfig, "config"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(appConfig, "config"), append(generated, "BUNDLE_PATH: \"vendor/bundle\"\n"...), 0o600))

	res.stop()

	info, err := os.Lstat(p.config)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeSymlink, "the user's symlinked config must stay a symlink")
	after, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, string(original)+"BUNDLE_PATH: \"vendor/bundle\"\n", string(after))
}
