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

// newRepoWithWorktree creates a repository with one commit and a linked worktree,
// returning the absolute path of each. A worktree needs a commit to check out.
func newRepoWithWorktree(t *testing.T) (string, string) {
	t.Helper()
	unsetGitHookEnv(t)

	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	repo := filepath.Join(base, "repo")
	require.NoError(t, os.MkdirAll(repo, 0o755))

	git := func(args ...string) {
		t.Helper()
		full := append([]string{"-C", repo}, args...)
		out, cmdErr := exec.Command("git", full...).CombinedOutput()
		require.NoErrorf(t, cmdErr, "git %v: %s", args, out)
	}

	git("init")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "test")
	git("config", "commit.gpgsign", "false")
	git("commit", "--allow-empty", "-m", "initial")

	worktree := filepath.Join(base, "wt")
	git("worktree", "add", "--detach", worktree)

	return repo, worktree
}

func Test_GitCommonDir_MatchesGitDirInANormalCheckout(t *testing.T) {
	chdirTwoLevelsIntoNewRepo(t)

	assert.Equal(t, filepath.Join(GitDir(true)...), filepath.Join(GitCommonDir(true)...),
		"outside a worktree the two directories are the same")
}

// Git shares .git/config across every linked worktree and makes per-worktree
// config opt-in. Local glab config describes the repository, not the checkout, so
// it is shared the same way. Before this, a worktree resolved to
// .git/worktrees/<name>/glab-cli and silently saw none of the repository's config.
func Test_LocalConfigFile_IsSharedWithLinkedWorktrees(t *testing.T) {
	repo, worktree := newRepoWithWorktree(t)
	shared := filepath.Join(repo, ".git", "glab-cli", "config.yml")

	t.Chdir(repo)
	fromRepo, err := filepath.Abs(LocalConfigFile())
	require.NoError(t, err)
	assert.Equal(t, shared, fromRepo)

	t.Chdir(worktree)
	fromWorktree, err := filepath.Abs(LocalConfigFile())
	require.NoError(t, err)
	assert.Equal(t, shared, fromWorktree, "a linked worktree reads the repository's config")
}

// A worktree that already carries its own file keeps using it, so a config
// written before the shared default still applies where it was written.
func Test_LocalConfigFile_HonoursAnExistingPerWorktreeFile(t *testing.T) {
	repo, worktree := newRepoWithWorktree(t)

	t.Chdir(worktree)
	perWorktreeDir := filepath.Join(GitDir(false)...)
	require.NotEqual(t, filepath.Join(repo, ".git"), perWorktreeDir,
		"a linked worktree must have its own git dir for this case to mean anything")

	own := filepath.Join(perWorktreeDir, "glab-cli", "config.yml")
	require.NoError(t, os.MkdirAll(filepath.Dir(own), 0o755))
	require.NoError(t, os.WriteFile(own, []byte("editor: worktree-only\n"), 0o600))

	got, err := filepath.Abs(LocalConfigFile())
	require.NoError(t, err)
	assert.Equal(t, own, got)
}
