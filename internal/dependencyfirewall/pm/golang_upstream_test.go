//go:build !integration

package pm

import (
	"bytes"
	"crypto/rand"
	"encoding/pem"
	"log"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/mod/sumdb"
	"golang.org/x/mod/sumdb/note"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/cilog"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

const (
	upstreamUser     = "alice"
	upstreamPassword = "s3cret-upstream-password"
	upstreamToken    = "t0ken-as-username"
)

type upstream struct {
	*httptest.Server
	mu     sync.Mutex
	paths  []string
	status map[string]int
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

func newUpstream(t *testing.T, secure bool, next http.Handler) *upstream {
	t.Helper()
	u := &upstream{status: map[string]int{}}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(sw, r)
		u.mu.Lock()
		defer u.mu.Unlock()
		u.paths = append(u.paths, r.URL.Path)
		u.status[r.URL.Path] = sw.code
	})
	if !secure {
		u.Server = httptest.NewServer(h)
		t.Cleanup(u.Close)
		return u
	}
	u.Server = httptest.NewTLSServer(h)
	t.Cleanup(u.Close)
	certFile := filepath.Join(t.TempDir(), "upstream.pem")
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: u.Certificate().Raw})
	require.NoError(t, os.WriteFile(certFile, cert, 0o600))
	t.Setenv("SSL_CERT_FILE", certFile)
	return u
}

func (u *upstream) seen() ([]string, map[string]int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return slices.Clone(u.paths), maps.Clone(u.status)
}

func (u *upstream) host() string {
	return strings.TrimPrefix(strings.TrimPrefix(u.URL, "https://"), "http://")
}

func moduleFiles(t *testing.T) http.Handler {
	t.Helper()
	root, _ := writeModuleFixture(t)
	return http.FileServer(http.Dir(root))
}

func requireBasicAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != upstreamUser || pass != upstreamPassword {
			w.Header().Set("WWW-Authenticate", `Basic realm="upstream"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func closedAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

func countFetcherEgress(t *testing.T, addr string) {
	t.Helper()
	t.Setenv("HTTPS_PROXY", addr)
	t.Setenv("HTTP_PROXY", addr)
}

func runUpstreamGo(t *testing.T, newBackend func(string, []string, map[string]string) (http.Handler, func(), error), args ...string) goRun {
	t.Helper()
	ios, _, out, errOut := cmdtest.TestIOStreams()
	r := goRun{dir: t.TempDir(), exec: newRealGoExecutor(t), m: &golangManager{newBackend: newBackend}}
	countFetcherEgress(t, r.exec.proxy)
	r.err = Run(t.Context(), r.m, RunOptions{
		IO: ios, Executor: r.exec, BaseDir: r.dir, ProjectID: "group/proj", Args: args,
	})
	r.stdout, r.stderr = out.String(), errOut.String()
	return r
}

func downloadFixture(t *testing.T) goRun {
	t.Helper()
	return runUpstreamGo(t, newGoBackend, "mod", "download", "-json", fixtureModule+"@"+fixtureVersion)
}

func TestRunUpstreamEnvFileGOPROXY(t *testing.T) {
	up := newUpstream(t, false, moduleFiles(t))
	isolateGo(t, "GOPROXY="+up.URL+"\n")
	t.Setenv("GOSUMDB", "off")
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")

	r := downloadFixture(t)
	require.NoError(t, r.err, r.stdout+r.stderr)

	paths, _ := up.seen()
	assert.Contains(t, paths, "/"+fixtureModule+"/@v/"+fixtureVersion+".zip", "the go env file's GOPROXY must be the upstream")
	assert.Zero(t, r.exec.proxied.Load(), "nothing may reach proxy.golang.org or any other host")
	assert.Equal(t, 1, r.m.checkedZips())
}

func TestRunUpstreamEmptyGOSUMDBFailsLikeGo(t *testing.T) {
	up := newUpstream(t, false, moduleFiles(t))
	isolateGo(t, "GOPROXY="+up.URL+"\nGOSUMDB=\n")
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")

	r := downloadFixture(t)
	var ee *ExitError
	require.ErrorAs(t, r.err, &ee)
	assert.True(t, ee.ChildExited, "go itself must fail")
	assert.Contains(t, r.stdout+r.stderr, "missing GOSUMDB", "the user must see the error plain go gives for this setting")
	assert.NotContains(t, r.stdout+r.stderr, "invalid GOSUMDB")
}

func TestRunUpstreamListFallsThroughOnlyOnNotFound(t *testing.T) {
	zip := "/" + fixtureModule + "/@v/" + fixtureVersion + ".zip"
	files := moduleFiles(t)
	a := newUpstream(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == zip {
			http.NotFound(w, r)
			return
		}
		files.ServeHTTP(w, r)
	}))
	b := newUpstream(t, false, files)
	isolateGo(t, "")
	t.Setenv("GOPROXY", a.URL+","+b.URL+",direct")
	t.Setenv("GOSUMDB", "off")
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")

	r := downloadFixture(t)
	require.NoError(t, r.err, r.stdout+r.stderr)

	aPaths, aStatus := a.seen()
	bPaths, _ := b.seen()
	assert.Contains(t, aPaths, zip)
	assert.Equal(t, []string{zip}, bPaths, "b sees only what a answered 404 for")
	assert.Equal(t, http.StatusNotFound, aStatus[zip])
	assert.Zero(t, r.exec.proxied.Load(), "direct must not be reached once b served the module")
}

func TestRunUpstreamSchemelessEntryUsesHTTPS(t *testing.T) {
	up := newUpstream(t, true, moduleFiles(t))
	isolateGo(t, "")
	t.Setenv("GOPROXY", up.host())
	t.Setenv("GOSUMDB", "off")
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")

	r := downloadFixture(t)
	require.NoError(t, r.err, r.stdout+r.stderr)
	paths, _ := up.seen()
	assert.Contains(t, paths, "/"+fixtureModule+"/@v/"+fixtureVersion+".zip", "a scheme-less entry is fetched over https")
}

func TestRunUpstreamDown(t *testing.T) {
	cases := []struct {
		sep          string
		directTried  bool
		wantInOutput string
	}{
		{",", false, "connection refused"},
		{"|", true, ""},
	}
	for _, c := range cases {
		t.Run(c.sep, func(t *testing.T) {
			isolateGo(t, "")
			t.Setenv("GOPROXY", "http://"+closedAddr(t)+c.sep+"direct")
			t.Setenv("GOSUMDB", "off")
			t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")

			r := downloadFixture(t)
			var ee *ExitError
			require.ErrorAs(t, r.err, &ee)
			assert.True(t, ee.ChildExited, "go itself must fail")
			assert.Contains(t, r.stdout+r.stderr, c.wantInOutput, "the child's error must say why the upstream failed")
			assert.NotContains(t, r.stdout+r.stderr, "internal server error")
			assert.Equal(t, c.directTried, r.exec.proxied.Load() > 0, "direct is tried only when the list falls back on errors")
			assert.Zero(t, r.m.checkedZips(), "no zip reached the gate")
		})
	}
}

func TestRunUpstreamNetrcCredentials(t *testing.T) {
	up := newUpstream(t, true, requireBasicAuth(moduleFiles(t)))
	isolateGo(t, "")
	netrc := filepath.Join(t.TempDir(), "netrc")
	require.NoError(t, os.WriteFile(netrc, []byte("machine "+up.host()+" login "+upstreamUser+" password "+upstreamPassword+"\n"), 0o600))
	t.Setenv("NETRC", netrc)
	t.Setenv("GOPROXY", up.URL)
	t.Setenv("GOSUMDB", "off")
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")

	r := downloadFixture(t)
	require.NoError(t, r.err, r.stdout+r.stderr)
	_, status := up.seen()
	assert.Equal(t, http.StatusOK, status["/"+fixtureModule+"/@v/"+fixtureVersion+".zip"], "the .netrc credentials must reach the upstream")
	assert.NotContains(t, r.stdout+r.stderr, upstreamPassword)
}

func TestRunUpstreamURLCredentials(t *testing.T) {
	up := newUpstream(t, true, requireBasicAuth(moduleFiles(t)))
	isolateGo(t, "")
	t.Setenv("GOPROXY", "https://"+upstreamUser+":"+upstreamPassword+"@"+up.host())
	t.Setenv("GOSUMDB", "off")
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")

	r := downloadFixture(t)
	require.NoError(t, r.err, r.stdout+r.stderr)
	_, status := up.seen()
	assert.Equal(t, http.StatusOK, status["/"+fixtureModule+"/@v/"+fixtureVersion+".zip"], "the URL's credentials must reach the upstream")
	assert.NotContains(t, r.stdout+r.stderr, upstreamPassword)
}

func captureDebugLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	t.Setenv("GLAB_DEBUG", "true")
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &buf
}

func TestRunUpstreamBlockedModuleWithCredentials(t *testing.T) {
	up := newUpstream(t, true, requireBasicAuth(moduleFiles(t)))
	isolateGo(t, "")
	debugLog := captureDebugLog(t)
	t.Setenv("GOPROXY", "https://"+upstreamUser+":"+upstreamPassword+"@"+up.host())
	t.Setenv("GOSUMDB", "off")
	t.Setenv("GLAB_DF_FAKE_BLOCK", "golang:"+fixtureModule+"@"+fixtureVersion)

	r := downloadFixture(t)
	var ee *ExitError
	require.ErrorAs(t, r.err, &ee)
	assert.True(t, ee.ChildExited, "go itself must fail")
	assert.Contains(t, r.stdout+r.stderr, "blocked by GLAB_DF_FAKE_BLOCK", "the gate refuses the module although the upstream serves it")

	got, err := cilog.Load(r.dir)
	require.NoError(t, err)
	require.Len(t, got.Entries, 1)
	assert.Equal(t, verdict.Blocked, got.Entries[0].Verdict)
	raw, err := os.ReadFile(filepath.Join(r.dir, ".gitlab", "df", "ci-log.json"))
	require.NoError(t, err)
	for name, out := range map[string]string{
		"verdict":   got.Entries[0].Reason,
		"ci log":    string(raw),
		"go output": r.stdout + r.stderr,
		"debug log": debugLog.String(),
	} {
		assert.NotContains(t, out, upstreamPassword, name)
	}
}

func TestRunUpstreamFailureHidesURLCredentials(t *testing.T) {
	for name, userinfo := range map[string]string{
		"user and password": upstreamUser + ":" + upstreamPassword,
		"token as username": upstreamToken,
	} {
		t.Run(name, func(t *testing.T) {
			isolateGo(t, "")
			debugLog := captureDebugLog(t)
			t.Setenv("GOPROXY", "https://"+userinfo+"@"+closedAddr(t))
			t.Setenv("GOSUMDB", "off")
			t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")

			r := downloadFixture(t)
			require.Error(t, r.err)
			assert.Contains(t, r.stdout+r.stderr, "connection refused")
			for name, out := range map[string]string{
				"error":     r.err.Error(),
				"go output": r.stdout + r.stderr,
				"debug log": debugLog.String(),
			} {
				assert.NotContains(t, out, upstreamPassword, name)
				assert.NotContains(t, out, upstreamToken, name)
			}
		})
	}
}

type recordingBackend struct {
	mu    sync.Mutex
	paths []string
}

func (b *recordingBackend) newBackend(goBin string, proxiedSumDBs []string, overrides map[string]string) (http.Handler, func(), error) {
	h, closeBackend, err := newGoBackend(goBin, proxiedSumDBs, overrides)
	if err != nil {
		return nil, nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		b.paths = append(b.paths, r.URL.Path)
		b.mu.Unlock()
		h.ServeHTTP(w, r)
	}), closeBackend, nil
}

func TestRunUpstreamCustomSumDBURLStaysOnLoopback(t *testing.T) {
	const name = "sumdb.example"
	_, sum := writeModuleFixture(t)
	skey, vkey, err := note.GenerateKey(rand.Reader, name)
	require.NoError(t, err)
	db := newUpstream(t, false, sumdb.NewServer(sumdb.NewTestServer(skey, func(string, string) ([]byte, error) { return sum, nil })))
	up := newUpstream(t, false, moduleFiles(t))
	isolateGo(t, "")
	t.Setenv("GOPROXY", up.URL)
	t.Setenv("GOSUMDB", vkey+" "+db.URL)
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
	backend := &recordingBackend{}

	r := runUpstreamGo(t, backend.newBackend, "mod", "download", "-json", fixtureModule+"@"+fixtureVersion)
	require.NoError(t, r.err, r.stdout+r.stderr)

	assert.Equal(t, vkey, lookupEnv(r.exec.env, "GOSUMDB"), "the child's GOSUMDB must not carry the URL")
	var lookups int
	for _, p := range backend.paths {
		if strings.HasPrefix(p, "/sumdb/"+name+"/lookup/") {
			lookups++
		}
	}
	assert.Positive(t, lookups, "the child's checksum lookups must go through the loopback server")
	assert.Zero(t, r.exec.proxied.Load(), "no checksum lookup may reach the MITM proxy")
}
