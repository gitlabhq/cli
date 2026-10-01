//go:build !integration

package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An in-memory config (no directory behind it) must not persist local config to
// the surrounding git repository's .git/glab-cli/config.yml, even when Set()
// (which writes) is called from inside a git checkout.
func Test_InMemoryConfig_LocalSetDoesNotPersist(t *testing.T) {
	unsetGitHookEnv(t)
	dir := t.TempDir()
	require.NoError(t, exec.Command("git", "-C", dir, "init").Run())
	t.Chdir(dir)

	cfg := NewBlankConfig()
	local, err := cfg.Local()
	require.NoError(t, err)
	require.NoError(t, local.Set("git_protocol", "ssh"))

	_, statErr := os.Stat(filepath.Join(dir, ".git", "glab-cli", "config.yml"))
	assert.True(t, os.IsNotExist(statErr), "in-memory config must not persist local config to .git")
}

func Test_GitDir(t *testing.T) {
	gotRelative := filepath.Join(GitDir(true)...)
	gotAbsolute := filepath.Join(GitDir(false)...)
	absRelative, err := filepath.Abs(gotRelative)
	require.NoError(t, err)
	assert.Equal(t, gotAbsolute, absRelative)
}

// chdirTwoLevelsIntoNewRepo keeps the expected relative paths independent of the
// checkout the tests run from, such as a git worktree whose .git is a file.
func chdirTwoLevelsIntoNewRepo(t *testing.T) {
	t.Helper()
	unsetGitHookEnv(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, exec.Command("git", "-C", dir, "init").Run())
	sub := filepath.Join(dir, "a", "b")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	t.Chdir(sub)
}

func Test_LocalConfigDir(t *testing.T) {
	chdirTwoLevelsIntoNewRepo(t)
	got := LocalConfigDir()
	assert.ElementsMatch(t, []string{filepath.Join("..", "..", ".git"), "glab-cli"}, got)
}

func Test_LocalConfigFile(t *testing.T) {
	chdirTwoLevelsIntoNewRepo(t)
	expectedPath := filepath.Join("..", "..", ".git", "glab-cli", "config.yml")
	got := LocalConfigFile()
	assert.Equal(t, expectedPath, got)
}

// unsetGitHookEnv mirrors the helper in internal/git, which this package cannot
// import without a cycle. Under a git hook these variables point the tests' git
// commands at the repository running the hook instead of the temporary one.
func unsetGitHookEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"} {
		t.Setenv(key, "")
		require.NoError(t, os.Unsetenv(key))
	}
}
