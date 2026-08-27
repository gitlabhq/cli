package policy

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
)

// Logf logs a diagnostic line. It matches iostreams.IOStreams.LogErrorf so
// callers can pass io.LogErrorf directly; nil is allowed (no logging).
type Logf func(format string, a ...any)

// CachingChecker wraps a Checker with per-request memoization and a
// fail-closed error policy. It is the only place caching and fail-mode live,
// so both Checker implementations stay simple. Safe for concurrent use: the
// proxy calls Check from multiple tunnel goroutines.
//
// Concurrent calls for the same request are coalesced: the first caller runs
// the inner check while the rest block on the entry's sync.Once, so a popular
// transitive dependency triggers a single inner check at cold start rather
// than one per tunnel goroutine.
//
// Lifetime invariant: the cache grows unbounded and is never evicted. It is
// sized for a single package-manager run (the proxy's lifetime). Callers must
// not share one CachingChecker across unrelated or long-lived workloads.
type CachingChecker struct {
	inner Checker
	log   Logf

	mu    sync.Mutex
	cache map[Request]*entry

	// notEvaluating latches the "firewall is not evaluating this project"
	// state per project. "Not evaluating" is a project-level condition (feature
	// flag off, not enforced, or the token cannot see the project), so once one
	// coordinate observes it, every later coordinate for the same project is
	// allowed without another round trip and without repeating the log line.
	// An npm install resolving hundreds of transitive dependencies then emits a
	// single diagnostic and issues a single evaluate call, instead of one per
	// coordinate.
	//
	// Trade-off: this latches an allow for the project for the rest of the run,
	// so a genuinely transient 404 (rolling deploy, brief gateway blip) that
	// happens to be the first response for a project fixes the fail-open
	// decision for the whole run. That is deliberate: a package-manager run is
	// short, "not evaluating" is overwhelmingly a stable configuration state,
	// and paying one evaluate call per coordinate to catch a rare transient
	// flip is not worth the request volume. The fail-closed branch keeps its
	// per-request retryability (it deletes the entry), so the two paths differ
	// on purpose.
	notEvaluating map[string]bool
}

// entry memoizes and coalesces a single request. once runs the inner check
// exactly once; res holds the decided result. Keying the cache on the
// comparable Request means there is one definition of request identity, so
// there is no way for two distinct requests to collide on a rendered string.
type entry struct {
	once sync.Once
	res  Result
}

// NewCachingChecker wraps inner. log may be nil.
func NewCachingChecker(inner Checker, log Logf) *CachingChecker {
	return &CachingChecker{
		inner:         inner,
		log:           log,
		cache:         map[Request]*entry{},
		notEvaluating: map[string]bool{},
	}
}

// Check returns the policy result for r, memoized per request for the lifetime
// of the checker. The error return is always nil: an inner failure is mapped
// to a fail-closed Blocked result rather than surfaced, so callers get a
// verdict on every path. It is part of the signature only to satisfy Checker.
func (c *CachingChecker) Check(ctx context.Context, r Request) (Result, error) {
	c.mu.Lock()
	// A project already observed to be not evaluating short-circuits to allow:
	// the state is project-level, so there is nothing to gain from another
	// evaluate call or another log line for a later coordinate.
	if c.notEvaluating[r.ProjectID] {
		c.mu.Unlock()
		return Result{}, nil
	}
	e, ok := c.cache[r]
	if !ok {
		e = &entry{}
		c.cache[r] = e
	}
	c.mu.Unlock()

	e.once.Do(func() {
		// The inner check is shared across every caller coalesced onto this
		// entry, so it must not inherit one caller's cancellation: WithoutCancel
		// keeps a single participant walking away from killing the shared check
		// for the rest. A hit past this point ignores ctx entirely, which is
		// intentional for a per-run cache.
		res, err := c.inner.Check(context.WithoutCancel(ctx), r)
		switch {
		case errors.Is(err, errFirewallNotEvaluating):
			// Fail open: the firewall is not evaluating this project (feature
			// flag off, not enforced, or the token cannot see the project), so
			// there is no verdict to apply and the package is allowed. Latch
			// the project so later coordinates short-circuit, and log only on
			// the first observation so an unexpected allow-all (a typo'd repo
			// or under-scoped token) is visible once rather than repeated per
			// coordinate or silent.
			c.mu.Lock()
			firstObservation := !c.notEvaluating[r.ProjectID]
			c.notEvaluating[r.ProjectID] = true
			c.mu.Unlock()
			if firstObservation && c.log != nil {
				c.log("dependency firewall not evaluating %s, allowing: %v\n", r.Key(), err)
			}
			e.res = Result{}
		case err != nil:
			// Fail closed: a policy decision we cannot obtain is treated as a
			// block. Drop the entry so the failure stays retryable within the
			// run instead of memoizing a transient error.
			if c.log != nil {
				c.log("warning: dependency firewall policy check failed for %s: %v\n", r.Key(), err)
			}
			c.mu.Lock()
			delete(c.cache, r)
			c.mu.Unlock()
			e.res = Result{
				Verdict: verdict.Blocked,
				Reason:  fmt.Sprintf("policy check failed: %v", err),
			}
		default:
			e.res = res
		}
	})

	return e.res, nil
}
