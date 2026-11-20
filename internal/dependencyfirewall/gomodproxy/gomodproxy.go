package gomodproxy

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"net/url"
	"path"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
)

const (
	drainTimeout      = 5 * time.Second
	readHeaderTimeout = 30 * time.Second
)

// Server serves the Go module proxy protocol on loopback and gates module
// zip downloads against the Dependency Firewall.
//
// The backend's fetcher carries the user's version-control credentials, so
// only a client that knows the per-run path token and addresses the listener
// by its own address reaches it: that excludes other local processes and
// DNS-rebinding web pages.
type Server struct {
	checker   policy.Checker
	projectID string
	backend   http.Handler
	token     string

	ctx    context.Context
	cancel context.CancelFunc

	listener net.Listener
	addr     string
	server   *http.Server

	checked atomic.Int64

	mu       sync.Mutex
	verdicts []verdict.Entry
	seen     map[string]struct{}

	stopOnce sync.Once
}

func New(checker policy.Checker, projectID string, backend http.Handler) *Server {
	s := &Server{
		checker:   checker,
		projectID: projectID,
		backend:   backend,
		token:     rand.Text(),
		seen:      make(map[string]struct{}),
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	return s
}

// Start listens on an ephemeral loopback port and begins serving.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	s.listener = ln
	s.addr = ln.Addr().String()
	s.server = &http.Server{
		Handler:           http.HandlerFunc(s.handle),
		ReadHeaderTimeout: readHeaderTimeout,
		BaseContext:       func(net.Listener) context.Context { return s.ctx },
	}
	go func() {
		if err := s.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			dbg.Debugf("gomodproxy: stopped serving: %v\n", err)
		}
	}()
	return nil
}

// BaseURL is the value to hand the child as GOPROXY. It is empty before Start.
func (s *Server) BaseURL() string {
	if s.addr == "" {
		return ""
	}
	return "http://" + s.addr + "/" + s.token
}

// Stop cancels in-flight policy checks and fetches, waits up to drainTimeout
// for their handlers to return, then force-closes what is left. Waiting
// matters because the caller removes the backend's temp tree next. Stop is
// idempotent.
func (s *Server) Stop() {
	s.stopOnce.Do(func() {
		s.cancel()
		if s.server == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), drainTimeout)
		defer cancel()
		if err := s.server.Shutdown(ctx); err != nil {
			dbg.Debugf("gomodproxy: drain: %v\n", err)
			_ = s.server.Close()
		}
	})
}

// Verdicts returns a copy of the blocked and warned verdicts recorded so far.
func (s *Server) Verdicts() []verdict.Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.verdicts)
}

// CheckedZips reports how many module zips got a policy decision.
func (s *Server) CheckedZips() int {
	return int(s.checked.Load())
}

func (s *Server) record(e verdict.Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := e.Key()
	if _, ok := s.seen[key]; ok {
		return
	}
	s.seen[key] = struct{}{}
	s.verdicts = append(s.verdicts, e)
}

// handle refuses anything it cannot classify with a 404, which is safe only
// because GOPROXY names this server alone: there is no next source for go to
// fall through to.
func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	rest, ok := s.authorize(r)
	if !ok {
		http.NotFound(w, r)
		return
	}

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// go percent-encodes only the '!' of the module case encoding
	// (github.com/BurntSushi -> github.com/%21burnt%21sushi).
	reqPath := strings.ReplaceAll("/"+rest, "%21", "!")

	// The gate checks the escaped path while goproxy routes the decoded one,
	// and module.UnescapeVersion accepts a literal '%'. Any other escape would
	// let ".../@v/v0%2e9%2e1.zip" be checked as an unknown version and then
	// served as v0.9.1.
	decoded, err := url.PathUnescape(reqPath)
	if err != nil || decoded != reqPath {
		http.NotFound(w, r)
		return
	}

	if path.Clean(reqPath) != reqPath {
		http.NotFound(w, r)
		return
	}

	if isSumDB(reqPath) {
		s.delegate(w, r, reqPath)
		return
	}

	req, ok := parseRequest(reqPath)
	if !ok {
		http.NotFound(w, r)
		return
	}

	// Metadata passes ungated: go probes every path prefix of a module
	// ("/github.com/@v/v1.6.0.info"), and a 403 on one of those would break
	// resolution for every module under it.
	if req.kind != kindZip || isToolchain(req) {
		s.delegate(w, r, reqPath)
		return
	}

	res, decided := s.check(req) //nolint:contextcheck // a client disconnect must not cancel the check; see check
	if !decided {
		http.Error(w, "dependency firewall stopped", http.StatusServiceUnavailable)
		return
	}
	switch {
	case res.Blocked():
		s.record(verdict.Entry{
			Package:   req.module,
			Version:   req.version,
			Verdict:   verdict.Blocked,
			Reason:    res.Reason,
			Status:    http.StatusForbidden,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
		reason := res.Reason
		if res.Internal {
			reason = policy.InternalErrorPhrase(policy.Coordinate{Name: req.module, Version: req.version})
		}
		writeBlocked(w, req.module, req.version, reason)
		return
	case res.Warned():
		s.record(verdict.Entry{
			Package:   req.module,
			Version:   req.version,
			Verdict:   verdict.Warning,
			Reason:    res.Reason,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}
	s.delegate(w, r, reqPath)
}

func (s *Server) authorize(r *http.Request) (string, bool) {
	if r.Host != s.addr {
		return "", false
	}
	token, rest, ok := strings.Cut(strings.TrimPrefix(r.URL.EscapedPath(), "/"), "/")
	if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) != 1 {
		return "", false
	}
	return rest, true
}

// check fails closed on any error so a firewall outage cannot admit a
// package. It reports false when Stop ended the check: that is not a
// firewall decision, and recording it as Blocked would fail a later
// ci-summary for a module nobody blocked.
//
// The check runs under the server context rather than the request's, which
// net/http cancels as soon as the client disconnects. It runs in its own
// goroutine because policy.CachingChecker calls the API under
// context.WithoutCancel, so Stop could otherwise neither end it nor stop it
// recording a verdict after Stop returned.
func (s *Server) check(req request) (policy.Result, bool) {
	ctx, cancel := context.WithTimeout(s.ctx, policy.CheckTimeout)
	defer cancel()

	type outcome struct {
		res policy.Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		// This goroutine is outside net/http's recover, so a panicking checker
		// would end glab. Block instead; the value stays out of the reason.
		defer func() {
			if r := recover(); r != nil {
				dbg.Debugf("dependency firewall policy check panicked for %s@%s: %v\n%s", req.module, req.version, r, debug.Stack())
				done <- outcome{err: policy.ErrCheckPanicked}
			}
		}()
		res, err := s.checker.Check(ctx, policy.Request{
			Coordinate: policy.Coordinate{
				Ecosystem: "golang",
				Name:      req.module,
				Version:   req.version,
			},
			ProjectID: s.projectID,
			Operation: policy.Download,
		})
		done <- outcome{res, err}
	}()

	var out outcome
	select {
	case out = <-done:
	case <-s.ctx.Done():
		return policy.Result{}, false
	}
	if out.err != nil && s.ctx.Err() != nil {
		return policy.Result{}, false
	}
	s.checked.Add(1)
	if errors.Is(out.err, policy.ErrCheckPanicked) {
		return policy.InternalErrorResult(policy.Coordinate{Name: req.module, Version: req.version}), true
	}
	if out.err != nil {
		return policy.Result{
			Verdict: verdict.Blocked,
			Reason:  "policy check failed: " + out.err.Error(),
		}, true
	}
	return out.res, true
}

func (s *Server) delegate(w http.ResponseWriter, r *http.Request, reqPath string) {
	out := new(http.Request)
	*out = *r
	out.URL = new(url.URL)
	*out.URL = *r.URL
	out.URL.Path = reqPath
	out.URL.RawPath = ""
	out.RequestURI = ""
	s.backend.ServeHTTP(w, out)
}
