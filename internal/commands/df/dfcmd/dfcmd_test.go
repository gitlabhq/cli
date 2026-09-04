//go:build !integration

package dfcmd

import (
	"context"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/pm"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

type fakeExecutor struct {
	gotName string
	gotArgs []string
	gotEnv  []string
	exitErr error
}

func (f *fakeExecutor) LookPath(file string) (string, error) { return "/usr/bin/" + file, nil }

func (f *fakeExecutor) Exec(ctx context.Context, name string, args, env []string) error {
	return nil
}

func (f *fakeExecutor) ExecWithCombinedOutput(ctx context.Context, name string, args, env []string) ([]byte, error) {
	return nil, nil
}

func (f *fakeExecutor) ExecWithIO(ctx context.Context, name string, args, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
	f.gotName = name
	f.gotArgs = args
	f.gotEnv = env
	return f.exitErr
}

var _ cmdutils.Executor = (*fakeExecutor)(nil)

func envValue(env []string, prefix string) string {
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return e
		}
	}
	return ""
}

func testSpec() ProxySpec {
	return ProxySpec{
		Manager: pm.NPM,
		Use:     "npm <npm args>",
		Short:   "Run npm through the GitLab Dependency Firewall.",
		Long:    heredoc.Doc("Run npm behind the firewall."),
		Example: heredoc.Doc("glab dependency-firewall npm ..."),
	}
}

func newTestCmd(f cmdutils.Factory) *cobra.Command { return NewProxyCmd(f, testSpec()) }

func TestForwardsArgsAndRoutesThroughProxy(t *testing.T) {
	t.Chdir(t.TempDir())
	fe := &fakeExecutor{}
	run := cmdtest.SetupCmdForTest(
		t,
		newTestCmd,
		false,
		cmdtest.WithExecutor(fe),
		cmdtest.WithBaseRepo("g", "p", "gitlab.com"),
	)

	_, err := run("install --save-dev left-pad")
	require.NoError(t, err)

	assert.Equal(t, "/usr/bin/npm", fe.gotName)
	assert.Equal(t, []string{"install", "--save-dev", "left-pad"}, fe.gotArgs)
	v := envValue(fe.gotEnv, "HTTPS_PROXY=")
	require.NotEmpty(t, v, "child env must set HTTPS_PROXY")
	assert.Contains(t, v, "127.0.0.1", "HTTPS_PROXY must route through the local inspection proxy, got %q", v)
}

func TestExitErrorPropagates(t *testing.T) {
	t.Chdir(t.TempDir())

	badCmd := exec.Command("sh", "-c", "exit 3")
	realErr := badCmd.Run()
	var ee *exec.ExitError
	require.ErrorAs(t, realErr, &ee)

	fe := &fakeExecutor{exitErr: realErr}
	run := cmdtest.SetupCmdForTest(
		t,
		newTestCmd,
		false,
		cmdtest.WithExecutor(fe),
		cmdtest.WithBaseRepo("g", "p", "gitlab.com"),
	)

	_, err := run("install --save-dev left-pad")
	require.Error(t, err)

	var cmdErr *cmdutils.ExitError
	require.ErrorAs(t, err, &cmdErr)
	assert.Equal(t, 3, cmdErr.Code)
}
