//go:build integration

package checkout

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/shlex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// Test_MRCheckout_RealGitOutput_Integration runs the checkout command against
// a real `git` subprocess (no mocked GitRunner) so the captured stdout/stderr
// contain the actual bytes git emits for `fetch` and `checkout`. This proves
// the io.Writer plumbing introduced in commit e9bdc3ed (replacing gr.Git with
// gr.GitWithIO) reaches the user end-to-end, not just at the mock boundary.
//
// No GitLab test host is required: the project's clone URL returned by the
// mocked API points at a local bare repo prepared in t.TempDir(), and `git`
// fetches happily from a local path.
func Test_MRCheckout_RealGitOutput_Integration(t *testing.T) {
	originDir := filepath.Join(t.TempDir(), "origin.git")
	seedDir := filepath.Join(t.TempDir(), "seed")
	workDir := filepath.Join(t.TempDir(), "work")

	runGit(t, "", "init", "--bare", "-b", "main", originDir)

	// Seed: create a branch named feat-new-mr with one commit and push it
	// into the bare origin.
	require.NoError(t, os.MkdirAll(seedDir, 0o755))
	runGit(t, seedDir, "init", "-b", "feat-new-mr")
	runGit(t, seedDir, "config", "user.email", "test@example.com")
	runGit(t, seedDir, "config", "user.name", "Integration Test")
	require.NoError(t, os.WriteFile(filepath.Join(seedDir, "hello.txt"), []byte("hi\n"), 0o644))
	runGit(t, seedDir, "add", "hello.txt")
	runGit(t, seedDir, "commit", "-m", "seed commit")
	runGit(t, seedDir, "push", originDir, "feat-new-mr")
	remoteSHA := gitOutput(t, seedDir, "rev-parse", "HEAD")

	// Work dir: a normal (non-bare) repo with one commit on main. This is
	// where the command will fetch/checkout. cwd must point here for git
	// to operate on the right repo.
	require.NoError(t, os.MkdirAll(workDir, 0o755))
	runGit(t, workDir, "init", "-b", "main")
	runGit(t, workDir, "config", "user.email", "test@example.com")
	runGit(t, workDir, "config", "user.name", "Integration Test")
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "main.txt"), []byte("main\n"), 0o644))
	runGit(t, workDir, "add", "main.txt")
	runGit(t, workDir, "commit", "-m", "main commit")
	t.Chdir(workDir)

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockMergeRequests.EXPECT().
		GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:              123,
				IID:             123,
				ProjectID:       3,
				SourceProjectID: 3,
				SourceBranch:    "feat-new-mr",
				State:           "opened",
				SHA:             remoteSHA,
			},
		}, nil, nil)
	testClient.MockProjects.EXPECT().
		GetProject(gomock.Any(), gomock.Any()).
		Return(&gitlab.Project{
			ID:            3,
			SSHURLToRepo:  originDir,
			HTTPURLToRepo: originDir,
		}, nil, nil)

	ios, _, stdout, stderr := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(ios, cmdtest.WithGitLabClient(testClient.Client))

	cmd := NewCmdCheckout(f)
	argv, err := shlex.Split("123")
	require.NoError(t, err)
	cmd.SetArgs(argv)

	_, err = cmd.ExecuteC()
	require.NoError(t, err, "stdout=%q stderr=%q", stdout.String(), stderr.String())

	// Real `git fetch` writes its summary line (e.g. "* [new branch] ...")
	// to stderr. Real `git checkout` writes "Switched to ..." to stderr too.
	combined := stdout.String() + stderr.String()
	assert.Contains(t, combined, "[new branch]", "fetch output should be captured; stdout=%q stderr=%q", stdout.String(), stderr.String())
	assert.Contains(t, combined, "Switched to", "checkout output should be captured; stdout=%q stderr=%q", stdout.String(), stderr.String())

	// Sanity check: HEAD really points at feat-new-mr — proves the command
	// did more than just print, it actually performed the checkout.
	headRef := strings.TrimSpace(readFile(t, filepath.Join(workDir, ".git", "HEAD")))
	assert.Equal(t, "ref: refs/heads/feat-new-mr", headRef)
}

func runGit(t *testing.T, cwd string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %s failed: %s", strings.Join(args, " "), string(out))
}

func gitOutput(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %s failed: %s", strings.Join(args, " "), string(out))
	return strings.TrimSpace(string(out))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}

// setupDivergedRepo creates a bare origin repo with `feat-new-mr` pointing at
// one commit, and a working clone with a LOCAL `feat-new-mr` branch pointing
// at a DIFFERENT commit. Returns originDir, workDir, remoteSHA, localSHA.
func setupDivergedRepo(t *testing.T) (originDir, workDir, remoteSHA, localSHA string) {
	t.Helper()
	originDir = filepath.Join(t.TempDir(), "origin.git")
	seedDir := filepath.Join(t.TempDir(), "seed")
	workDir = filepath.Join(t.TempDir(), "work")

	runGit(t, "", "init", "--bare", "-b", "main", originDir)

	require.NoError(t, os.MkdirAll(seedDir, 0o755))
	runGit(t, seedDir, "init", "-b", "feat-new-mr")
	runGit(t, seedDir, "config", "user.email", "test@example.com")
	runGit(t, seedDir, "config", "user.name", "Integration Test")
	require.NoError(t, os.WriteFile(filepath.Join(seedDir, "remote.txt"), []byte("remote\n"), 0o644))
	runGit(t, seedDir, "add", "remote.txt")
	runGit(t, seedDir, "commit", "-m", "remote commit")
	runGit(t, seedDir, "push", originDir, "feat-new-mr")
	remoteSHA = gitOutput(t, seedDir, "rev-parse", "HEAD")

	require.NoError(t, os.MkdirAll(workDir, 0o755))
	runGit(t, workDir, "init", "-b", "main")
	runGit(t, workDir, "config", "user.email", "test@example.com")
	runGit(t, workDir, "config", "user.name", "Integration Test")
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "main.txt"), []byte("main\n"), 0o644))
	runGit(t, workDir, "add", "main.txt")
	runGit(t, workDir, "commit", "-m", "main commit")
	runGit(t, workDir, "checkout", "-b", "feat-new-mr")
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "local.txt"), []byte("local\n"), 0o644))
	runGit(t, workDir, "add", "local.txt")
	runGit(t, workDir, "commit", "-m", "local divergent commit")
	localSHA = gitOutput(t, workDir, "rev-parse", "HEAD")

	require.NotEqual(t, remoteSHA, localSHA, "test setup must produce divergent SHAs")
	return originDir, workDir, remoteSHA, localSHA
}

func mockCheckoutMR(t *testing.T, originDir string) *gitlabtesting.TestClient {
	t.Helper()
	remoteSHA := gitOutput(t, originDir, "rev-parse", "refs/heads/feat-new-mr")
	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockMergeRequests.EXPECT().
		GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:              123,
				IID:             123,
				ProjectID:       3,
				SourceProjectID: 3,
				SourceBranch:    "feat-new-mr",
				State:           "opened",
				SHA:             remoteSHA,
			},
		}, nil, nil)
	testClient.MockProjects.EXPECT().
		GetProject(gomock.Any(), gomock.Any()).
		Return(&gitlab.Project{
			ID:            3,
			SSHURLToRepo:  originDir,
			HTTPURLToRepo: originDir,
		}, nil, nil)
	return testClient
}

func execCheckout(t *testing.T, testClient *gitlabtesting.TestClient, cli string) error {
	t.Helper()
	ios, _, stdout, stderr := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(ios, cmdtest.WithGitLabClient(testClient.Client))
	_, err := cmdtest.ExecuteCommand(NewCmdCheckout(f), cli, stdout, stderr)
	return err
}

func Test_MRCheckout_Force_Integration(t *testing.T) {
	t.Run("--force, on target branch, clean working tree", func(t *testing.T) {
		originDir, workDir, remoteSHA, _ := setupDivergedRepo(t)
		t.Chdir(workDir)

		// Currently on feat-new-mr (the diverged local branch). Working tree clean.
		require.Equal(t, "feat-new-mr", gitOutput(t, workDir, "symbolic-ref", "--short", "HEAD"))

		testClient := mockCheckoutMR(t, originDir)
		require.NoError(t, execCheckout(t, testClient, "123 --force"))

		// Local branch ref must now match remote.
		assert.Equal(t, remoteSHA, gitOutput(t, workDir, "rev-parse", "HEAD"))
		// Reflog proves it was a reset (not branch delete + recreate).
		reflog := gitOutput(t, workDir, "reflog", "show", "feat-new-mr")
		assert.Contains(t, reflog, "reset")
	})

	t.Run("--force, on target branch, tracked modification refuses", func(t *testing.T) {
		originDir, workDir, _, localSHA := setupDivergedRepo(t)
		t.Chdir(workDir)

		// Introduce uncommitted change to a TRACKED file.
		require.NoError(t, os.WriteFile(filepath.Join(workDir, "local.txt"), []byte("local edit\n"), 0o644))

		testClient := mockCheckoutMR(t, originDir)
		err := execCheckout(t, testClient, "123 --force")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "changes that would be lost")
		// HEAD still at local SHA — refusal preserved branch state.
		assert.Equal(t, localSHA, gitOutput(t, workDir, "rev-parse", "HEAD"))
		// Modified file still has user's edit.
		assert.Equal(t, "local edit\n", readFile(t, filepath.Join(workDir, "local.txt")))
	})

	t.Run("--force, on target, unrelated untracked file is allowed", func(t *testing.T) {
		originDir, workDir, remoteSHA, _ := setupDivergedRepo(t)
		t.Chdir(workDir)

		// Untracked file with path NOT present in FETCH_HEAD's tree (which contains
		// only remote.txt). reset --hard leaves it alone.
		require.NoError(t, os.WriteFile(filepath.Join(workDir, "notes.txt"), []byte("personal notes\n"), 0o644))

		testClient := mockCheckoutMR(t, originDir)
		require.NoError(t, execCheckout(t, testClient, "123 --force"))

		// Branch matches remote.
		assert.Equal(t, remoteSHA, gitOutput(t, workDir, "rev-parse", "HEAD"))
		// Untracked file survived the reset.
		assert.Equal(t, "personal notes\n", readFile(t, filepath.Join(workDir, "notes.txt")))
	})

	t.Run("--force, on target, untracked file conflicts with incoming refuses", func(t *testing.T) {
		originDir, workDir, _, localSHA := setupDivergedRepo(t)
		t.Chdir(workDir)

		// Untracked file with same path as incoming tracked file (remote.txt is in
		// FETCH_HEAD's tree). reset --hard would silently overwrite — we must block.
		require.NoError(t, os.WriteFile(filepath.Join(workDir, "remote.txt"), []byte("LOCAL UNTRACKED\n"), 0o644))

		testClient := mockCheckoutMR(t, originDir)
		err := execCheckout(t, testClient, "123 --force")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "changes that would be lost")
		// HEAD untouched.
		assert.Equal(t, localSHA, gitOutput(t, workDir, "rev-parse", "HEAD"))
		// Local untracked content preserved.
		assert.Equal(t, "LOCAL UNTRACKED\n", readFile(t, filepath.Join(workDir, "remote.txt")))
	})

	t.Run("--force, not on target branch, updates ref and switches", func(t *testing.T) {
		originDir, workDir, remoteSHA, _ := setupDivergedRepo(t)
		// Switch back to main so we are NOT on the target branch.
		runGit(t, workDir, "checkout", "main")
		t.Chdir(workDir)
		require.Equal(t, "main", gitOutput(t, workDir, "symbolic-ref", "--short", "HEAD"))

		testClient := mockCheckoutMR(t, originDir)
		require.NoError(t, execCheckout(t, testClient, "123 --force"))

		// Now on feat-new-mr at remote SHA.
		assert.Equal(t, "feat-new-mr", gitOutput(t, workDir, "symbolic-ref", "--short", "HEAD"))
		assert.Equal(t, remoteSHA, gitOutput(t, workDir, "rev-parse", "HEAD"))
	})
}
