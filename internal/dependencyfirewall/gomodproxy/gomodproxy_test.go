//go:build !integration

package gomodproxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
)

type stubChecker struct {
	result policy.Result
	err    error
	calls  atomic.Int64
}

func (s *stubChecker) Check(context.Context, policy.Request) (policy.Result, error) {
	s.calls.Add(1)
	return s.result, s.err
}

type backendStub struct {
	mu     sync.Mutex
	hits   int
	paths  []string
	body   string
	status int
}

func (b *backendStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	b.hits++
	b.paths = append(b.paths, r.URL.Path)
	b.mu.Unlock()
	if b.status != 0 {
		w.WriteHeader(b.status)
	}
	body := b.body
	if body == "" {
		body = "upstream-bytes"
	}
	_, _ = io.WriteString(w, body)
}

func (b *backendStub) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.hits
}

func (b *backendStub) lastPath() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.paths) == 0 {
		return ""
	}
	return b.paths[len(b.paths)-1]
}

func startServer(t *testing.T, checker policy.Checker, backend http.Handler) *Server {
	t.Helper()
	s := New(checker, "group/proj", backend)
	require.NoError(t, s.Start())
	t.Cleanup(s.Stop)
	return s
}

type response struct {
	status      int
	contentType string
	body        string
}

func do(t *testing.T, method, rawURL string) response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, rawURL, http.NoBody)
	require.NoError(t, err)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	return response{
		status:      res.StatusCode,
		contentType: res.Header.Get("Content-Type"),
		body:        string(body),
	}
}

func get(t *testing.T, s *Server, suffix string) response {
	t.Helper()
	return do(t, http.MethodGet, s.BaseURL()+suffix)
}

func TestAllowedZipReachesBackendVerbatim(t *testing.T) {
	t.Parallel()
	checker := &stubChecker{result: policy.Result{}}
	backend := &backendStub{body: "zip-bytes"}
	s := startServer(t, checker, backend)

	res := get(t, s, "/github.com/pkg/errors/@v/v0.9.1.zip")

	assert.Equal(t, http.StatusOK, res.status)
	assert.Equal(t, "zip-bytes", res.body)
	assert.Equal(t, 1, backend.count())
	assert.Equal(t, int64(1), checker.calls.Load())
	assert.Empty(t, s.Verdicts(), "allowed coordinates are not recorded")
	assert.Equal(t, 1, s.CheckedZips())
}

func TestBlockedZipIsRefusedAndRecorded(t *testing.T) {
	t.Parallel()
	checker := &stubChecker{result: policy.Result{Verdict: verdict.Blocked, Reason: "policy violation"}}
	backend := &backendStub{}
	s := startServer(t, checker, backend)

	res := get(t, s, "/github.com/pkg/errors/@v/v0.9.1.zip")

	assert.Equal(t, http.StatusForbidden, res.status)
	assert.Equal(t, "text/plain; charset=utf-8", res.contentType)
	assert.Contains(t, res.body, "github.com/pkg/errors")
	assert.Contains(t, res.body, "policy violation")
	assert.Zero(t, backend.count(), "a blocked module must never reach the backend")

	entries := s.Verdicts()
	require.Len(t, entries, 1)
	assert.Equal(t, "github.com/pkg/errors", entries[0].Package)
	assert.Equal(t, "v0.9.1", entries[0].Version)
	assert.Equal(t, verdict.Blocked, entries[0].Verdict)
	assert.Equal(t, http.StatusForbidden, entries[0].Status)
	assert.Equal(t, 1, s.CheckedZips())
}

func TestWarnedZipIsServedAndRecorded(t *testing.T) {
	t.Parallel()
	checker := &stubChecker{result: policy.Result{Verdict: verdict.Warning, Reason: "deprecated"}}
	backend := &backendStub{}
	s := startServer(t, checker, backend)

	res := get(t, s, "/github.com/pkg/errors/@v/v0.9.1.zip")
	assert.Equal(t, http.StatusOK, res.status)
	assert.Equal(t, 1, backend.count())

	entries := s.Verdicts()
	require.Len(t, entries, 1)
	assert.Equal(t, verdict.Warning, entries[0].Verdict)
	assert.Equal(t, "deprecated", entries[0].Reason)
	assert.Zero(t, entries[0].Status)
	assert.Equal(t, 1, s.CheckedZips())
}

func TestCheckerErrorFailsClosed(t *testing.T) {
	t.Parallel()
	checker := &stubChecker{err: errors.New("boom")}
	backend := &backendStub{}
	s := startServer(t, checker, backend)

	res := get(t, s, "/github.com/pkg/errors/@v/v0.9.1.zip")

	assert.Equal(t, http.StatusForbidden, res.status)
	assert.Contains(t, res.body, "policy check failed")
	assert.Zero(t, backend.count())
	require.Len(t, s.Verdicts(), 1)
	assert.Equal(t, verdict.Blocked, s.Verdicts()[0].Verdict)
	assert.Equal(t, 1, s.CheckedZips())
}

func TestMetadataPassesThroughUngated(t *testing.T) {
	t.Parallel()
	checker := &stubChecker{result: policy.Result{Verdict: verdict.Blocked, Reason: "would block"}}
	backend := &backendStub{}
	s := startServer(t, checker, backend)

	for _, suffix := range []string{
		"/github.com/pkg/errors/@v/v0.9.1.info",
		"/github.com/pkg/errors/@v/v0.9.1.mod",
		"/github.com/pkg/errors/@v/list",
		"/github.com/pkg/errors/@latest",
		"/github.com/@v/v1.6.0.info",
		"/github.com/google/@v/v1.6.0.info",
	} {
		res := get(t, s, suffix)
		assert.Equal(t, http.StatusOK, res.status, suffix)
	}

	assert.Equal(t, 6, backend.count())
	assert.Zero(t, checker.calls.Load(), "metadata must not reach the firewall")
	assert.Empty(t, s.Verdicts())
	assert.Zero(t, s.CheckedZips())
}

func TestSumDBReachesBackend(t *testing.T) {
	t.Parallel()
	checker := &stubChecker{result: policy.Result{Verdict: verdict.Blocked}}
	backend := &backendStub{}
	s := startServer(t, checker, backend)

	res := get(t, s, "/sumdb/sum.golang.org/supported")
	assert.Equal(t, http.StatusOK, res.status)
	assert.Equal(t, 1, backend.count())
	assert.Equal(t, "/sumdb/sum.golang.org/supported", backend.lastPath())
	assert.Zero(t, checker.calls.Load())
}

func TestToolchainZipIsExempt(t *testing.T) {
	t.Parallel()
	checker := &stubChecker{result: policy.Result{Verdict: verdict.Blocked}}
	backend := &backendStub{}
	s := startServer(t, checker, backend)

	res := get(t, s, "/golang.org/toolchain/@v/v0.0.1-go1.27.0.darwin-arm64.zip")
	assert.Equal(t, http.StatusOK, res.status)
	rc := get(t, s, "/golang.org/toolchain/@v/v0.0.1-go1.27rc1.linux-amd64.zip")
	assert.Equal(t, http.StatusOK, rc.status)
	assert.Equal(t, 2, backend.count())
	assert.Zero(t, checker.calls.Load())
	assert.Empty(t, s.Verdicts())
	assert.Zero(t, s.CheckedZips())
}

func TestUnparseablePathIsRefusedNotDelegated(t *testing.T) {
	t.Parallel()
	checker := &stubChecker{}
	backend := &backendStub{}
	s := startServer(t, checker, backend)

	for _, suffix := range []string{
		"/example.com/M/@v/v1.0.0.zip",
		"/not-a-module-path",
		"//github.com/x/@v/v1.0.0.zip",
	} {
		res := get(t, s, suffix)
		assert.Equal(t, http.StatusNotFound, res.status, suffix)
	}
	assert.Zero(t, backend.count(), "an unclassifiable path must not reach the backend")
	assert.Zero(t, checker.calls.Load())
}

func TestNonGetIsRejected(t *testing.T) {
	t.Parallel()
	checker := &stubChecker{}
	backend := &backendStub{}
	s := startServer(t, checker, backend)

	res := do(t, http.MethodPost, s.BaseURL()+"/github.com/pkg/errors/@v/v0.9.1.zip")

	assert.Equal(t, http.StatusMethodNotAllowed, res.status)
	assert.Zero(t, backend.count())
}

func TestConcurrentBlocksRecordOneVerdict(t *testing.T) {
	t.Parallel()
	checker := &stubChecker{result: policy.Result{Verdict: verdict.Blocked, Reason: "nope"}}
	backend := &backendStub{}
	s := startServer(t, checker, backend)

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			res, err := http.Get(s.BaseURL() + "/github.com/pkg/errors/@v/v0.9.1.zip")
			if err == nil {
				_, _ = io.Copy(io.Discard, res.Body)
				_ = res.Body.Close()
			}
		})
	}
	wg.Wait()

	assert.Len(t, s.Verdicts(), 1, "verdicts dedupe by entry key")
}

func TestBaseURLEmptyBeforeStart(t *testing.T) {
	t.Parallel()
	s := New(&stubChecker{}, "group/proj", &backendStub{})
	assert.Empty(t, s.BaseURL())
}

func TestBaseURLCarriesPerRunPathToken(t *testing.T) {
	t.Parallel()
	s := startServer(t, &stubChecker{}, &backendStub{})
	other := startServer(t, &stubChecker{}, &backendStub{})

	host, token, found := strings.Cut(strings.TrimPrefix(s.BaseURL(), "http://"), "/")
	require.True(t, found, "BaseURL %q has no path token", s.BaseURL())
	assert.Equal(t, s.listener.Addr().String(), host)
	assert.NotEmpty(t, token)
	assert.NotContains(t, token, "/")

	_, otherToken, _ := strings.Cut(strings.TrimPrefix(other.BaseURL(), "http://"), "/")
	assert.NotEqual(t, token, otherToken, "the token is random per server")
}

func TestMissingOrWrongPathTokenIsRefused(t *testing.T) {
	t.Parallel()
	checker := &stubChecker{}
	backend := &backendStub{}
	s := startServer(t, checker, backend)
	root := "http://" + s.listener.Addr().String()

	missing := do(t, http.MethodGet, root+"/github.com/pkg/errors/@v/v0.9.1.info")
	assert.Equal(t, http.StatusNotFound, missing.status)

	wrong := do(t, http.MethodGet, root+"/not-the-token/github.com/pkg/errors/@v/v0.9.1.zip")
	assert.Equal(t, http.StatusNotFound, wrong.status)

	sumdb := do(t, http.MethodGet, root+"/sumdb/sum.golang.org/supported")
	assert.Equal(t, http.StatusNotFound, sumdb.status)

	assert.Zero(t, backend.count())
	assert.Zero(t, checker.calls.Load())
}

func TestForeignHostIsRefused(t *testing.T) {
	t.Parallel()
	checker := &stubChecker{}
	backend := &backendStub{}
	s := startServer(t, checker, backend)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, s.BaseURL()+"/github.com/pkg/errors/@v/v0.9.1.info", http.NoBody)
	require.NoError(t, err)
	req.Host = "evil.example"
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	assert.Equal(t, http.StatusNotFound, res.StatusCode)
	assert.Zero(t, backend.count())
	assert.Zero(t, checker.calls.Load())
}

func TestToolchainLookalikeZipIsChecked(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		suffix string
	}{
		{name: "submodule of toolchain", suffix: "/golang.org/toolchain/x/@v/v0.0.1-go1.27.0.darwin-arm64.zip"},
		{name: "ordinary version", suffix: "/golang.org/toolchain/@v/v1.0.0.zip"},
		{name: "toolchain prefix without platform", suffix: "/golang.org/toolchain/@v/v0.0.1-go1.27.0.zip"},
		{name: "toolchain prefix with non-release", suffix: "/golang.org/toolchain/@v/v0.0.1-evil.darwin-arm64.zip"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checker := &stubChecker{result: policy.Result{Verdict: verdict.Blocked, Reason: "not a toolchain"}}
			backend := &backendStub{}
			s := startServer(t, checker, backend)

			res := get(t, s, tc.suffix)

			assert.Equal(t, http.StatusForbidden, res.status)
			assert.Equal(t, int64(1), checker.calls.Load())
			assert.Zero(t, backend.count())
		})
	}
}

type hangingChecker struct {
	entered chan struct{}
}

func (h *hangingChecker) Check(ctx context.Context, _ policy.Request) (policy.Result, error) {
	close(h.entered)
	<-ctx.Done()
	return policy.Result{}, ctx.Err()
}

type cancellableBackend struct {
	entered chan struct{}
}

func (h *cancellableBackend) ServeHTTP(_ http.ResponseWriter, r *http.Request) {
	close(h.entered)
	<-r.Context().Done()
}

type wedgedBackend struct {
	entered chan struct{}
	release chan struct{}
}

func (h *wedgedBackend) ServeHTTP(http.ResponseWriter, *http.Request) {
	close(h.entered)
	<-h.release
}

func stopWithin(t *testing.T, s *Server, limit time.Duration) {
	t.Helper()
	stopped := make(chan struct{})
	go func() {
		s.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(limit):
		t.Fatalf("Stop did not return within %s", limit)
	}
}

func getZipInBackground(s *Server) (<-chan struct{}, func()) {
	client := &http.Client{Transport: &http.Transport{}}
	ch := make(chan struct{})
	go func() {
		defer close(ch)
		res, err := client.Get(s.BaseURL() + "/github.com/pkg/errors/@v/v0.9.1.zip")
		if err == nil {
			_ = res.Body.Close()
		}
	}()
	return ch, client.CloseIdleConnections
}

func TestStopCancelsHangingBackendRequest(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	backend := &cancellableBackend{entered: make(chan struct{})}
	s := New(&stubChecker{}, "group/proj", backend)
	require.NoError(t, s.Start())

	done, closeIdle := getZipInBackground(s)
	<-backend.entered

	stopWithin(t, s, drainTimeout/2)
	<-done
	closeIdle()
}

func TestStopIsBoundedWhileBackendIgnoresCancellation(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	backend := &wedgedBackend{entered: make(chan struct{}), release: make(chan struct{})}
	s := New(&stubChecker{}, "group/proj", backend)
	require.NoError(t, s.Start())

	done, closeIdle := getZipInBackground(s)
	<-backend.entered

	stopWithin(t, s, drainTimeout+2*time.Second)
	<-done
	close(backend.release)
	closeIdle()
}

func TestStopCancelsHangingPolicyCheckWithoutVerdict(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	checker := &hangingChecker{entered: make(chan struct{})}
	backend := &backendStub{}
	s := New(checker, "group/proj", backend)
	require.NoError(t, s.Start())

	done, closeIdle := getZipInBackground(s)
	<-checker.entered

	stopWithin(t, s, drainTimeout)
	<-done
	closeIdle()

	assert.Empty(t, s.Verdicts(), "a check cut short by Stop is not a firewall decision")
	assert.Zero(t, backend.count())
	assert.Zero(t, s.CheckedZips())
}

type ctxErrChecker struct{}

func (ctxErrChecker) Check(ctx context.Context, _ policy.Request) (policy.Result, error) {
	return policy.Result{}, ctx.Err()
}

// A request on a still-open keep-alive connection can reach check after
// Stop. Both select arms are then ready and Go picks one at random, so the
// check runs repeatedly to reach the arm that receives the failed result.
func TestCheckAfterStopIsNotADecision(t *testing.T) {
	t.Parallel()
	s := New(ctxErrChecker{}, "group/proj", &backendStub{})
	s.Stop()

	for range 100_000 {
		_, decided := s.check(request{module: "github.com/pkg/errors", version: "v0.9.1", kind: kindZip})
		require.False(t, decided, "a check ended by Stop is not a firewall decision")
	}
	assert.Zero(t, s.CheckedZips())
}

// wedgedChecker never sees cancellation, like the inner call
// policy.CachingChecker makes under context.WithoutCancel, and fails once
// released.
type wedgedChecker struct {
	entered chan struct{}
	release chan struct{}
}

func (c *wedgedChecker) Check(context.Context, policy.Request) (policy.Result, error) {
	close(c.entered)
	<-c.release
	return policy.Result{}, errors.New("policy API unreachable")
}

func TestStopAbandonsCheckThatIgnoresCancellation(t *testing.T) {
	ignore := goleak.IgnoreCurrent()

	inner := &wedgedChecker{entered: make(chan struct{}), release: make(chan struct{})}
	backend := &backendStub{}
	s := New(policy.NewCachingChecker(inner, nil), "group/proj", backend)
	require.NoError(t, s.Start())

	done, closeIdle := getZipInBackground(s)
	<-inner.entered

	stopWithin(t, s, drainTimeout/2)
	<-done
	closeIdle()
	close(inner.release)
	goleak.VerifyNone(t, ignore)

	assert.Empty(t, s.Verdicts(), "a check abandoned by Stop is not a firewall decision")
	assert.Zero(t, backend.count())
	assert.Zero(t, s.CheckedZips())
}

func TestStopIsIdempotent(t *testing.T) {
	t.Parallel()
	s := New(&stubChecker{}, "group/proj", &backendStub{})
	assert.NotPanics(t, s.Stop, "Stop before Start must be safe")

	s2 := New(&stubChecker{}, "group/proj", &backendStub{})
	require.NoError(t, s2.Start())
	assert.NotPanics(t, s2.Stop)
	assert.NotPanics(t, s2.Stop)
}

type panickingChecker struct{}

func (panickingChecker) Check(context.Context, policy.Request) (policy.Result, error) {
	panic("secret-token in a checker bug")
}

func TestPanickingCheckBlocksInsteadOfCrashing(t *testing.T) {
	t.Parallel()
	for name, checker := range map[string]policy.Checker{
		"when the checker panics":                  panickingChecker{},
		"when the checker panics behind the cache": policy.NewCachingChecker(panickingChecker{}, nil),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			backend := &backendStub{}
			s := startServer(t, checker, backend)

			res := get(t, s, "/github.com/pkg/errors/@v/v0.9.1.zip")

			assert.Equal(t, http.StatusForbidden, res.status)
			assert.Contains(t, res.body, "Blocked: glab internal error while checking github.com/pkg/errors@v0.9.1")
			assert.NotContains(t, res.body, "secret-token", "the panic value stays out of the response")
			assert.Zero(t, backend.count(), "a module whose check panicked must never reach the backend")
			entries := s.Verdicts()
			require.Len(t, entries, 1)
			assert.Equal(t, verdict.Blocked, entries[0].Verdict)
			assert.Contains(t, entries[0].Reason, "glab hit an internal error while checking github.com/pkg/errors@v0.9.1 and blocked it")
		})
	}
}
