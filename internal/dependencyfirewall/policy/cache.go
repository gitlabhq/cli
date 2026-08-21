package policy

import (
	"context"
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
		inner: inner,
		log:   log,
		cache: map[Request]*entry{},
	}
}

// Check returns the policy result for r, memoized per request for the lifetime
// of the checker. The error return is always nil: an inner failure is mapped
// to a fail-closed Blocked result rather than surfaced, so callers get a
// verdict on every path. It is part of the signature only to satisfy Checker.
func (c *CachingChecker) Check(ctx context.Context, r Request) (Result, error) {
	c.mu.Lock()
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
		if err != nil {
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
			return
		}
		e.res = res
	})

	return e.res, nil
}
