//go:build !integration

package policy

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
)

type countingChecker struct {
	calls  atomic.Int64
	result Result
	err    error

	// entered, when non-nil, receives once each time a call enters inner, and
	// gate, when non-nil, blocks that call until closed. Together they let a
	// test observe a call in flight and then release it without sleeping, so
	// the coalescing window is exercised deterministically.
	entered chan<- struct{}
	gate    <-chan struct{}
}

func (c *countingChecker) Check(context.Context, Request) (Result, error) {
	c.calls.Add(1)
	if c.entered != nil {
		c.entered <- struct{}{}
	}
	if c.gate != nil {
		<-c.gate
	}
	return c.result, c.err
}

func TestCacheMemoizesByRequestKey(t *testing.T) {
	t.Parallel()
	inner := &countingChecker{result: Result{Verdict: verdict.Warning, Reason: "abc"}}
	c := NewCachingChecker(inner, nil)
	r1, err := c.Check(t.Context(), req("npm", "x", "1.0.0"))
	require.NoError(t, err)
	r2, err := c.Check(t.Context(), req("npm", "x", "1.0.0"))
	require.NoError(t, err)
	assert.Equal(t, "abc", r1.Reason)
	assert.Equal(t, "abc", r2.Reason)
	assert.Equal(t, int64(1), inner.calls.Load(), "second call must hit cache")
}

func TestCacheMemoizesBlockedResult(t *testing.T) {
	t.Parallel()
	inner := &countingChecker{result: Result{Verdict: verdict.Blocked, Reason: "denied"}}
	c := NewCachingChecker(inner, nil)
	r1, err := c.Check(t.Context(), req("npm", "x", "1.0.0"))
	require.NoError(t, err)
	r2, err := c.Check(t.Context(), req("npm", "x", "1.0.0"))
	require.NoError(t, err)
	assert.Equal(t, verdict.Blocked, r1.Verdict)
	assert.Equal(t, verdict.Blocked, r2.Verdict)
	assert.Equal(t, "denied", r2.Reason)
	assert.Equal(t, int64(1), inner.calls.Load(), "a blocked result must round-trip through the cache")
}

func TestCacheDistinctRequestKeys(t *testing.T) {
	t.Parallel()
	inner := &countingChecker{result: Result{}}
	c := NewCachingChecker(inner, nil)
	_, err := c.Check(t.Context(), req("npm", "x", "1.0.0"))
	require.NoError(t, err)
	_, err = c.Check(t.Context(), req("npm", "x", "2.0.0"))
	require.NoError(t, err)
	assert.Equal(t, int64(2), inner.calls.Load())
}

func TestCacheDistinctByProjectID(t *testing.T) {
	t.Parallel()
	inner := &countingChecker{result: Result{}}
	c := NewCachingChecker(inner, nil)
	r := req("npm", "x", "1.0.0")
	rA := r
	rA.ProjectID = "group/project-a"
	rB := r
	rB.ProjectID = "group/project-b"
	_, err := c.Check(t.Context(), rA)
	require.NoError(t, err)
	_, err = c.Check(t.Context(), rB)
	require.NoError(t, err)
	assert.Equal(t, int64(2), inner.calls.Load(), "same coordinate in different projects must not share a cache entry")
}

func TestCacheDistinctByOperation(t *testing.T) {
	t.Parallel()
	inner := &countingChecker{result: Result{}}
	c := NewCachingChecker(inner, nil)
	r := req("npm", "x", "1.0.0")
	rDownload := r
	rDownload.Operation = Download
	rUpload := r
	rUpload.Operation = Upload
	_, err := c.Check(t.Context(), rDownload)
	require.NoError(t, err)
	_, err = c.Check(t.Context(), rUpload)
	require.NoError(t, err)
	assert.Equal(t, int64(2), inner.calls.Load(), "same coordinate under different operations must not share a cache entry")
}

func TestCacheFailsClosedOnError(t *testing.T) {
	t.Parallel()
	inner := &countingChecker{err: errors.New("boom")}
	c := NewCachingChecker(inner, nil)
	r, err := c.Check(t.Context(), req("npm", "x", "1.0.0"))
	require.NoError(t, err, "fail-closed returns a blocked Result, not an error")
	assert.Equal(t, verdict.Blocked, r.Verdict)
	assert.Contains(t, r.Reason, "policy check failed")
}

func TestCacheDoesNotCacheErrors(t *testing.T) {
	t.Parallel()
	inner := &countingChecker{err: errors.New("boom")}
	c := NewCachingChecker(inner, nil)
	_, err := c.Check(t.Context(), req("npm", "x", "1.0.0"))
	require.NoError(t, err)
	_, err = c.Check(t.Context(), req("npm", "x", "1.0.0"))
	require.NoError(t, err)
	assert.Equal(t, int64(2), inner.calls.Load(), "errored results must not be cached")
}

func TestCacheLogsOnError(t *testing.T) {
	t.Parallel()
	inner := &countingChecker{err: errors.New("boom")}
	var gotFormat string
	var gotArgs []any
	log := func(format string, a ...any) {
		gotFormat = format
		gotArgs = a
	}
	c := NewCachingChecker(inner, log)
	_, err := c.Check(t.Context(), req("npm", "x", "1.0.0"))
	require.NoError(t, err)
	require.NotEmpty(t, gotFormat, "log callback must be invoked on a failed check")
	// The key and the underlying error are passed to the callback; the raw
	// error is not surfaced verbatim in the caller-facing Result.
	require.Len(t, gotArgs, 2)
	assert.Contains(t, gotArgs[0], "npm")
	assert.Contains(t, gotArgs[0], "x")
}

func TestCacheFailsOpenWhenFirewallNotEvaluating(t *testing.T) {
	t.Parallel()
	inner := &countingChecker{err: errFirewallNotEvaluating}
	c := NewCachingChecker(inner, nil)
	r, err := c.Check(t.Context(), req("npm", "x", "1.0.0"))
	require.NoError(t, err)
	assert.Equal(t, Result{}, r, "not-evaluating maps to allow, not block")
}

func TestCacheLogsWhenFirewallNotEvaluating(t *testing.T) {
	t.Parallel()
	inner := &countingChecker{err: errFirewallNotEvaluating}
	var gotFormat string
	log := func(format string, a ...any) { gotFormat = format }
	c := NewCachingChecker(inner, log)
	_, err := c.Check(t.Context(), req("npm", "x", "1.0.0"))
	require.NoError(t, err)
	assert.Contains(t, gotFormat, "not evaluating", "an unexpected allow-all must be visible in the log")
}

func TestCacheLatchesNotEvaluatingPerProject(t *testing.T) {
	t.Parallel()
	// Once a project is observed not evaluating, later coordinates for that
	// same project short-circuit to allow without another inner call.
	inner := &countingChecker{err: errFirewallNotEvaluating}
	c := NewCachingChecker(inner, nil)

	base := req("npm", "a", "1.0.0")
	base.ProjectID = "group/project"

	first := base
	r1, err := c.Check(t.Context(), first)
	require.NoError(t, err)
	assert.Equal(t, Result{}, r1)

	second := base
	second.Coordinate = Coordinate{Ecosystem: "npm", Name: "b", Version: "2.0.0"}
	r2, err := c.Check(t.Context(), second)
	require.NoError(t, err)
	assert.Equal(t, Result{}, r2)

	assert.Equal(t, int64(1), inner.calls.Load(),
		"a not-evaluating project must be evaluated once, then latched for later coordinates")
}

func TestCacheLatchLogsOncePerProject(t *testing.T) {
	t.Parallel()
	// The not-evaluating diagnostic is emitted once per project, not once per
	// coordinate, so a large install does not flood stderr.
	inner := &countingChecker{err: errFirewallNotEvaluating}
	var logCount int
	log := func(string, ...any) { logCount++ }
	c := NewCachingChecker(inner, log)

	base := req("npm", "a", "1.0.0")
	base.ProjectID = "group/project"

	for i, name := range []string{"a", "b", "c"} {
		r := base
		r.Coordinate = Coordinate{Ecosystem: "npm", Name: name, Version: "1.0.0"}
		res, err := c.Check(t.Context(), r)
		require.NoError(t, err, "coordinate %d", i)
		assert.Equal(t, Result{}, res)
	}

	assert.Equal(t, 1, logCount, "not-evaluating must be logged once per project, not per coordinate")
}

func TestCacheLatchIsPerProject(t *testing.T) {
	t.Parallel()
	// Latching one project must not short-circuit a different project: the
	// second project is still evaluated by inner.
	inner := &countingChecker{err: errFirewallNotEvaluating}
	c := NewCachingChecker(inner, nil)

	rA := req("npm", "x", "1.0.0")
	rA.ProjectID = "group/project-a"
	rB := req("npm", "x", "1.0.0")
	rB.ProjectID = "group/project-b"

	_, err := c.Check(t.Context(), rA)
	require.NoError(t, err)
	_, err = c.Check(t.Context(), rB)
	require.NoError(t, err)

	assert.Equal(t, int64(2), inner.calls.Load(),
		"a not-evaluating latch is per project, so a different project is still evaluated")
}

func TestCacheConcurrentSameKeySingleFetch(t *testing.T) {
	t.Parallel()
	// The winning caller enters inner and signals on entered; it blocks on
	// gate while the other callers pile up on the entry's Once. Receiving the
	// entry signal proves a call is in flight, so we can release it without a
	// sleep. Coalescing means exactly one call ever enters inner.
	entered := make(chan struct{})
	gate := make(chan struct{})
	inner := &countingChecker{
		result:  Result{Verdict: verdict.Blocked, Reason: "denied"},
		entered: entered,
		gate:    gate,
	}
	c := NewCachingChecker(inner, nil)
	r := req("npm", "left-pad", "1.3.0")

	const n = 50
	var wg sync.WaitGroup
	results := make([]Result, n)
	errs := make([]error, n)
	wg.Add(n)
	for i := range n {
		go func() {
			defer wg.Done()
			results[i], errs[i] = c.Check(t.Context(), r)
		}()
	}
	// Block until a call is provably in flight, then release it. Any caller
	// that had to run inner would deadlock here, since the test only ever
	// drains a single entry signal.
	<-entered
	close(gate)
	wg.Wait()

	for i := range n {
		require.NoError(t, errs[i])
		assert.Equal(t, verdict.Blocked, results[i].Verdict)
		assert.Equal(t, "denied", results[i].Reason)
	}
	// Coalescing collapses concurrent identical requests into one inner call.
	assert.Equal(t, int64(1), inner.calls.Load(), "concurrent identical requests must coalesce into a single inner check")
}
