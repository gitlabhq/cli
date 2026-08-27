// Package policy decides whether a package coordinate is allowed by the
// GitLab Dependency Firewall. It is the seam between the inspection proxy
// (which identifies a coordinate) and the verdict source. Two Checker
// implementations exist: a fake, environment-driven one for testing and a
// REST-backed one that calls the real policy API. Callers wrap the chosen
// Checker in a CachingChecker.
package policy

import (
	"context"
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

// String returns a stable, human-readable name for the operation. It keeps
// log lines legible and independent of the numeric ordering of the constants.
func (o Operation) String() string {
	switch o {
	case Download:
		return "download"
	case Upload:
		return "upload"
	default:
		return fmt.Sprintf("operation(%d)", int(o))
	}
}

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

// Request is a single policy question. ProjectID selects the project whose
// firewall answers it; the REST checker evaluates against this field.
// Operation records the package-manager action for cache-key isolation and
// diagnostics; the evaluate API is scoped to project and coordinate only and
// does not accept an operation, so it is not transmitted.
type Request struct {
	Coordinate Coordinate
	ProjectID  string // project id or full-path slug, from git repo context
	Operation  Operation
}

// Key is the cache and dedupe key for a request. It includes the project and
// operation, not just the coordinate: the same package can get a different
// verdict per project (policies are project-scoped), and download and upload
// are tracked separately, so keying on the coordinate alone would let one
// project's or operation's cached verdict answer for another.
func (r Request) Key() string {
	return fmt.Sprintf("%s|%s|%s", r.Coordinate.Key(), r.ProjectID, r.Operation)
}

// Result is the outcome of a policy check. A zero Verdict (verdict.Allowed)
// means allow.
type Result struct {
	Verdict verdict.Verdict // verdict.Allowed, verdict.Blocked, or verdict.Warning
	Reason  string          // human-readable; used in the 403 body and summary
}

// Allowed reports whether the result permits the coordinate.
func (r Result) Allowed() bool { return r.Verdict == verdict.Allowed }

// Blocked reports whether the result denies the coordinate.
func (r Result) Blocked() bool { return r.Verdict == verdict.Blocked }

// Warned reports whether the result permits the coordinate but flags it.
func (r Result) Warned() bool { return r.Verdict == verdict.Warning }

// Checker answers policy questions.
type Checker interface {
	Check(ctx context.Context, req Request) (Result, error)
}

// fakeEnvPrefix is the environment-variable prefix that both selects and
// configures the fake checker.
const fakeEnvPrefix = "GLAB_DF_FAKE_"

// New returns the fake checker when any GLAB_DF_FAKE_* variable is set,
// otherwise the REST checker backed by client. The REST checker evaluates
// against each Request's ProjectID, so no project is bound here. Callers wrap
// the result in a CachingChecker.
func New(client *gitlab.Client) Checker {
	environ := os.Environ()
	if fakeConfigured(environ) {
		return newFakeChecker(environ)
	}
	return newRESTChecker(client)
}

func fakeConfigured(environ []string) bool {
	for _, e := range environ {
		if strings.HasPrefix(e, fakeEnvPrefix) {
			return true
		}
	}
	return false
}
