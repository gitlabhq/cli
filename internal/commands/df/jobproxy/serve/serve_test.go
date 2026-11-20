//go:build !integration

package serve

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/session"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestServeRequiresStateDir(t *testing.T) {
	t.Parallel()
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
	_, err := exec("")
	assert.ErrorContains(t, err, `required flag(s) "state-dir" not set`)
}

// Not parallel: it sets CI_PROJECT_DIR.
func TestServeAnchorsTheCILogToTheProjectDir(t *testing.T) {
	t.Setenv("CI_PROJECT_DIR", "/builds/group/project")
	o := &options{}
	require.NoError(t, o.complete())
	assert.Equal(t, "/builds/group/project", o.baseDir)
}

// Not parallel: it clears CI_PROJECT_DIR and changes the working directory.
func TestServeAnchorsTheCILogToTheWorkingDirOutsideCI(t *testing.T) {
	t.Setenv("CI_PROJECT_DIR", "")
	dir := t.TempDir()
	t.Chdir(dir)
	o := &options{}
	require.NoError(t, o.complete())
	assert.Equal(t, dir, o.baseDir)
}

// Not parallel: it clears CI_PROJECT_DIR and GITLAB_CI and changes the working
// directory. Without the client_cert wiring the proxy starts and serve blocks
// until it is signalled, so the timeout turns that into a failure.
func TestServeFailsWhenClientCertForRepoHostCannotBeLoaded(t *testing.T) {
	t.Setenv("CI_PROJECT_DIR", "")
	t.Setenv("GITLAB_CI", "")
	t.Chdir(t.TempDir())

	dir := t.TempDir()
	certPath := filepath.Join(dir, "client.pem")
	keyPath := filepath.Join(dir, "client-key.pem")
	require.NoError(t, os.WriteFile(certPath, []byte("not a certificate"), 0o600))
	require.NoError(t, os.WriteFile(keyPath, []byte("not a key"), 0o600))
	cfg := config.NewFromString(heredoc.Docf(`
		hosts:
		  gitlab.example.com:
		    client_cert: %s
		    client_key: %s
	`, certPath, keyPath))
	stateDir := t.TempDir()
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithConfig(cfg),
		cmdtest.WithBaseRepo("g", "p", "gitlab.example.com"),
	)

	done := make(chan error, 1)
	go func() {
		_, err := exec("--state-dir " + stateDir)
		done <- err
	}()
	select {
	case err := <-done:
		require.ErrorContains(t, err, "failed to load client certificate")
	case <-time.After(10 * time.Second):
		t.Fatal("the job-level proxy started without the outbound proxy client certificate")
	}
	assert.NoFileExists(t, filepath.Join(stateDir, session.AddrFile), "the job-level proxy must not report ready")
}

// Not parallel: it clears CI_PROJECT_DIR and changes the working directory.
func TestServeBaseRepoErrorWithoutGitSaysGitIsRequired(t *testing.T) {
	t.Setenv("CI_PROJECT_DIR", "")
	t.Chdir(t.TempDir())
	executor := cmdtest.NewMockExecutor(gomock.NewController(t))
	executor.EXPECT().LookPath("git").Return("", errors.New("not found"))
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithExecutor(executor),
		cmdtest.WithBaseRepoError(errors.New("no GitLab remote found")))
	_, err := exec("--state-dir " + t.TempDir())
	var xerr *cmdutils.ExitError
	require.ErrorAs(t, err, &xerr)
	assert.Contains(t, xerr.Details, "failed to resolve GitLab project")
	assert.EqualError(t, err, "no GitLab remote found. git is required")
}

// Not parallel: it clears CI_PROJECT_DIR and changes the working directory.
func TestServeBaseRepoErrorWithGitInstalledHasNoGitHint(t *testing.T) {
	t.Setenv("CI_PROJECT_DIR", "")
	t.Chdir(t.TempDir())
	executor := cmdtest.NewMockExecutor(gomock.NewController(t))
	executor.EXPECT().LookPath("git").Return("/usr/bin/git", nil)
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithExecutor(executor),
		cmdtest.WithBaseRepoError(errors.New("no GitLab remote found")))
	_, err := exec("--state-dir " + t.TempDir())
	assert.EqualError(t, err, "no GitLab remote found")
}
