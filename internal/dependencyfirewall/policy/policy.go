// Package policy decides whether a package coordinate is allowed by the
// GitLab Dependency Firewall. It is the seam between the inspection proxy
// (which identifies a coordinate) and the verdict source. A fake,
// environment-driven Checker exists for testing; the REST-backed Checker
// that calls the real policy API lands in a follow-up. Callers wrap the
// chosen Checker in a CachingChecker.
package policy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
)

// Operation is the package-manager action that triggered a check.
type Operation int

const (
	// Download is an artifact fetch (tarball, wheel, jar, gem).
	Download Operation = iota
	// Upload is a publish/push of a package.
	Upload
)

// Coordinate is the exact package identity a check is performed against.
type Coordinate struct {
	Ecosystem string // "npm", "pypi", "maven", "gem"
	Name      string
	Version   string
}

// Key is the cache and dedupe key for a coordinate.
func (c Coordinate) Key() string {
	return fmt.Sprintf("%s:%s@%s", c.Ecosystem, c.Name, c.Version)
}

// Request is a single policy question.
type Request struct {
	Coordinate Coordinate
	ProjectID  string // project id or full-path slug, from git repo context
	Operation  Operation
}

// Key is the cache and dedupe key for a request. It includes the project and
// operation, not just the coordinate: the same package can get a different
// verdict per project (policies are project-scoped) and per operation
// (download vs upload), so keying on the coordinate alone would leak one
// project's or operation's verdict to another once the real checker lands.
func (r Request) Key() string {
	return fmt.Sprintf("%s|%s|%d", r.Coordinate.Key(), r.ProjectID, r.Operation)
}

// Result is the outcome of a policy check. A zero Verdict means allow.
type Result struct {
	Verdict verdict.Verdict // verdict.Blocked, verdict.Warning, or "" (allow)
	Reason  string          // human-readable; used in the 403 body and summary
}

// Blocked reports whether the result denies the coordinate.
func (r Result) Blocked() bool { return r.Verdict == verdict.Blocked }

// Checker answers policy questions.
type Checker interface {
	Check(ctx context.Context, req Request) (Result, error)
}

// fakeEnvPrefix is the environment-variable prefix that both selects and
// configures the fake checker.
const fakeEnvPrefix = "GLAB_DF_FAKE_"

// ErrNotImplemented is returned by the placeholder checker until the
// REST-backed checker is wired in a follow-up. It fails closed so a
// misconfigured build denies rather than silently allows.
var ErrNotImplemented = errors.New("policy: real checker not yet implemented")

// New returns the fake checker when any GLAB_DF_FAKE_* variable is set.
// Until the REST checker lands, the non-fake path returns a placeholder
// that fails closed. Callers wrap the result in a CachingChecker.
func New(client *gitlab.Client, projectID string) Checker {
	environ := os.Environ()
	if fakeConfigured(environ) {
		return newFakeChecker(environ)
	}
	return notImplementedChecker{}
}

// notImplementedChecker is the fail-closed placeholder for the not-yet-wired
// REST checker.
type notImplementedChecker struct{}

func (notImplementedChecker) Check(context.Context, Request) (Result, error) {
	return Result{}, ErrNotImplemented
}

func fakeConfigured(environ []string) bool {
	for _, e := range environ {
		if strings.HasPrefix(e, fakeEnvPrefix) {
			return true
		}
	}
	return false
}
