//go:build !integration

package configure

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestConfigWritesConfig(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
	out, err := exec("npm --repo-resolve virt --repo-deploy local")
	require.NoError(t, err)
	assert.Contains(t, out.OutBuf.String(), filepath.Join(".gitlab", "df", "config.json"))

	raw, err := os.ReadFile(filepath.Join(dir, ".gitlab", "df", "config.json"))
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"npm"`)
	assert.Contains(t, string(raw), `"repoResolve": "virt"`)
	assert.Contains(t, string(raw), `"repoDeploy": "local"`)
}

func TestConfigRequiresAtLeastOneFlag(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
	_, err := exec("npm")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one of the flags")

	_, statErr := os.Stat(filepath.Join(dir, ".gitlab", "df", "config.json"))
	assert.True(t, os.IsNotExist(statErr), "config must not be written when no flags are passed")
}

func TestConfigRequiresAPackageManager(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
	_, err := exec("--repo-resolve virt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "accepts 1 arg(s)")
}

func TestConfigRejectsUnsupportedPackageManager(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
	_, err := exec("cargo --repo-resolve virt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cargo")

	_, statErr := os.Stat(filepath.Join(dir, ".gitlab", "df", "config.json"))
	assert.True(t, os.IsNotExist(statErr), "config must not be written for an unsupported manager")
}

func TestConfigMergesOnlyProvidedFlags(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".gitlab", "df"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, ".gitlab", "df", "config.json"),
		[]byte(`{"npm":{"repoResolve":"oldR","repoDeploy":"oldD"}}`), 0o644))

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
	_, err := exec("npm --repo-resolve newR")
	require.NoError(t, err)

	raw, err := os.ReadFile(filepath.Join(dir, ".gitlab", "df", "config.json"))
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"repoResolve": "newR"`)
	assert.Contains(t, string(raw), `"repoDeploy": "oldD"`)
}

// The manager positional selects which block is written, so an unrelated
// manager's settings must survive untouched.
func TestConfigPreservesOtherManagers(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".gitlab", "df"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, ".gitlab", "df", "config.json"),
		[]byte(`{"pypi":{"repoResolve":"keepMe"}}`), 0o644))

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
	_, err := exec("npm --repo-resolve newR")
	require.NoError(t, err)

	raw, err := os.ReadFile(filepath.Join(dir, ".gitlab", "df", "config.json"))
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"keepMe"`)
	assert.Contains(t, string(raw), `"repoResolve": "newR"`)
}
