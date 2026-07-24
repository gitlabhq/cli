package git

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/run"
)

func TestStandardGitCommand_Git_SetsLocale(t *testing.T) {
	// This test verifies that StandardGitCommand.Git() sets LC_ALL=C
	// to ensure Git output is always in English, regardless of user's locale.

	// Use InitGitRepoWithCommit to ensure we have a proper repo with commits
	// so git status returns predictable output
	InitGitRepoWithCommit(t)

	gitCmd := StandardGitCommand{}

	t.Run("git status outputs English messages", func(t *testing.T) {
		// Run git status which outputs human-readable messages
		output, err := gitCmd.Git("status", "--long")
		require.NoError(t, err)

		// Verify output contains English messages
		// These strings would be different in other locales if LC_ALL=C wasn't set:
		// - German: "Auf Branch" instead of "On branch"
		// - French: "Sur la branche" instead of "On branch"
		// - Chinese: "位于分支" instead of "On branch"
		// Note: Fresh repos may show "No commits yet" but repos with commits show "On branch"
		require.True(t,
			strings.Contains(output, "On branch") || strings.Contains(output, "No commits yet"),
			"Git output should be in English, got: %s", output)
	})

	t.Run("git status contains expected English phrases", func(t *testing.T) {
		// InitGitRepoWithCommit creates a repo with a commit and clean working tree,
		// so git status should output "nothing to commit" since there are no changes
		output, err := gitCmd.Git("status", "--long")
		require.NoError(t, err)

		// This is the key phrase that stack sync looks for to detect up-to-date branches
		// Without LC_ALL=C, it would be localized and string matching would fail:
		// - German: "nichts zu committen"
		// - French: "rien à valider"
		// - Chinese: "无文件要提交"
		require.Contains(t, output, "nothing to commit",
			"Git status should contain 'nothing to commit' for clean working tree")

		// Also verify it shows the branch is clean (another English phrase)
		require.Contains(t, output, "working tree clean",
			"Git status should contain 'working tree clean' for clean working tree")
	})

	t.Run("git commands work regardless of user's LANG setting", func(t *testing.T) {
		// Even if the user has a non-English LANG variable set in their environment,
		// the LC_ALL=C setting should override it and force English output.
		// We can't reliably test locale changes in the test environment,
		// but we can verify the command succeeds and produces valid output.
		output, err := gitCmd.Git("branch", "--show-current")
		require.NoError(t, err)
		require.NotEmpty(t, output, "Git command should produce output")
	})
}

// TestStandardGitCommand_GitWithIO_WrapsExitError verifies that when the
// subprocess fails, GitWithIO wraps the *exec.ExitError with git's stderr
// message instead of returning a bare "exit status N". Without the wrap the
// CLI prints a cryptic "Error: exit status 128" after git's real message has
// already been streamed live.
func TestStandardGitCommand_GitWithIO_WrapsExitError(t *testing.T) {
	// Cannot call t.Parallel(): we rely on t.Chdir to drop into a non-git
	// directory, and t.Chdir is incompatible with parallel tests.

	// Strip inherited GIT_DIR/GIT_WORK_TREE/GIT_INDEX_FILE so this test
	// really runs outside any git repo (e.g. when invoked via lefthook).
	unsetGitHookEnv(t)

	// chdir to a fresh temp dir that is NOT a git repo, so `git rev-parse`
	// exits with status 128 and prints a recognizable stderr message.
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	var stdout, stderr bytes.Buffer
	gitc := StandardGitCommand{}
	err := gitc.GitWithIO(&stdout, &stderr, "rev-parse", "--verify", "HEAD")

	require.Error(t, err, "git rev-parse outside a repo should fail")

	// The wrapped error must include git's stderr message — proves GitWithIO
	// captures the stderr stream alongside teeing it to the live writer.
	assert.Contains(t, err.Error(), "not a git repository",
		"wrapped error should include git's stderr message; got: %v", err)

	// The original *exec.ExitError must remain in the chain so callers using
	// errors.As / errors.Is keep working.
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr, "*exec.ExitError should remain in the chain")
	assert.Equal(t, 128, exitErr.ExitCode(), "git rev-parse outside a repo should exit 128")

	// The live stderr writer received the same message (io.MultiWriter
	// guarantees it; we assert it so a future regression dropping the tee
	// is caught here too).
	assert.Contains(t, stderr.String(), "not a git repository")
	assert.Empty(t, stdout.String(), "no stdout expected on this failure")
}

func TestStandardGitRunner_LatestCommitPreservesSubjectSpaces(t *testing.T) {
	InitGitRepo(t)
	configureGitConfig(t)
	require.NoError(t, os.WriteFile("randomfile", []byte("test"), 0o600))
	_, err := run.PrepareCmd(GitCommand("add", "randomfile")).Output()
	require.NoError(t, err)

	_, err = run.PrepareCmd(GitCommand("commit", "-m", "fix multiple words")).Output()
	require.NoError(t, err)

	commit, err := NewStandardGitRunner("").LatestCommit("HEAD")

	require.NoError(t, err)
	assert.Equal(t, "fix multiple words", commit.Title)
	assert.NotEmpty(t, commit.Sha)
}
