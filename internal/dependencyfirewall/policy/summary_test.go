//go:build !integration

package policy

import (
	"cmp"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/run"
)

const gatewayError = `{"message":"502 Bad Gateway"}`

// newFakeGitLab answers the enablement and evaluate endpoints of any project.
func newFakeGitLab(t *testing.T, enablement, evaluate http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/dependency_firewall/enablement") {
			enablement(w, r)
			return
		}
		evaluate(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func respond(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

// newNoRetryChecker returns a REST checker for srv whose client does not retry.
func newNoRetryChecker(t *testing.T, srv *httptest.Server) Checker {
	t.Helper()
	client, err := gitlab.NewClient("token", gitlab.WithBaseURL(srv.URL+"/api/v4"), gitlab.WithCustomRetryMax(0))
	require.NoError(t, err)
	return newRESTChecker(client)
}

// newRunChecker sets up the checker a run uses for group/proj under baseDir,
// with a client that does not retry.
func newRunChecker(t *testing.T, srv *httptest.Server, baseDir string) *CachingChecker {
	t.Helper()
	rest := newNoRetryChecker(t, srv)
	return NewCachingChecker(rest, nil, WithUnreachableFallback(LoadSummary(t.Context(), rest, "group/proj", baseDir, nil)))
}

func check(t *testing.T, c *CachingChecker) Result {
	t.Helper()
	res, err := c.Check(t.Context(), restReq("npm", "left-pad", "1.3.0"))
	require.NoError(t, err)
	return res
}

func expiresIn(d time.Duration) string {
	return time.Now().Add(d).UTC().Format(time.RFC3339)
}

func enablementJSON(fallback, expiresAt string) string {
	return fmt.Sprintf(`{"enabled":true,"unreachable_fallback":%q,"expires_at":%q}`, fallback, expiresAt)
}

func writeStoredSummary(t *testing.T, host, baseDir, projectID, expiresAt string) {
	t.Helper()
	path := summaryPath(baseDir)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	content := fmt.Sprintf(`{"host":%q,"project_id":%q,"unreachable_fallback":"warn","expires_at":%q}`, host, projectID, expiresAt)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

// repoDir returns a new Git repository, where an untracked stored summary applies.
func repoDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, git.GitCommand("init", "-q", dir).Run())
	return dir
}

func hostOf(srv *httptest.Server) string { return strings.TrimPrefix(srv.URL, "http://") }

// handshakeTimeout mirrors net/http's unexported tlsHandshakeTimeoutError.
type handshakeTimeout struct{}

func (handshakeTimeout) Error() string   { return "net/http: TLS handshake timeout" }
func (handshakeTimeout) Timeout() bool   { return true }
func (handshakeTimeout) Temporary() bool { return true }

func TestIsUnreachable(t *testing.T) {
	t.Parallel()
	status := func(code int) error { return &gitlab.ErrorResponse{StatusCode: code} }
	dial := func(err error) error {
		return &url.Error{Op: "Post", URL: "https://gitlab.example.com", Err: &net.OpError{Op: "dial", Net: "tcp", Err: err}}
	}
	_, err := net.DialTimeout("tcp", "127.0.0.1:1", time.Nanosecond)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	dialTimeout := &url.Error{Op: "Post", URL: "https://gitlab.example.com", Err: err}
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"500", status(http.StatusInternalServerError), true},
		{"502", status(http.StatusBadGateway), true},
		{"429", status(http.StatusTooManyRequests), true},
		{"no status code", status(0), true},
		{"501", status(http.StatusNotImplemented), false},
		{"401", status(http.StatusUnauthorized), false},
		{"connection refused", dial(syscall.ECONNREFUSED), true},
		{"temporary DNS failure", dial(&net.DNSError{IsTemporary: true}), true},
		{"unknown host", dial(&net.DNSError{IsNotFound: true}), false},
		{"dial timeout", dialTimeout, true},
		{"read timeout", &url.Error{Op: "Post", Err: &net.OpError{Op: "read", Err: os.ErrDeadlineExceeded}}, true},
		{"reset mid-request", &url.Error{Op: "Post", Err: &net.OpError{Op: "read", Err: syscall.ECONNRESET}}, false},
		{"untrusted certificate", &url.Error{Op: "Post", Err: x509.UnknownAuthorityError{}}, false},
		{"failed token refresh", &oauth2.RetrieveError{Response: &http.Response{StatusCode: http.StatusServiceUnavailable}}, false},
		{"text that only looks like a dial failure", errors.New("dial tcp: connection refused"), false},
		{"TLS handshake timeout", handshakeTimeout{}, true},
		{"TLS handshake timeout from the HTTP client", &url.Error{Op: "Post", Err: handshakeTimeout{}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isUnreachable(t.Context(), fmt.Errorf("wrapped: %w", tt.err)))
		})
	}

	expired, cancel := context.WithDeadline(t.Context(), time.Now())
	cancel()
	assert.False(t, isUnreachable(expired, &url.Error{Op: "Post", Err: context.DeadlineExceeded}), "deadline exceeded")
	assert.False(t, isUnreachable(expired, dialTimeout), "dial timeout after the deadline")
}

func TestFallbackAppliesWhenGitLabIsUnreachable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		fallback    string
		wantVerdict verdict.Verdict
	}{
		{"allow", verdict.Allowed},
		{"warn", verdict.Warning},
		{"block", verdict.Blocked},
	}
	for _, tt := range tests {
		t.Run(tt.fallback, func(t *testing.T) {
			t.Parallel()
			gl := newFakeGitLab(t, respond(http.StatusOK, enablementJSON(tt.fallback, expiresIn(time.Hour))), respond(http.StatusBadGateway, gatewayError))

			res := check(t, newRunChecker(t, gl, t.TempDir()))

			assert.Equal(t, tt.wantVerdict, res.Verdict, "got %+v", res)
			assert.Equal(t, tt.wantVerdict == verdict.Blocked, res.CheckFailed)
		})
	}
}

func TestStoredSummaryAppliesOnlyWhenValidForTheProject(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		host        string // empty means the fake GitLab's host
		projectID   string // empty leaves no stored summary
		expiresIn   time.Duration
		wantVerdict verdict.Verdict
	}{
		{"unexpired summary for this project applies", "", "group/proj", time.Hour, verdict.Warning},
		{"expired summary blocks", "", "group/proj", -time.Hour, verdict.Blocked},
		{"summary for another project blocks", "", "group/other", time.Hour, verdict.Blocked},
		{"summary for another host blocks", "gitlab.example.com", "group/proj", time.Hour, verdict.Blocked},
		{"missing summary blocks", "", "", 0, verdict.Blocked},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gl := newFakeGitLab(t, respond(http.StatusBadGateway, gatewayError), respond(http.StatusBadGateway, gatewayError))
			baseDir := repoDir(t)
			if tt.projectID != "" {
				writeStoredSummary(t, cmp.Or(tt.host, hostOf(gl)), baseDir, tt.projectID, expiresIn(tt.expiresIn))
			}

			res := check(t, newRunChecker(t, gl, baseDir))

			assert.Equal(t, tt.wantVerdict, res.Verdict, "got %+v", res)
		})
	}
}

func TestCorruptStoredSummaryBlocks(t *testing.T) {
	t.Parallel()
	gl := newFakeGitLab(t, respond(http.StatusBadGateway, gatewayError), respond(http.StatusBadGateway, gatewayError))
	baseDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Dir(summaryPath(baseDir)), 0o700))
	require.NoError(t, os.WriteFile(summaryPath(baseDir), []byte(`{"host":`), 0o600))

	assert.True(t, check(t, newRunChecker(t, gl, baseDir)).Blocked())
}

// glab takes the expiry only from expires_at, so a fetched summary without a
// future one, or without a fallback, never applies.
func TestFetchedSummaryThatCannotApplyBlocks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		enablement string
	}{
		{"expires_at in the past", enablementJSON("warn", expiresIn(-time.Hour))},
		{"no expires_at", `{"enabled":true,"unreachable_fallback":"warn"}`},
		{"no fallback fields", `{"enabled":true}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gl := newFakeGitLab(t, respond(http.StatusOK, tt.enablement), respond(http.StatusBadGateway, gatewayError))

			assert.True(t, check(t, newRunChecker(t, gl, t.TempDir())).Blocked())
		})
	}
}

func TestStoredSummaryIsIgnoredWhenGitLabAnswersTheEnablementCall(t *testing.T) {
	t.Parallel()
	gl := newFakeGitLab(t, respond(http.StatusNotFound, `{"message":"404 Not Found"}`), respond(http.StatusBadGateway, gatewayError))
	baseDir := t.TempDir()
	writeStoredSummary(t, hostOf(gl), baseDir, "group/proj", expiresIn(time.Hour))

	assert.True(t, check(t, newRunChecker(t, gl, baseDir)).Blocked())
}

func TestFetchedSummaryIsStoredForALaterRun(t *testing.T) {
	t.Parallel()
	var down atomic.Bool
	gl := newFakeGitLab(t,
		func(w http.ResponseWriter, r *http.Request) {
			if down.Load() {
				respond(http.StatusBadGateway, gatewayError)(w, r)
				return
			}
			respond(http.StatusOK, enablementJSON("warn", expiresIn(time.Hour)))(w, r)
		},
		respond(http.StatusBadGateway, gatewayError))
	baseDir := repoDir(t)
	newRunChecker(t, gl, baseDir)

	down.Store(true)

	assert.True(t, check(t, newRunChecker(t, gl, baseDir)).Warned())
}

func TestFallbackStopsApplyingOnceItExpires(t *testing.T) {
	t.Parallel()
	expiresAt := time.Now().Add(500 * time.Millisecond)
	gl := newFakeGitLab(t,
		respond(http.StatusOK, enablementJSON("warn", expiresAt.Format(time.RFC3339Nano))),
		respond(http.StatusBadGateway, gatewayError))
	c := newRunChecker(t, gl, t.TempDir())
	require.True(t, check(t, c).Warned())

	time.Sleep(time.Until(expiresAt) + 50*time.Millisecond)

	assert.True(t, check(t, c).Blocked())
}

func TestRequestAfterFallbackStillAsksGitLab(t *testing.T) {
	t.Parallel()
	var evaluateCalls atomic.Int64
	gl := newFakeGitLab(t, respond(http.StatusOK, enablementJSON("warn", expiresIn(time.Hour))), func(w http.ResponseWriter, r *http.Request) {
		if evaluateCalls.Add(1) == 1 {
			respond(http.StatusBadGateway, gatewayError)(w, r)
			return
		}
		respond(http.StatusOK, `{"outcome":"blocked","reason":"malware"}`)(w, r)
	})
	c := newRunChecker(t, gl, t.TempDir())
	require.True(t, check(t, c).Warned())

	assert.Equal(t, Result{Verdict: verdict.Blocked, Reason: "malware"}, check(t, c))
}

// Not parallel: git.InitGitRepo changes the working directory.
func TestStoredSummaryTrackedByGitIsIgnored(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	const untrustedGit = "because Git couldn't confirm it is untracked"
	tests := []struct {
		name        string
		enablement  http.HandlerFunc
		git         string          // tracked, untracked, missing or outside a repository
		wantVerdict verdict.Verdict // Blocked means no summary
		wantWarning string
		wantStored  verdict.Verdict
	}{
		{"tracked copy is ignored", respond(http.StatusBadGateway, gatewayError), "tracked", verdict.Blocked, "because Git tracks it", verdict.Warning},
		{"untracked copy in a repository applies", respond(http.StatusBadGateway, gatewayError), "untracked", verdict.Warning, "", verdict.Warning},
		{"fetched summary replaces a tracked copy", respond(http.StatusOK, enablementJSON("allow", expiresIn(time.Hour))), "tracked", verdict.Allowed, "", verdict.Allowed},
		{"copy is ignored without git", respond(http.StatusBadGateway, gatewayError), "missing", verdict.Blocked, untrustedGit, verdict.Warning},
		{"copy outside a repository is ignored", respond(http.StatusBadGateway, gatewayError), "outside a repository", verdict.Blocked, untrustedGit, verdict.Warning},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseDir := git.InitGitRepo(t)
			if tt.git == "outside a repository" {
				baseDir = t.TempDir()
			}
			gl := newFakeGitLab(t, tt.enablement, respond(http.StatusBadGateway, gatewayError))
			writeStoredSummary(t, hostOf(gl), baseDir, "group/proj", expiresIn(time.Hour))
			switch tt.git {
			case "tracked":
				_, err := run.PrepareCmd(git.GitCommand("add", summaryPath(baseDir))).Output()
				require.NoError(t, err)
			case "missing":
				t.Setenv("PATH", t.TempDir())
			}
			var logged strings.Builder
			log := func(format string, a ...any) { fmt.Fprintf(&logged, format, a...) }

			s := LoadSummary(t.Context(), newNoRetryChecker(t, gl), "group/proj", baseDir, log)

			if tt.wantVerdict == verdict.Blocked {
				assert.Nil(t, s)
			} else {
				require.NotNil(t, s)
				assert.Equal(t, tt.wantVerdict, s.verdict())
			}
			if tt.wantWarning == "" {
				assert.Empty(t, logged.String())
			} else {
				assert.Contains(t, logged.String(), tt.wantWarning)
			}
			stored, err := readSummary(summaryPath(baseDir))
			require.NoError(t, err)
			assert.Equal(t, tt.wantStored, stored.verdict(), "stored %+v", stored)
		})
	}
}

// The client's default retries stay on here, as they are for a real run.
func TestStoredSummaryAppliesAfterEnablementRetriesGiveUp(t *testing.T) {
	t.Parallel()
	gl := newFakeGitLab(t, respond(http.StatusBadGateway, gatewayError), respond(http.StatusBadGateway, gatewayError))
	baseDir := repoDir(t)
	writeStoredSummary(t, hostOf(gl), baseDir, "group/proj", expiresIn(time.Hour))
	client, err := gitlab.NewClient("token", gitlab.WithBaseURL(gl.URL+"/api/v4"))
	require.NoError(t, err)

	s := LoadSummary(t.Context(), newRESTChecker(client), "group/proj", baseDir, nil)

	require.NotNil(t, s)
	assert.Equal(t, verdict.Warning, s.verdict())
}
