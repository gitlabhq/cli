package gomodproxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/goproxy/goproxy"

	"gitlab.com/gitlab-org/cli/internal/dbg"
)

type Backend struct {
	handler http.Handler
	tempDir string
}

func NewBackend(goBin string, proxiedSumDBs []string, envOverrides map[string]string, upstreamCAs []byte) (*Backend, error) {
	return newBackend(runtime.GOOS, goBin, proxiedSumDBs, envOverrides, upstreamCAs)
}

func newBackend(goos, goBin string, proxiedSumDBs []string, envOverrides map[string]string, upstreamCAs []byte) (*Backend, error) {
	tempDir, err := os.MkdirTemp("", "glab-df-gomodproxy-*")
	if err != nil {
		return nil, err
	}
	workDir := filepath.Join(tempDir, "work")
	if err := os.Mkdir(workDir, 0o700); err != nil {
		os.RemoveAll(tempDir)
		return nil, err
	}

	env := fetcherEnv(tempDir, envOverrides)
	var transport http.RoundTripper
	if len(upstreamCAs) > 0 {
		dropsSystemRoots := goos == "darwin" || goos == "windows" ||
			(os.Getenv("SSL_CERT_DIR") != "" && os.Getenv("SSL_CERT_FILE") == "")
		if dropsSystemRoots {
			dbg.Debugf("gomodproxy: the go fetcher keeps its own CA trust; SSL_CERT_FILE would drop its system roots\n")
		} else {
			caFile, err := writeUpstreamCAs(tempDir, upstreamCAs)
			if err != nil {
				os.RemoveAll(tempDir)
				return nil, err
			}
			env = append(env, "SSL_CERT_FILE="+caFile)
		}
		if transport, err = upstreamTransport(upstreamCAs); err != nil {
			os.RemoveAll(tempDir)
			return nil, err
		}
	}
	return &Backend{
		tempDir: tempDir,
		handler: &goproxy.Goproxy{
			Fetcher: &goFetcher{
				goBin: goBin,
				env:   env,
				dir:   workDir,
			},
			Cacher:        goproxy.DirCacher(filepath.Join(tempDir, "cache")),
			TempDir:       tempDir,
			ProxiedSumDBs: proxiedSumDBs,
			Transport:     transport,
			Logger:        slog.New(debugHandler{}),
		},
	}, nil
}

func writeUpstreamCAs(tempDir string, upstreamCAs []byte) (string, error) {
	path := filepath.Join(tempDir, "upstream-ca.pem")
	return path, os.WriteFile(path, upstreamCAs, 0o600)
}

func upstreamTransport(upstreamCAs []byte) (http.RoundTripper, error) {
	pool, err := x509.SystemCertPool()
	if err != nil {
		return nil, err
	}
	pool.AppendCertsFromPEM(upstreamCAs)
	t := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // net/http declares it as *http.Transport
	t.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	return t, nil
}

func (b *Backend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	failure := new(fetchFailure)
	r = r.WithContext(context.WithValue(r.Context(), fetchFailureKey{}, failure))
	b.handler.ServeHTTP(&failureWriter{ResponseWriter: w, failure: failure}, r)
}

type failureWriter struct {
	http.ResponseWriter
	failure  *fetchFailure
	replaced bool
}

func (w *failureWriter) WriteHeader(code int) {
	if code != http.StatusInternalServerError || w.failure.msg == "" {
		w.ResponseWriter.WriteHeader(code)
		return
	}
	w.replaced = true
	w.ResponseWriter.WriteHeader(http.StatusBadGateway)
	_, _ = io.WriteString(w.ResponseWriter, w.failure.msg+"\n")
}

func (w *failureWriter) Write(p []byte) (int, error) {
	if w.replaced {
		return len(p), nil
	}
	return w.ResponseWriter.Write(p)
}

func (w *failureWriter) ReadFrom(r io.Reader) (int64, error) {
	if w.replaced {
		return io.Copy(io.Discard, r)
	}
	return io.Copy(w.ResponseWriter, r)
}

func (b *Backend) Close() {
	if err := os.RemoveAll(b.tempDir); err != nil {
		dbg.Debugf("gomodproxy: removing %s: %v\n", b.tempDir, err)
	}
}

func fetcherEnv(tempDir string, overrides map[string]string) []string {
	settings := map[string]string{}
	maps.Copy(settings, overrides)
	maps.Copy(settings, map[string]string{
		"GO111MODULE": "on",
		"GOMODCACHE":  filepath.Join(tempDir, "modcache"),
		"GOFLAGS":     "-mod=mod -modcacherw",
		"GOTOOLCHAIN": "local",
		"GOWORK":      "off",
	})

	env := os.Environ()
	for _, k := range slices.Sorted(maps.Keys(settings)) {
		env = append(env, k+"="+settings[k])
	}
	return env
}

type debugHandler struct{}

func (debugHandler) Enabled(context.Context, slog.Level) bool { return true }

func (debugHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		b.WriteString(" " + a.String())
		return true
	})
	dbg.Debugf("gomodproxy: %s\n", redactUserinfo(b.String()))
	return nil
}

func (h debugHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h debugHandler) WithGroup(string) slog.Handler { return h }
