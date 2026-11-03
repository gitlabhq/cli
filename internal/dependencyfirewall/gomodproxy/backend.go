package gomodproxy

import (
	"context"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/goproxy/goproxy"

	"gitlab.com/gitlab-org/cli/internal/dbg"
)

// Backend serves allowed module proxy requests through goproxy and owns the
// temp tree goproxy fetches into.
type Backend struct {
	handler http.Handler
	tempDir string
}

// NewBackend builds a goproxy handler that fetches through goBin.
// proxiedSumDBs lists the checksum databases served under /sumdb/, as
// goproxy's "name" or "name URL" entries. envOverrides replaces the fetcher
// environment's value for each key it sets, GOPROXY included, except the
// isolation pins, which always win.
func NewBackend(goBin string, proxiedSumDBs []string, envOverrides map[string]string) (*Backend, error) {
	tempDir, err := os.MkdirTemp("", "glab-df-gomodproxy-*")
	if err != nil {
		return nil, err
	}
	workDir := filepath.Join(tempDir, "work")
	if err := os.Mkdir(workDir, 0o700); err != nil {
		os.RemoveAll(tempDir)
		return nil, err
	}

	return &Backend{
		tempDir: tempDir,
		handler: &goproxy.Goproxy{
			Fetcher: &goFetcher{
				goBin: goBin,
				env:   fetcherEnv(tempDir, envOverrides),
				dir:   workDir,
			},
			Cacher:        goproxy.DirCacher(filepath.Join(tempDir, "cache")),
			TempDir:       tempDir,
			ProxiedSumDBs: proxiedSumDBs,
			Logger:        slog.New(debugHandler{}),
		},
	}, nil
}

func (b *Backend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	failure := new(fetchFailure)
	r = r.WithContext(context.WithValue(r.Context(), fetchFailureKey{}, failure))
	b.handler.ServeHTTP(&failureWriter{ResponseWriter: w, failure: failure}, r)
}

// failureWriter turns goproxy's bare 500 for a failed fetch into a 502 that
// carries go's error, which the child prints as the server response.
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

// Close removes the temp tree, including modules fetched while resolving a
// request that was later blocked.
func (b *Backend) Close() {
	if err := os.RemoveAll(b.tempDir); err != nil {
		dbg.Debugf("gomodproxy: removing %s: %v\n", b.tempDir, err)
	}
}

// fetcherEnv starts from os.Environ because the fetcher's go needs PATH for
// git and HOME for SSH keys, .netrc, gitconfig, and the go env file.
// Settings are appended last because exec uses the last value per key. The
// isolation pins are applied after the overrides:
//   - a private GOMODCACHE, because sharing the child's deadlocks on the
//     module-cache lock and would let the child skip the zip request;
//   - -modcacherw, so Close can delete what go extracts read-only;
//   - GOTOOLCHAIN=local, so the fetcher never downloads a toolchain;
//   - GOWORK=off, so an absolute GOWORK cannot apply workspace replaces;
//   - GO111MODULE=on, because go list -m and go mod download refuse to run
//     with modules off.
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

// debugHandler routes goproxy's slog output to glab's debug log; on stderr
// it would corrupt the firewall summary.
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
