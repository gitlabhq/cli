//go:build !integration

package proxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
)

// mirrorHost is the host used to simulate a mirror or proxy repository in
// these tests, via mvn's host parameter (defined in mavenmatch_test.go).
const mirrorHost = "https://mirror.example.com"

// denyChecker blocks exactly one coordinate name and allows everything else,
// standing in for a policy rule written against the real package.
type denyChecker struct {
	name string
	mu   sync.Mutex
	seen []string
	ops  []policy.Operation
}

func (d *denyChecker) Check(_ context.Context, r policy.Request) (policy.Result, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seen = append(d.seen, r.Coordinate.Name)
	d.ops = append(d.ops, r.Operation)
	if r.Coordinate.Name == d.name {
		return policy.Result{Verdict: verdict.Blocked, Reason: "denied by test policy"}, nil
	}
	return policy.Result{}, nil
}

// verdictChecker returns a canned verdict per coordinate name.
type verdictChecker map[string]policy.Result

func (v verdictChecker) Check(_ context.Context, r policy.Request) (policy.Result, error) {
	return v[r.Coordinate.Name], nil
}

// errChecker fails the check for one coordinate and allows the rest, standing
// in for a transient API failure part-way through the candidates.
type errChecker struct{ on string }

func (e errChecker) Check(_ context.Context, r policy.Request) (policy.Result, error) {
	if r.Coordinate.Name == e.on {
		return policy.Result{}, errors.New("boom")
	}
	return policy.Result{}, nil
}

// deadlineRecorder allows everything and records the deadline each check ran
// under, so a test can tell one shared budget from one budget per candidate.
type deadlineRecorder struct {
	mu        sync.Mutex
	deadlines []time.Time
}

func (d *deadlineRecorder) Check(ctx context.Context, _ policy.Request) (policy.Result, error) {
	deadline, _ := ctx.Deadline()
	d.mu.Lock()
	defer d.mu.Unlock()
	d.deadlines = append(d.deadlines, deadline)
	return policy.Result{}, nil
}

func candidateNames(m Match) []string {
	names := []string{m.Coordinate.Name}
	for _, c := range m.Alternates {
		names = append(names, c.Name)
	}
	return names
}

func TestMavenMirrorCandidates(t *testing.T) {
	t.Parallel()

	t.Run("when the path has no mirror prefix", func(t *testing.T) {
		t.Parallel()
		m := MavenMatcher{}.Match(mvn(http.MethodGet, "/maven2/org/slf4j/slf4j-api/2.0.9/slf4j-api-2.0.9.jar"))

		require.True(t, m.Matched)
		assert.Equal(t, "org.slf4j:slf4j-api", m.Coordinate.Name,
			"the unprefixed parse must stay the primary coordinate")
		assert.Equal(t, "2.0.9", m.Coordinate.Version)
	})

	t.Run("when a mirror adds path segments", func(t *testing.T) {
		t.Parallel()
		m := MavenMatcher{}.Match(mvn(http.MethodGet, "/repository/maven-central/org/slf4j/slf4j-api/2.0.9/slf4j-api-2.0.9.jar", mirrorHost))

		require.True(t, m.Matched)
		assert.Equal(t, "repository.maven-central.org.slf4j:slf4j-api", m.Coordinate.Name)
		assert.Equal(t, []policy.Coordinate{
			{Ecosystem: "maven", Name: "slf4j:slf4j-api", Version: "2.0.9"},
			{Ecosystem: "maven", Name: "org.slf4j:slf4j-api", Version: "2.0.9"},
			{Ecosystem: "maven", Name: "maven-central.org.slf4j:slf4j-api", Version: "2.0.9"},
		}, m.Alternates, "the real coordinate must be among the candidates whatever the mirror prefixes it with")
	})

	t.Run("when the mirror is the GitLab virtual registry", func(t *testing.T) {
		t.Parallel()
		m := MavenMatcher{}.Match(mvn(http.MethodGet, "/api/v4/virtual_registries/packages/maven/7/org/slf4j/slf4j-api/2.0.9/slf4j-api-2.0.9.jar", mirrorHost))

		require.True(t, m.Matched)
		assert.Contains(t, candidateNames(m), "org.slf4j:slf4j-api")
	})

	t.Run("when the group has more segments than the cap", func(t *testing.T) {
		t.Parallel()
		// Uncapped, 25 segments give 24 readings; the cap keeps the ten shortest
		// and the ten that drop fewest leading segments.
		m := MavenMatcher{}.Match(mvn(http.MethodGet, "/a/b/c/d/e/f/g/h/i/j/k/l/m/n/o/p/q/r/s/t/u/v/w/x/y/pkg/1.0/pkg-1.0.jar", mirrorHost))

		require.True(t, m.Matched)
		assert.Len(t, m.Alternates, 2*mavenMaxGroupDepth,
			"a pathologically deep group must not generate unbounded policy calls")
		assert.Equal(t, "y:pkg", m.Alternates[0].Name,
			"the shortest reading -- the one a real rule is most likely written against -- must survive the cap")
		assert.Contains(t, candidateNames(m), "b.c.d.e.f.g.h.i.j.k.l.m.n.o.p.q.r.s.t.u.v.w.x.y:pkg",
			"dropping one leading segment must survive the cap however long the reading is")
	})

	t.Run("when a long group sits behind a mirror prefix", func(t *testing.T) {
		t.Parallel()
		// The publisher picks the groupId's depth, so an 11-segment group must
		// not slip past a rule written for it.
		m := MavenMatcher{}.Match(mvn(http.MethodGet, "/repository/maven-central/io/github/evil/a/b/c/d/e/f/g/h/payload/1.0/payload-1.0.jar", mirrorHost))

		require.True(t, m.Matched)
		assert.Contains(t, candidateNames(m), "io.github.evil.a.b.c.d.e.f.g.h:payload")
	})

	t.Run("when the path is the exact Central layout", func(t *testing.T) {
		t.Parallel()
		// Under a leading maven2/ the coordinate is exact, and dropping segments
		// would only collide with relocated groups like dom4j:dom4j.
		m := MavenMatcher{}.Match(mvn(http.MethodGet, "/maven2/org/dom4j/dom4j/2.1.4/dom4j-2.1.4.jar"))

		require.True(t, m.Matched)
		assert.Equal(t, "org.dom4j:dom4j", m.Coordinate.Name)
		assert.Empty(t, m.Alternates)
	})

	t.Run("when the path has a doubled slash", func(t *testing.T) {
		t.Parallel()
		m := MavenMatcher{}.Match(mvn(http.MethodGet, "/repository//org/slf4j/slf4j-api/2.0.9/slf4j-api-2.0.9.jar", mirrorHost))

		require.True(t, m.Matched)
		assert.Equal(t, []string{
			"repository..org.slf4j:slf4j-api",
			"slf4j:slf4j-api",
			"org.slf4j:slf4j-api",
		}, candidateNames(m), "readings with an empty group segment are not coordinates and cost a wasted call")
	})

	t.Run("the candidate list for a mirrored path", func(t *testing.T) {
		t.Parallel()
		m := MavenMatcher{}.Match(mvn(http.MethodGet, "/repository/central/org/apache/maven/plugins/maven-compiler-plugin/3.1/maven-compiler-plugin-3.1.jar", mirrorHost))

		require.True(t, m.Matched)
		// Pinned so the cost of the fan-out cannot change without showing up
		// in a diff: one policy call per entry when the package is allowed.
		assert.Equal(t, []string{
			"repository.central.org.apache.maven.plugins:maven-compiler-plugin",
			"plugins:maven-compiler-plugin",
			"maven.plugins:maven-compiler-plugin",
			"apache.maven.plugins:maven-compiler-plugin",
			"org.apache.maven.plugins:maven-compiler-plugin",
			"central.org.apache.maven.plugins:maven-compiler-plugin",
		}, candidateNames(m))
	})

	t.Run("when the mirror is a deep GitLab virtual registry layout, for a plugin artifact", func(t *testing.T) {
		t.Parallel()
		// A plugin resolved via <pluginRepositories> must be covered the
		// same as a dependency via <repositories>: the matcher can't tell
		// which section caused the request.
		m := MavenMatcher{}.Match(mvn(http.MethodGet, "/api/v4/virtual_registries/packages/maven/42/org/apache/maven/plugins/maven-compiler-plugin/3.1/maven-compiler-plugin-3.1.jar", mirrorHost))

		require.True(t, m.Matched)
		assert.Contains(t, candidateNames(m), "org.apache.maven.plugins:maven-compiler-plugin")
	})

	t.Run("when the mirror is a shallow corporate Nexus-style layout", func(t *testing.T) {
		t.Parallel()
		// A 2-segment mirror path, the shape a Nexus proxy typically uses.
		m := MavenMatcher{}.Match(mvn(http.MethodGet, "/repository/public/org/slf4j/slf4j-api/2.0.9/slf4j-api-2.0.9.jar", mirrorHost))

		require.True(t, m.Matched)
		assert.Contains(t, candidateNames(m), "org.slf4j:slf4j-api",
			"must cover a shallow corporate mirror path, not only the 'maven-central' name used in other fixtures")
	})
}

func TestMavenMirrorPolicyMatching(t *testing.T) {
	t.Parallel()

	t.Run("when a rule names the real package behind a mirror", func(t *testing.T) {
		t.Parallel()
		checker := &denyChecker{name: "org.slf4j:slf4j-api"}
		p, err := New(MavenMatcher{}, checker, "group/proj")
		require.NoError(t, err)

		m := MavenMatcher{}.Match(mvn(http.MethodGet, "/repository/maven-central/org/slf4j/slf4j-api/2.0.9/slf4j-api-2.0.9.jar", mirrorHost))
		res, coord := p.checkPolicy(t.Context(), m)

		assert.True(t, res.Blocked(), "a mirror prefix must not hide a package from its rule")
		assert.Equal(t, "org.slf4j:slf4j-api", coord.Name,
			"the blocked entry must name the real package, not the prefixed form")
	})

	t.Run("when no rule matches any candidate", func(t *testing.T) {
		t.Parallel()
		checker := &denyChecker{name: "com.example:nothing"}
		p, err := New(MavenMatcher{}, checker, "group/proj")
		require.NoError(t, err)

		m := MavenMatcher{}.Match(mvn(http.MethodGet, "/repository/maven-central/org/slf4j/slf4j-api/2.0.9/slf4j-api-2.0.9.jar", mirrorHost))
		res, _ := p.checkPolicy(t.Context(), m)

		assert.False(t, res.Blocked())
	})

	t.Run("when an earlier candidate warns and a later one is blocked", func(t *testing.T) {
		t.Parallel()
		checker := verdictChecker{
			"repository.maven-central.org.slf4j:slf4j-api": policy.Result{Verdict: verdict.Warning, Reason: "warned"},
			"org.slf4j:slf4j-api":                          policy.Result{Verdict: verdict.Blocked, Reason: "denied"},
		}
		p, err := New(MavenMatcher{}, checker, "group/proj")
		require.NoError(t, err)

		m := MavenMatcher{}.Match(mvn(http.MethodGet, "/repository/maven-central/org/slf4j/slf4j-api/2.0.9/slf4j-api-2.0.9.jar", mirrorHost))
		res, coord := p.checkPolicy(t.Context(), m)

		assert.True(t, res.Blocked(), "a warn on one reading must not mask a block on another")
		assert.Equal(t, "org.slf4j:slf4j-api", coord.Name)
	})

	t.Run("when a candidate check errors", func(t *testing.T) {
		t.Parallel()
		checker := errChecker{on: "org.slf4j:slf4j-api"}
		p, err := New(MavenMatcher{}, checker, "group/proj")
		require.NoError(t, err)

		m := MavenMatcher{}.Match(mvn(http.MethodGet, "/repository/maven-central/org/slf4j/slf4j-api/2.0.9/slf4j-api-2.0.9.jar", mirrorHost))
		res, coord := p.checkPolicy(t.Context(), m)

		assert.True(t, res.Blocked(), "an undecidable candidate must fail closed")
		assert.Equal(t, m.Coordinate.Name, coord.Name,
			"the report must name the request, not the invented candidate that errored")
	})

	t.Run("when there is no mirror prefix", func(t *testing.T) {
		t.Parallel()
		checker := &denyChecker{name: "org.slf4j:slf4j-api"}
		p, err := New(MavenMatcher{}, checker, "group/proj")
		require.NoError(t, err)

		m := MavenMatcher{}.Match(mvn(http.MethodGet, "/maven2/org/slf4j/slf4j-api/2.0.9/slf4j-api-2.0.9.jar"))
		res, _ := p.checkPolicy(t.Context(), m)

		require.True(t, res.Blocked())
		assert.Equal(t, []string{"org.slf4j:slf4j-api"}, checker.seen,
			"a blocked primary coordinate decides immediately, costing one call")
	})

	t.Run("when an unmirrored package is allowed", func(t *testing.T) {
		t.Parallel()
		checker := &denyChecker{name: "com.example:nothing"}
		p, err := New(MavenMatcher{}, checker, "group/proj")
		require.NoError(t, err)

		m := MavenMatcher{}.Match(mvn(http.MethodGet, "/maven2/org/apache/maven/plugins/maven-compiler-plugin/3.1/maven-compiler-plugin-3.1.jar"))
		res, _ := p.checkPolicy(t.Context(), m)

		assert.False(t, res.Blocked())
		// The exact Central layout needs no candidates, so the common case
		// costs one call, as it did before enumeration existed.
		assert.Equal(t, []string{"org.apache.maven.plugins:maven-compiler-plugin"}, checker.seen)
	})

	t.Run("when a rule names a relocated single-segment group on Central", func(t *testing.T) {
		t.Parallel()
		// dom4j:dom4j was relocated to org.dom4j:dom4j; a rule on the old name
		// must not reach the new one from Central, which main allows.
		checker := &denyChecker{name: "dom4j:dom4j"}
		p, err := New(MavenMatcher{}, checker, "group/proj")
		require.NoError(t, err)

		m := MavenMatcher{}.Match(mvn(http.MethodGet, "/maven2/org/dom4j/dom4j/2.1.4/dom4j-2.1.4.jar"))
		res, _ := p.checkPolicy(t.Context(), m)

		assert.False(t, res.Blocked())
		assert.Equal(t, []string{"org.dom4j:dom4j"}, checker.seen)
	})

	t.Run("when a check fails behind the caching checker", func(t *testing.T) {
		t.Parallel()
		// Production's CachingChecker turns an error into a block, which must
		// still name the request rather than the invented candidate.
		checker := policy.NewCachingChecker(errChecker{on: "slf4j:slf4j-api"}, nil)
		p, err := New(MavenMatcher{}, checker, "group/proj")
		require.NoError(t, err)

		m := MavenMatcher{}.Match(mvn(http.MethodGet, "/repository/maven-central/org/slf4j/slf4j-api/2.0.9/slf4j-api-2.0.9.jar", mirrorHost))
		res, coord := p.checkPolicy(t.Context(), m)

		assert.True(t, res.Blocked(), "an undecidable candidate must fail closed")
		assert.Equal(t, m.Coordinate.Name, coord.Name)
	})

	t.Run("when only alternates warn", func(t *testing.T) {
		t.Parallel()
		checker := verdictChecker{
			"slf4j:slf4j-api":     {Verdict: verdict.Warning, Reason: "first"},
			"org.slf4j:slf4j-api": {Verdict: verdict.Warning, Reason: "second"},
		}
		p, err := New(MavenMatcher{}, checker, "group/proj")
		require.NoError(t, err)

		m := MavenMatcher{}.Match(mvn(http.MethodGet, "/repository/maven-central/org/slf4j/slf4j-api/2.0.9/slf4j-api-2.0.9.jar", mirrorHost))
		res, coord := p.checkPolicy(t.Context(), m)

		assert.Equal(t, verdict.Warning, res.Verdict)
		assert.Equal(t, "slf4j:slf4j-api", coord.Name, "the first warning in candidate order decides")
		assert.Equal(t, "first", res.Reason)
	})

	t.Run("when candidates share one deadline", func(t *testing.T) {
		t.Parallel()
		// One policy.CheckTimeout covers the whole candidate list, so a slow
		// backend bounds a request's total wait rather than one per candidate.
		checker := &deadlineRecorder{}
		p, err := New(MavenMatcher{}, checker, "group/proj")
		require.NoError(t, err)

		m := MavenMatcher{}.Match(mvn(http.MethodGet, "/repository/maven-central/org/slf4j/slf4j-api/2.0.9/slf4j-api-2.0.9.jar", mirrorHost))
		_, _ = p.checkPolicy(t.Context(), m)

		require.Greater(t, len(checker.deadlines), 1)
		require.False(t, checker.deadlines[0].IsZero(), "the candidates must run under a deadline")
		assert.Equal(t, slices.Repeat([]time.Time{checker.deadlines[0]}, len(checker.deadlines)), checker.deadlines,
			"every candidate must share one deadline")
	})

	t.Run("when the request is an upload to a deploy repository under a path prefix", func(t *testing.T) {
		t.Parallel()
		// Maven applies mirrors only to resolution, so a deploy repository
		// whose URL has a prefix is what puts an upload on a prefixed path.
		checker := &denyChecker{name: "org.slf4j:slf4j-api"}
		p, err := New(MavenMatcher{}, checker, "group/proj")
		require.NoError(t, err)

		m := MavenMatcher{}.Match(mvn(http.MethodPut, "/repository/releases/org/slf4j/slf4j-api/2.0.9/slf4j-api-2.0.9.jar", mirrorHost))
		require.True(t, m.Matched)

		res, coord := p.checkPolicy(t.Context(), m)

		assert.True(t, res.Blocked(), "an upload under a prefix must be covered the same way as a download")
		assert.Equal(t, "org.slf4j:slf4j-api", coord.Name)
		require.NotEmpty(t, checker.ops)
		assert.Equal(t, slices.Repeat([]policy.Operation{policy.Upload}, len(checker.ops)), checker.ops,
			"every candidate must be checked as an upload")
	})
}

// TestMavenMirrorThroughTunnel drives a request through the proxy, since only
// that path records the verdict entry naming the real package.
func TestMavenMirrorThroughTunnel(t *testing.T) {
	t.Parallel()
	const path = "/repository/maven-central/org/slf4j/slf4j-api/2.0.9/slf4j-api-2.0.9.jar"

	get := func(t *testing.T, checker policy.Checker) (int, *Proxy) {
		t.Helper()
		upstream := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		t.Cleanup(upstream.Close)
		p := startProxyWithMatcher(t, upstream, MavenMatcher{}, checker)
		resp, err := newClient(t, p).Get(upstream.URL + path)
		require.NoError(t, err)
		defer resp.Body.Close()
		return resp.StatusCode, p
	}

	t.Run("when a rule names the real package", func(t *testing.T) {
		t.Parallel()
		status, p := get(t, policy.NewCachingChecker(&denyChecker{name: "org.slf4j:slf4j-api"}, nil))

		assert.Equal(t, http.StatusForbidden, status)
		verdicts := p.Verdicts()
		require.Len(t, verdicts, 1)
		assert.Equal(t, "org.slf4j:slf4j-api", verdicts[0].Package)
	})

	t.Run("when a warn rule names the real package", func(t *testing.T) {
		t.Parallel()
		status, p := get(t, policy.NewCachingChecker(verdictChecker{"org.slf4j:slf4j-api": {Verdict: verdict.Warning, Reason: "warned"}}, nil))

		assert.Equal(t, http.StatusOK, status)
		verdicts := p.Verdicts()
		require.Len(t, verdicts, 1)
		assert.Equal(t, verdict.Warning, verdicts[0].Verdict)
		assert.Equal(t, "org.slf4j:slf4j-api", verdicts[0].Package)
	})

	t.Run("when a check fails behind the caching checker", func(t *testing.T) {
		t.Parallel()
		status, p := get(t, policy.NewCachingChecker(errChecker{on: "slf4j:slf4j-api"}, nil))

		assert.Equal(t, http.StatusForbidden, status)
		verdicts := p.Verdicts()
		require.Len(t, verdicts, 1)
		assert.Equal(t, "repository.maven-central.org.slf4j:slf4j-api", verdicts[0].Package,
			"a failed check is reported under the request's own coordinate, not an invented candidate")
	})
}
