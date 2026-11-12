//go:build !integration

package pm

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb"
	"golang.org/x/mod/sumdb/dirhash"
	"golang.org/x/mod/sumdb/note"
	modzip "golang.org/x/mod/zip"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/cilog"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

const (
	fixtureModule  = "example.com/m"
	fixtureVersion = "v1.0.0"
	fixtureGoMod   = "module example.com/m\n"
)

type realGoExecutor struct {
	dir     string
	proxied atomic.Int64
	proxy   string
	calls   [][]string
	env     []string
}

func newRealGoExecutor(t *testing.T) *realGoExecutor {
	t.Helper()
	e := &realGoExecutor{dir: t.TempDir()}
	counter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		e.proxied.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(counter.Close)
	e.proxy = counter.URL
	return e
}

func (e *realGoExecutor) LookPath(file string) (string, error) { return exec.LookPath(file) }

func (e *realGoExecutor) ExecWithIO(ctx context.Context, name string, args, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
	e.calls = append(e.calls, args)
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = e.dir
	cmd.Env = append(slices.Clone(env), "HTTPS_PROXY="+e.proxy, "HTTP_PROXY="+e.proxy)
	e.env = cmd.Env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	return cmd.Run()
}

func isolateGo(t *testing.T, goenv string) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	envFile := filepath.Join(t.TempDir(), "goenv")
	require.NoError(t, os.WriteFile(envFile, []byte(goenv), 0o600))
	t.Setenv("GITLAB_CI", "")
	t.Setenv("GOENV", envFile)
	t.Setenv("GOPATH", t.TempDir())
	t.Setenv("GOMODCACHE", t.TempDir())
	t.Setenv("GOFLAGS", "-modcacherw")
	t.Setenv("GOTOOLCHAIN", "local")
	for _, k := range []string{"GOPROXY", "GONOPROXY", "GONOSUMDB", "GOPRIVATE", "GOSUMDB", "GOINSECURE", "GIT_HTTP_USER_AGENT"} {
		t.Setenv(k, "")
	}
}

func writeModuleFixture(t *testing.T) (string, []byte) {
	t.Helper()
	src := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(src, "go.mod"), []byte(fixtureGoMod), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(src, "m.go"), []byte("package m\n"), 0o600))

	root := t.TempDir()
	dir := filepath.Join(root, fixtureModule, "@v")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "list"), []byte(fixtureVersion+"\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, fixtureVersion+".info"), []byte(`{"Version":"`+fixtureVersion+`"}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, fixtureVersion+".mod"), []byte(fixtureGoMod), 0o600))
	zipPath := filepath.Join(dir, fixtureVersion+".zip")
	zf, err := os.Create(zipPath)
	require.NoError(t, err)
	require.NoError(t, modzip.CreateFromDir(zf, module.Version{Path: fixtureModule, Version: fixtureVersion}, src))
	require.NoError(t, zf.Close())

	zipHash, err := dirhash.HashZip(zipPath, dirhash.Hash1)
	require.NoError(t, err)
	modHash, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(fixtureGoMod)), nil
	})
	require.NoError(t, err)
	sum := fixtureModule + " " + fixtureVersion + " " + zipHash + "\n" +
		fixtureModule + " " + fixtureVersion + "/go.mod " + modHash + "\n"
	return root, []byte(sum)
}

type moduleBackend struct {
	fixtureGoBackend
	vkey string
}

func newModuleBackend(t *testing.T, sumDBName string) *moduleBackend {
	t.Helper()
	root, sum := writeModuleFixture(t)
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir(root)))
	b := &moduleBackend{}
	if sumDBName != "" {
		skey, vkey, err := note.GenerateKey(rand.Reader, sumDBName)
		require.NoError(t, err)
		b.vkey = vkey
		db := sumdb.NewServer(sumdb.NewTestServer(skey, func(string, string) ([]byte, error) { return sum, nil }))
		prefix := "/sumdb/" + sumDBName
		mux.HandleFunc(prefix+"/supported", func(http.ResponseWriter, *http.Request) {})
		mux.Handle(prefix+"/", http.StripPrefix(prefix, db))
	}
	b.handler = mux
	return b
}

type goRun struct {
	err    error
	stdout string
	stderr string
	dir    string
	exec   *realGoExecutor
	m      *golangManager
	b      *moduleBackend
}

func runRealGo(t *testing.T, b *moduleBackend, args ...string) goRun {
	t.Helper()
	ios, _, out, errOut := cmdtest.TestIOStreams()
	r := goRun{dir: t.TempDir(), exec: newRealGoExecutor(t), m: &golangManager{newBackend: b.newBackend}, b: b}
	r.err = Run(t.Context(), r.m, RunOptions{
		IO: ios, Executor: r.exec, BaseDir: r.dir, ProjectID: "group/proj", Args: args,
	})
	r.stdout, r.stderr = out.String(), errOut.String()
	return r
}

func TestRunComposedGolangAllowedModule(t *testing.T) {
	isolateGo(t, "")
	t.Setenv("GOSUMDB", "off")
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")

	r := runRealGo(t, newModuleBackend(t, ""), "mod", "download", "-json", fixtureModule+"@"+fixtureVersion)
	require.NoError(t, r.err, r.stderr)

	got, err := cilog.Load(r.dir)
	require.NoError(t, err)
	assert.Empty(t, got.Entries, "an allowed module records no verdict")
	assert.Equal(t, "go mod download -json "+fixtureModule+"@"+fixtureVersion, got.Session.Command, "the CI log names the command the user ran")
	assert.Equal(t, 1, r.m.checkedZips())
	assert.Zero(t, r.exec.proxied.Load(), "no request may reach the MITM proxy")
	assert.NotContains(t, r.stderr, goNothingCheckedNotice, "a run that checked a zip must not claim it checked nothing")
}

// TestRunComposedGolangTrustsConfiguredCAsUpstream proves the go subprocess
// that fetches modules verifies an HTTPS module proxy against glab's ca_cert
// and the package-manager CA variables, not only the system roots.
func TestRunComposedGolangTrustsConfiguredCAsUpstream(t *testing.T) {
	root, _ := writeModuleFixture(t)
	var reached atomic.Int64
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		http.FileServer(http.Dir(root)).ServeHTTP(w, r)
	}))
	upstream.Config.ErrorLog = log.New(io.Discard, "", 0)
	upstream.StartTLS()
	t.Cleanup(upstream.Close)
	upstreamCA := filepath.Join(t.TempDir(), "upstream-ca.pem")
	require.NoError(t, os.WriteFile(upstreamCA, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upstream.Certificate().Raw}), 0o600))

	cases := []struct {
		name       string
		caCertPath string
		requestsCA string
		wantOK     bool
	}{
		{name: "ca_cert", caCertPath: upstreamCA, wantOK: true},
		{name: "REQUESTS_CA_BUNDLE", requestsCA: upstreamCA, wantOK: true},
		{name: "neither", wantOK: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.wantOK && (runtime.GOOS == "darwin" || runtime.GOOS == "windows") {
				t.Skip("glab leaves the go subprocess on the platform store on " + runtime.GOOS)
			}
			isolateGo(t, "")
			t.Setenv("GOSUMDB", "off")
			t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
			for _, v := range upstreamCAVars {
				t.Setenv(v, "")
			}
			t.Setenv("REQUESTS_CA_BUNDLE", c.requestsCA)
			reached.Store(0)

			m := &golangManager{newBackend: func(goBin string, proxiedSumDBs []string, overrides map[string]string, upstreamCAs []byte) (http.Handler, func(), error) {
				overrides["GOPROXY"] = upstream.URL
				return newGoBackend(goBin, proxiedSumDBs, overrides, upstreamCAs)
			}}
			ios, _, _, errOut := cmdtest.TestIOStreams()
			exec := newRealGoExecutor(t)
			err := Run(t.Context(), m, RunOptions{
				IO: ios, Executor: exec, BaseDir: t.TempDir(), ProjectID: "group/proj",
				Args:       []string{"mod", "download", "-json", fixtureModule + "@" + fixtureVersion},
				CACertPath: c.caCertPath,
			})
			if c.wantOK {
				require.NoError(t, err, errOut.String())
				assert.Equal(t, 1, m.checkedZips())
				assert.Positive(t, reached.Load())
				return
			}
			require.Error(t, err)
			assert.Zero(t, m.checkedZips())
			assert.Zero(t, reached.Load(), "an untrusted module proxy must not be fetched")
		})
	}
}

func TestRunComposedGolangNoDownloadPrintsNotice(t *testing.T) {
	isolateGo(t, "")
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")

	r := runRealGo(t, newModuleBackend(t, ""), "env", "GOPROXY")
	require.NoError(t, r.err, r.stderr)
	assert.Zero(t, r.m.checkedZips())
	assert.Equal(t, 1, strings.Count(r.stderr, goNothingCheckedNotice))
}

func TestRunComposedGolangBlockedModule(t *testing.T) {
	cases := map[string]map[string]string{
		"plain":               {},
		"inherited GOFLAGS":   {"GOFLAGS": "-mod=mod -modcacherw"},
		"inherited GONOPROXY": {"GONOPROXY": "*"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			isolateGo(t, "")
			t.Setenv("GOSUMDB", "off")
			t.Setenv("GLAB_DF_FAKE_BLOCK", "golang:"+fixtureModule+"@"+fixtureVersion)
			for k, v := range env {
				t.Setenv(k, v)
			}

			r := runRealGo(t, newModuleBackend(t, ""), "mod", "download", "-json", fixtureModule+"@"+fixtureVersion)
			var ee *ExitError
			require.ErrorAs(t, r.err, &ee)
			assert.True(t, ee.ChildExited, "go itself must fail")
			assert.Contains(t, r.stdout+r.stderr, "blocked by GLAB_DF_FAKE_BLOCK", "go's output must carry the firewall's reason")

			got, err := cilog.Load(r.dir)
			require.NoError(t, err)
			require.Len(t, got.Entries, 1)
			assert.Equal(t, fixtureModule, got.Entries[0].Package)
			assert.Equal(t, verdict.Blocked, got.Entries[0].Verdict)
			assert.Zero(t, r.exec.proxied.Load(), "no request may reach the MITM proxy")
		})
	}
}

func TestRunComposedGolangCustomSumDBStaysOnLoopback(t *testing.T) {
	isolateGo(t, "")
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
	b := newModuleBackend(t, "sumdb.example")
	t.Setenv("GOSUMDB", b.vkey)

	r := runRealGo(t, b, "mod", "download", "-json", fixtureModule+"@"+fixtureVersion)
	require.NoError(t, r.err, r.stderr)
	assert.Equal(t, []string{"sumdb.example"}, b.proxiedSumDBs)
	assert.Equal(t, b.vkey, b.overrides["GOSUMDB"])
	assert.Equal(t, 1, r.m.checkedZips())
	assert.Zero(t, r.exec.proxied.Load(), "checksum lookups must go to the loopback server")
}

func TestRunComposedGolangEnvFileGOPRIVATE(t *testing.T) {
	isolateGo(t, "GOPRIVATE=corp.example/*\n")
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
	b := newModuleBackend(t, "")

	r := runRealGo(t, b, "env", "-json", "GOPROXY", "GONOPROXY", "GONOSUMDB")
	require.NoError(t, r.err, r.stderr)

	var child map[string]string
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &child))
	assert.Equal(t, "corp.example/*", child["GONOSUMDB"], "private paths must stay off the public checksum database")
	assert.Equal(t, "none", child["GONOPROXY"])
	assert.Equal(t, r.m.server.BaseURL(), child["GOPROXY"])

	assert.Equal(t, "corp.example/*", b.overrides["GOPRIVATE"])
	assert.Equal(t, "corp.example/*", b.overrides["GONOPROXY"])
	assert.Equal(t, "corp.example/*", b.overrides["GONOSUMDB"])
}

// go env -changed prints only the settings that differ from the defaults, so
// GOFLAGS=-changed would hide GOPROXY and the GOPRIVATE-derived GONOPROXY.
func TestRunComposedGolangGOFLAGSChanged(t *testing.T) {
	isolateGo(t, "GOPRIVATE=corp.example/*\n")
	t.Setenv("GOFLAGS", "-changed -modcacherw")
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
	b := newModuleBackend(t, "")

	r := runRealGo(t, b, "env", "GOPROXY")
	require.NoError(t, r.err, r.stderr)

	assert.Contains(t, r.stdout, r.m.server.BaseURL(), "the child's GOPROXY must be the gate")
	assert.Equal(t, "corp.example/*", b.overrides["GONOPROXY"], "private paths must not be fetched from the public proxy")
	assert.Equal(t, "corp.example/*", b.overrides["GONOSUMDB"])
}
