//go:build !integration

package pm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

type fakeGoExecutor struct {
	settings map[string]string
	envErr   error

	calls        [][]string
	preflightEnv []string
	childEnv     []string
}

func defaultGoSettings() map[string]string {
	return map[string]string{
		"GOPROXY":    "https://proxy.golang.org,direct",
		"GONOPROXY":  "",
		"GONOSUMDB":  "",
		"GOPRIVATE":  "",
		"GOSUMDB":    "sum.golang.org",
		"GOINSECURE": "",
	}
}

func (f *fakeGoExecutor) LookPath(file string) (string, error) { return "/bin/" + file, nil }

func (f *fakeGoExecutor) ExecWithIO(_ context.Context, _ string, args, env []string, _ io.Reader, stdout, stderr io.Writer) error {
	f.calls = append(f.calls, args)
	if len(args) > 0 && args[0] == "env" {
		f.preflightEnv = env
		if f.envErr != nil {
			_, _ = io.WriteString(stderr, "go: unknown GOENV file /home/u/go.env\n")
			return f.envErr
		}
		return json.NewEncoder(stdout).Encode(f.settings)
	}
	f.childEnv = env
	return nil
}

type allowChecker struct{}

func (allowChecker) Check(context.Context, policy.Request) (policy.Result, error) {
	return policy.Result{}, nil
}

type fixtureGoBackend struct {
	built         int
	closed        int
	goBin         string
	proxiedSumDBs []string
	overrides     map[string]string
	handler       http.Handler
	onClose       func()
}

func (b *fixtureGoBackend) newBackend(goBin string, proxiedSumDBs []string, overrides map[string]string) (http.Handler, func(), error) {
	b.built++
	b.goBin, b.proxiedSumDBs, b.overrides = goBin, proxiedSumDBs, overrides
	h := b.handler
	if h == nil {
		h = http.NotFoundHandler()
	}
	return h, func() {
		b.closed++
		if b.onClose != nil {
			b.onClose()
		}
	}, nil
}

func setupGolang(t *testing.T, m *golangManager, exec Executor) (setupResult, error) {
	t.Helper()
	ios, _, _, _ := cmdtest.TestIOStreams()
	res, err := m.Setup(t.Context(), setupInput{
		parent:    []string{"GOTOOLCHAIN=auto", "HOME=/home/u"},
		bin:       "/opt/go/bin/go",
		exec:      exec,
		checker:   allowChecker{},
		projectID: "group/proj",
		io:        ios,
	})
	if err == nil {
		t.Cleanup(res.stop)
	}
	return res, err
}

var _ setupManager = (*golangManager)(nil)

func TestGolangPassThroughMatcherMatchesNothing(t *testing.T) {
	t.Parallel()
	req, err := http.NewRequest(http.MethodGet, "https://proxy.golang.org/github.com/pkg/errors/@v/v0.9.1.zip", http.NoBody)
	require.NoError(t, err)
	assert.False(t, Golang().Matcher().Match(req).Matched)
}

// TestGolangEnvironmentFailsClosed pins the default the child gets if
// Setup's GOPROXY never reached it: off, not the inherited GOPROXY.
func TestGolangEnvironmentFailsClosed(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{
		"GOPROXY=off",
		"GONOPROXY=none",
		"GO111MODULE=on",
		"GIT_HTTP_USER_AGENT=",
	}, Golang().Environment("http://127.0.0.1:9999"))
}

func TestGolangRunChildEnvironment(t *testing.T) {
	t.Setenv("GITLAB_CI", "")
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
	t.Setenv("GIT_HTTP_USER_AGENT", "proxy.golang.org")
	t.Setenv("GOPROXY", "direct")
	t.Setenv("GONOPROXY", "*")
	t.Setenv("GONOSUMDB", "")
	require.NoError(t, os.Unsetenv("GONOSUMDB"))
	ios, _, _, _ := cmdtest.TestIOStreams()
	exec := &fakeGoExecutor{settings: defaultGoSettings()}
	backend := &fixtureGoBackend{}
	m := &golangManager{newBackend: backend.newBackend}

	err := Run(t.Context(), m, RunOptions{
		IO: ios, Executor: exec, BaseDir: t.TempDir(), ProjectID: "group/proj", Args: []string{"mod", "download"},
	})
	require.NoError(t, err)
	require.NotNil(t, exec.childEnv, "the child must start")

	goproxy := lookupEnv(exec.childEnv, "GOPROXY")
	assert.Equal(t, m.server.BaseURL(), goproxy)
	assert.True(t, strings.HasPrefix(goproxy, "http://127.0.0.1:"), "the server must be plain HTTP on loopback")
	assert.NotContains(t, goproxy, ",")
	assert.NotContains(t, goproxy, "|")
	assert.Equal(t, "none", lookupEnv(exec.childEnv, "GONOPROXY"))
	assert.Equal(t, "on", lookupEnv(exec.childEnv, "GO111MODULE"))
	assert.Contains(t, exec.childEnv, "GIT_HTTP_USER_AGENT=")
	assert.NotContains(t, exec.childEnv, "GIT_HTTP_USER_AGENT=proxy.golang.org")
	for _, e := range exec.childEnv {
		assert.False(t, strings.HasPrefix(e, "GONOSUMDB="), "GONOSUMDB must fall back to the user's value")
	}
}

func TestGolangSetupRunsPreflight(t *testing.T) {
	t.Parallel()
	exec := &fakeGoExecutor{settings: defaultGoSettings()}
	_, err := setupGolang(t, &golangManager{newBackend: (&fixtureGoBackend{}).newBackend}, exec)
	require.NoError(t, err)

	require.Len(t, exec.calls, 1)
	args := exec.calls[0]
	require.Len(t, args, 10)
	assert.Equal(t, []string{"env", "-C"}, args[:2])
	assert.Equal(t, []string{"-json", "GOPROXY", "GONOPROXY", "GONOSUMDB", "GOPRIVATE", "GOSUMDB", "GOINSECURE"}, args[3:])
	_, statErr := os.Stat(args[2])
	assert.True(t, os.IsNotExist(statErr), "the preflight directory must be removed")
	assert.Equal(t, "local", lookupEnv(exec.preflightEnv, "GOTOOLCHAIN"), "the preflight must not download a toolchain")
	assert.Equal(t, "/home/u", lookupEnv(exec.preflightEnv, "HOME"))
}

func TestGolangSetupPassesEffectiveSettingsToFetcher(t *testing.T) {
	t.Parallel()
	exec := &fakeGoExecutor{settings: map[string]string{
		"GOPROXY":    "https://proxy.golang.org,direct",
		"GONOPROXY":  "corp.example/*",
		"GONOSUMDB":  "corp.example/*",
		"GOPRIVATE":  "corp.example/*",
		"GOSUMDB":    "sum.golang.org",
		"GOINSECURE": "",
	}}
	backend := &fixtureGoBackend{}
	m := &golangManager{newBackend: backend.newBackend}

	res, err := setupGolang(t, m, exec)
	require.NoError(t, err)

	assert.Equal(t, "/opt/go/bin/go", backend.goBin)
	assert.Equal(t, []string{"sum.golang.org"}, backend.proxiedSumDBs)
	assert.Equal(t, map[string]string{
		"GONOPROXY":  "corp.example/*",
		"GONOSUMDB":  "corp.example/*",
		"GOPRIVATE":  "corp.example/*",
		"GOSUMDB":    "sum.golang.org",
		"GOINSECURE": "none",
	}, backend.overrides)
	assert.Equal(t, []string{"GOPROXY=" + m.server.BaseURL()}, res.env)
}

func TestGolangProxiedSumDBs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		gosumdb string
		want    []string
	}{
		{"sum.golang.org", []string{"sum.golang.org"}},
		{"off", nil},
		{"sumdb.example+1a2b3c4d+AW5vdCBhIHJlYWwga2V5", []string{"sumdb.example"}},
		{"sumdb.example+1a2b3c4d+AW5vdCBhIHJlYWwga2V5 https://mirror.example/sumdb", []string{"sumdb.example"}},
		{"", nil},
	}
	for _, c := range cases {
		t.Run(c.gosumdb, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, proxiedSumDBs(c.gosumdb))
		})
	}
}

func TestGolangRunGOPROXYOffStartsNoServer(t *testing.T) {
	t.Setenv("GITLAB_CI", "")
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
	ios, _, _, errOut := cmdtest.TestIOStreams()
	settings := defaultGoSettings()
	settings["GOPROXY"] = "off"
	exec := &fakeGoExecutor{settings: settings}
	backend := &fixtureGoBackend{}
	m := &golangManager{newBackend: backend.newBackend}

	err := Run(t.Context(), m, RunOptions{
		IO: ios, Executor: exec, BaseDir: t.TempDir(), ProjectID: "p", Args: []string{"mod", "download"},
	})
	require.NoError(t, err)
	assert.Equal(t, "off", lookupEnv(exec.childEnv, "GOPROXY"))
	assert.Zero(t, backend.built, "GOPROXY=off must start no backend")
	assert.Nil(t, m.server, "GOPROXY=off must start no server")
	assert.Zero(t, m.checkedZips())
	assert.Equal(t, 1, strings.Count(errOut.String(), goNothingCheckedNotice), "a run that checked nothing says so once")
}

func TestGolangRunFailsWhenPreflightFails(t *testing.T) {
	t.Setenv("GITLAB_CI", "")
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
	ios, _, _, _ := cmdtest.TestIOStreams()
	exec := &fakeGoExecutor{envErr: errors.New("exit status 1")}
	backend := &fixtureGoBackend{}

	err := Run(t.Context(), &golangManager{newBackend: backend.newBackend}, RunOptions{
		IO: ios, Executor: exec, BaseDir: t.TempDir(), ProjectID: "p", Args: []string{"mod", "download"},
	})
	require.ErrorContains(t, err, "exit status 1")
	require.ErrorContains(t, err, "unknown GOENV file", "go env's stderr is the user's only clue to why the preflight failed")
	assert.Nil(t, exec.childEnv, "go must not start when the preflight failed")
	assert.Zero(t, backend.built)
}

func TestGolangSetupRefusesCustomGOPROXY(t *testing.T) {
	t.Parallel()
	for _, goproxy := range []string{
		"https://corp.example",
		"https://user:secret@corp.example,direct",
		"https://proxy.golang.org",
		"https://proxy.golang.org|direct",
		"direct",
	} {
		t.Run(goproxy, func(t *testing.T) {
			t.Parallel()
			settings := defaultGoSettings()
			settings["GOPROXY"] = goproxy
			backend := &fixtureGoBackend{}
			m := &golangManager{newBackend: backend.newBackend}

			_, err := setupGolang(t, m, &fakeGoExecutor{settings: settings})
			require.ErrorIs(t, err, errCustomGOPROXY)
			assert.NotContains(t, err.Error(), "corp.example")
			assert.NotContains(t, err.Error(), "secret")
			assert.Zero(t, backend.built, "nothing may be fetched for a refused GOPROXY")
			assert.Nil(t, m.server)
		})
	}
}

func TestGolangSetupFailsWhenBackendFails(t *testing.T) {
	t.Parallel()
	m := &golangManager{newBackend: func(string, []string, map[string]string) (http.Handler, func(), error) {
		return nil, nil, errors.New("no temp dir")
	}}
	_, err := setupGolang(t, m, &fakeGoExecutor{settings: defaultGoSettings()})
	require.ErrorContains(t, err, "no temp dir")
	assert.Nil(t, m.server)
}

func TestGolangStopStopsServerBeforeClosingBackend(t *testing.T) {
	t.Parallel()
	backend := &fixtureGoBackend{}
	m := &golangManager{newBackend: backend.newBackend}
	res, err := setupGolang(t, m, &fakeGoExecutor{settings: defaultGoSettings()})
	require.NoError(t, err)

	var servingAtClose bool
	backend.onClose = func() {
		resp, err := http.Get(m.server.BaseURL() + "/example.com/m/@v/list")
		if err == nil {
			_ = resp.Body.Close()
			servingAtClose = true
		}
	}
	res.stop()
	assert.Equal(t, 1, backend.closed)
	assert.False(t, servingAtClose, "the server must stop before the backend closes")
}

func TestGolangManagersGetSeparateServersAndTempTrees(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	exec := &fakeGoExecutor{settings: defaultGoSettings()}
	a, ok := Golang().(*golangManager)
	require.True(t, ok)
	b, ok := Golang().(*golangManager)
	require.True(t, ok)

	resA, err := setupGolang(t, a, exec)
	require.NoError(t, err)
	resB, err := setupGolang(t, b, exec)
	require.NoError(t, err)

	assert.NotEqual(t, a.server.BaseURL(), b.server.BaseURL())
	trees, err := filepath.Glob(filepath.Join(tmp, "glab-df-gomodproxy-*"))
	require.NoError(t, err)
	assert.Len(t, trees, 2)

	resA.stop()
	resB.stop()
	left, err := os.ReadDir(tmp)
	require.NoError(t, err)
	assert.Empty(t, left, "stopping must leave no temp files behind")
}
