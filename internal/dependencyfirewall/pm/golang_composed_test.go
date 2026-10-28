//go:build !integration

package pm

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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

func TestRunComposedGolangRefusesEnvFileGOPROXY(t *testing.T) {
	for _, goproxy := range []string{"https://corp.example", "https://user:secret@corp.example"} {
		t.Run(goproxy, func(t *testing.T) {
			isolateGo(t, "GOPROXY="+goproxy+"\n")
			t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
			t.Setenv("GLAB_DEBUG", "true")
			var debugLog bytes.Buffer
			log.SetOutput(&debugLog)
			t.Cleanup(func() { log.SetOutput(os.Stderr) })
			b := newModuleBackend(t, "")

			r := runRealGo(t, b, "mod", "download", fixtureModule+"@"+fixtureVersion)
			require.ErrorIs(t, r.err, errCustomGOPROXY)
			require.Len(t, r.exec.calls, 1, "go must not start")
			assert.Equal(t, "env", r.exec.calls[0][0])
			assert.Zero(t, b.built, "no request may leave the machine")
			for _, out := range []string{r.err.Error(), r.stderr, debugLog.String()} {
				assert.NotContains(t, out, "corp.example")
				assert.NotContains(t, out, "secret")
			}
		})
	}
}
