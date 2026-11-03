//go:build !integration

package dfcmd

import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
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

	// The Dependency Firewall summary already explains the failure, so the
	// wrapper must not also trigger fang's ERROR block. Wrapping SilentError
	// preserves the exit code while suppressing that duplicate output.
	assert.ErrorIs(t, err, cmdutils.SilentError, "wrapper failure must be silent so no ERROR block prints after the summary")
}

// TestChildNeverRanErrorIsNotSilenced covers the path where the package
// manager never actually executed (e.g. fork/exec failure). No summary box is
// rendered in that case, so the error must stay visible — silencing it would
// exit non-zero with nothing printed.
func TestChildNeverRanErrorIsNotSilenced(t *testing.T) {
	t.Chdir(t.TempDir())

	// A plain error (not an *exec.ExitError) models the child never running.
	fe := &fakeExecutor{exitErr: errors.New("fork/exec /usr/bin/npm: permission denied")}
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
	assert.Equal(t, 1, cmdErr.Code, "a generic run failure maps to exit code 1")
	assert.NotErrorIs(t, err, cmdutils.SilentError, "a child that never ran must surface a visible error, not be silenced")
}

// fetchingExecutor GETs url through the inspection proxy the child is handed,
// so the test observes whether the proxy trusted the registry. The proxy's
// leaf for an IP authority carries a DNS SAN only, so this child skips
// hostname verification on its own hop to the proxy.
type fetchingExecutor struct {
	fakeExecutor
	url    string
	status int
}

func (f *fetchingExecutor) ExecWithIO(ctx context.Context, _ string, _ []string, env []string, _ io.Reader, _, _ io.Writer) error {
	proxyURL, err := url.Parse(strings.TrimPrefix(envValue(env, "HTTPS_PROXY="), "HTTPS_PROXY="))
	if err != nil {
		return err
	}
	client := &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}, //nolint:gosec // child -> inspection proxy hop only
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	f.status = resp.StatusCode
	return nil
}

func TestRegistryTrustsCACertForRepoHost(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("GITLAB_CI", "")
	t.Setenv("GLAB_DF_FAKE_BLOCK", "npm:left-pad@1.3.0")

	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(upstream.Close)
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upstream.Certificate().Raw}), 0o600))

	cfg := config.NewFromString(heredoc.Docf(`
		hosts:
		  gitlab.example.com:
		    ca_cert: %s
	`, caPath))
	fe := &fetchingExecutor{url: upstream.URL + "/ping"}
	run := cmdtest.SetupCmdForTest(
		t,
		newTestCmd,
		false,
		cmdtest.WithExecutor(fe),
		cmdtest.WithConfig(cfg),
		cmdtest.WithBaseRepo("g", "p", "gitlab.example.com"),
	)

	_, err := run("install")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, fe.status, "the inspection proxy must trust the registry via ca_cert")
}
