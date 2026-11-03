//go:build !integration

package gomodproxy

import (
	"bytes"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goproxy/goproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/mod/module"
	modzip "golang.org/x/mod/zip"
)

func envValue(env []string, key string) (string, bool) {
	value, found := "", false
	for _, entry := range env {
		if k, v, ok := strings.Cut(entry, "="); ok && k == key {
			value, found = v, true
		}
	}
	return value, found
}

func TestFetcherEnvInheritsProcessEnvironment(t *testing.T) {
	t.Setenv("HOME", "/tmp/home-fixture")
	t.Setenv("GOCACHE", "/tmp/gocache-fixture")

	env := fetcherEnv(t.TempDir(), nil)
	for _, key := range []string{"PATH", "HOME", "GOCACHE"} {
		_, ok := envValue(env, key)
		assert.True(t, ok, "%s must be inherited", key)
	}
}

func TestFetcherEnvPinsFetcherSettings(t *testing.T) {
	t.Setenv("GOTOOLCHAIN", "go1.99.0")
	t.Setenv("GOWORK", "/elsewhere/go.work")
	t.Setenv("GOFLAGS", "-mod=vendor")
	t.Setenv("GOMODCACHE", "/elsewhere/modcache")
	t.Setenv("GO111MODULE", "off")
	tempDir := t.TempDir()

	env := fetcherEnv(tempDir, nil)

	toolchain, _ := envValue(env, "GOTOOLCHAIN")
	assert.Equal(t, "local", toolchain)
	work, _ := envValue(env, "GOWORK")
	assert.Equal(t, "off", work)
	flags, _ := envValue(env, "GOFLAGS")
	assert.Equal(t, "-mod=mod -modcacherw", flags)
	modcache, _ := envValue(env, "GOMODCACHE")
	assert.Equal(t, filepath.Join(tempDir, "modcache"), modcache)
	module, _ := envValue(env, "GO111MODULE")
	assert.Equal(t, "on", module)
}

func TestFetcherEnvOverrideReplacesInheritedValue(t *testing.T) {
	t.Setenv("GOSUMDB", "off")
	t.Setenv("GOPROXY", "https://inherited.example")

	env := fetcherEnv(t.TempDir(), map[string]string{
		"GOSUMDB": "sum.golang.org",
		"GOPROXY": "corp.example|https://user:pw@mirror.example,direct",
	})

	sumdb, _ := envValue(env, "GOSUMDB")
	assert.Equal(t, "sum.golang.org", sumdb)
	proxy, _ := envValue(env, "GOPROXY")
	assert.Equal(t, "corp.example|https://user:pw@mirror.example,direct", proxy)
}

func TestFetcherEnvOverrideCannotReplaceIsolationPins(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	env := fetcherEnv(tempDir, map[string]string{
		"GOMODCACHE":  "/elsewhere/modcache",
		"GOFLAGS":     "-mod=vendor",
		"GOTOOLCHAIN": "go1.99.0",
		"GOWORK":      "/elsewhere/go.work",
	})

	modcache, _ := envValue(env, "GOMODCACHE")
	assert.Equal(t, filepath.Join(tempDir, "modcache"), modcache)
	flags, _ := envValue(env, "GOFLAGS")
	assert.Equal(t, "-mod=mod -modcacherw", flags)
	toolchain, _ := envValue(env, "GOTOOLCHAIN")
	assert.Equal(t, "local", toolchain)
	work, _ := envValue(env, "GOWORK")
	assert.Equal(t, "off", work)
}

func fetcherOf(t *testing.T, b *Backend) *goFetcher {
	t.Helper()
	gp, ok := b.handler.(*goproxy.Goproxy)
	require.True(t, ok)
	fetcher, ok := gp.Fetcher.(*goFetcher)
	require.True(t, ok)
	return fetcher
}

func TestNewBackendConfiguresSumDBAndLogger(t *testing.T) {
	t.Parallel()
	b, err := NewBackend("/usr/bin/go", []string{"sum.golang.org"}, map[string]string{"GOSUMDB": "sum.golang.org"})
	require.NoError(t, err)
	t.Cleanup(b.Close)

	gp, ok := b.handler.(*goproxy.Goproxy)
	require.True(t, ok)
	assert.Equal(t, []string{"sum.golang.org"}, gp.ProxiedSumDBs)
	assert.NotNil(t, gp.Cacher, "a nil Cacher refetches upstream on every request")
	require.NotNil(t, gp.Logger)
	assert.IsType(t, debugHandler{}, gp.Logger.Handler())

	fetcher := fetcherOf(t, b)
	assert.Equal(t, "/usr/bin/go", fetcher.goBin)
	sumdb, _ := envValue(fetcher.env, "GOSUMDB")
	assert.Equal(t, "sum.golang.org", sumdb)
	entries, err := os.ReadDir(fetcher.dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "go must run where no go.mod applies")
}

func writeFileProxy(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(src, "go.mod"), []byte("module example.com/m\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(src, "m.go"), []byte("package m\n"), 0o600))

	root := t.TempDir()
	dir := filepath.Join(root, "example.com", "m", "@v")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "list"), []byte("v1.0.0\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "v1.0.0.info"), []byte(`{"Version":"v1.0.0"}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "v1.0.0.mod"), []byte("module example.com/m\n"), 0o600))

	zf, err := os.Create(filepath.Join(dir, "v1.0.0.zip"))
	require.NoError(t, err)
	require.NoError(t, modzip.CreateFromDir(zf, module.Version{Path: "example.com/m", Version: "v1.0.0"}, src))
	require.NoError(t, zf.Close())
	return root
}

func TestBackendCloseRemovesPopulatedModuleCache(t *testing.T) {
	t.Parallel()
	b, err := NewBackend("go", nil, map[string]string{
		"GOPROXY": "file://" + filepath.ToSlash(writeFileProxy(t)),
		"GOSUMDB": "off",
	})
	require.NoError(t, err)

	cmd := exec.Command("go", "mod", "download", "-json", "example.com/m@v1.0.0")
	cmd.Env = fetcherOf(t, b).env
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	_, err = os.Stat(filepath.Join(b.tempDir, "modcache", "example.com", "m@v1.0.0", "m.go"))
	require.NoError(t, err, "go mod download must populate the private module cache")

	b.Close()
	_, err = os.Stat(b.tempDir)
	assert.True(t, os.IsNotExist(err), "temp tree must not survive Close")
}

func serveBackend(t *testing.T, goproxy, target string) *httptest.ResponseRecorder {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	b, err := NewBackend("go", nil, map[string]string{"GOPROXY": goproxy, "GOSUMDB": "off"})
	require.NoError(t, err)
	t.Cleanup(b.Close)
	rec := httptest.NewRecorder()
	b.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, http.NoBody))
	return rec
}

func TestBackendFetchesEachRequestKindThroughGo(t *testing.T) {
	t.Parallel()
	goproxy := "file://" + filepath.ToSlash(writeFileProxy(t))
	for target, want := range map[string]string{
		"/example.com/m/@v/list":        "v1.0.0",
		"/example.com/m/@latest":        `"Version":"v1.0.0"`,
		"/example.com/m/@v/v1.0.0.mod":  "module example.com/m",
		"/example.com/m/@v/v1.0.0.info": `"Version":"v1.0.0"`,
	} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			rec := serveBackend(t, goproxy, target)
			assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), want)
		})
	}
}

func TestBackendAnswersMissingModuleWith404(t *testing.T) {
	t.Parallel()
	rec := serveBackend(t, "file://"+filepath.ToSlash(writeFileProxy(t)), "/example.com/missing/@v/v1.0.0.info")
	assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
}

var _ io.ReaderFrom = (*failureWriter)(nil)

func TestBackendFetchesWithModulesDisabledInEnvironment(t *testing.T) {
	t.Setenv("GO111MODULE", "off")
	rec := serveBackend(t, "file://"+filepath.ToSlash(writeFileProxy(t)), "/example.com/m/@v/v1.0.0.info")
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestBackendListsRetractedVersions(t *testing.T) {
	t.Parallel()
	root := writeFileProxy(t)
	dir := filepath.Join(root, "example.com", "m", "@v")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "list"), []byte("v1.0.0\nv1.1.0\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "v1.1.0.info"), []byte(`{"Version":"v1.1.0"}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "v1.1.0.mod"), []byte("module example.com/m\n\nretract v1.0.0\n"), 0o600))

	rec := serveBackend(t, "file://"+filepath.ToSlash(root), "/example.com/m/@v/list")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []string{"v1.0.0", "v1.1.0"}, strings.Fields(rec.Body.String()), "the child applies retractions itself")
}

func TestDebugHandlerRedactsURLUserinfo(t *testing.T) {
	t.Setenv("GLAB_DEBUG", "true")
	var out bytes.Buffer
	log.SetOutput(&out)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	slog.New(debugHandler{}).Error("failed to proxy checksum database",
		"error", `Get "https://t0ken@sum.corp.example/latest": dial tcp: connection refused`)
	assert.Contains(t, out.String(), "https://xxxxx@sum.corp.example/latest")
	assert.NotContains(t, out.String(), "t0ken")
}

func TestBackendServesCachedZipIntact(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	root := writeFileProxy(t)
	want, err := os.ReadFile(filepath.Join(root, "example.com", "m", "@v", "v1.0.0.zip"))
	require.NoError(t, err)
	b, err := NewBackend("go", nil, map[string]string{"GOPROXY": "file://" + filepath.ToSlash(root), "GOSUMDB": "off"})
	require.NoError(t, err)
	t.Cleanup(b.Close)
	srv := httptest.NewServer(b)
	t.Cleanup(srv.Close)

	for _, pass := range []string{"fetched", "cached"} {
		resp, err := http.Get(srv.URL + "/example.com/m/@v/v1.0.0.zip")
		require.NoError(t, err, pass)
		got, err := io.ReadAll(resp.Body)
		require.NoError(t, resp.Body.Close())
		require.NoError(t, err, pass)
		assert.Equal(t, http.StatusOK, resp.StatusCode, pass)
		assert.Equal(t, want, got, pass)
	}
}

func TestBackendAnswersUpstreamFailureWithItsMessage(t *testing.T) {
	t.Parallel()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())

	rec := serveBackend(t, "https://t0ken@"+addr, "/example.com/m/@v/v1.0.0.info")
	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Contains(t, rec.Body.String(), "connection refused")
	assert.NotContains(t, rec.Body.String(), "t0ken")
}

func TestIsNotFound(t *testing.T) {
	t.Parallel()
	// goproxy's GoFetcher treated every go failure as not-found; a 404 keeps
	// go probing shorter module paths for private repos and still carries
	// go's message.
	for msg, want := range map[string]bool{
		"corp.example/m@v1.0.0: invalid version: git ls-remote -q origin in /tmp/x: exit status 128:\n\tgit@corp.example: Permission denied (publickey).\n\tfatal: Could not read from remote repository.": true,
		"unrecognized import path \"corp.invalid/m\": https fetch: Get \"https://corp.invalid/m?go-get=1\": dial tcp: lookup corp.invalid: no such host":                                                   true,
		"example.com/m@v1.0.0: reading https://corp.example/example.com/m/@v/v1.0.0.info: 404 Not Found":                                                                                                   true,
		"example.com/m@v1.0.0: reading https://corp.example/example.com/m/@v/v1.0.0.info: 410 Gone":                                                                                                        true,
		"example.com/m@v9.9.9: invalid version: unknown revision v9.9.9":                                                                                                                                   true,
		"module example.com/m: no matching versions for query \"v2\"":                                                                                                                                      true,
		"unrecognized import path \"corp.example/m\": https fetch: 404":                                                                                                                                    true,
		"module lookup disabled by GOPROXY=off":                                                                                                                                                            true,
		"example.com/m@v1.0.0: reading file:///srv/proxy/example.com/m/@v/v1.0.0.info: no such file or directory":                                                                                          true,
		"example.com/m@v1.0.0: reading https://corp.example/example.com/m/@v/v1.0.0.info: 401 Unauthorized":                                                                                                false,
		"Get \"https://corp.example/example.com/m/@v/list\": dial tcp: connection refused":                                                                                                                 false,
	} {
		assert.Equal(t, want, isNotFound(msg), msg)
	}
}

func TestRedactUserinfo(t *testing.T) {
	t.Parallel()
	assert.Equal(t,
		`Get "https://xxxxx@corp.example/m/@v/list" and http://xxxxx@mirror.example/x`,
		redactUserinfo(`Get "https://t0ken@corp.example/m/@v/list" and http://alice:xxxxx@mirror.example/x`))
}
