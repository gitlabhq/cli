//go:build !integration

package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
)

// log4jParentPOM is log4j 2's parent POM behind a mirror. One of its guessed
// readings is log4j:log4j, the unrelated Log4j 1 package.
const log4jParentPOM = "/repository/public/org/apache/logging/log4j/log4j/2.24.3/log4j-2.24.3.pom"

func knownMirrors(t *testing.T, externalOnly bool, raw ...string) *MavenMirrors {
	t.Helper()
	urls := make([]*url.URL, 0, len(raw))
	for _, r := range raw {
		u, err := url.Parse(r)
		require.NoError(t, err)
		urls = append(urls, u)
	}
	m := &MavenMirrors{}
	m.Set(urls, externalOnly)
	return m
}

func TestMavenMirrorKnownURLs(t *testing.T) {
	t.Parallel()
	nexus := knownMirrors(t, false, "https://nexus.example.com/repository/public/")

	t.Run("when a download is under a known mirror", func(t *testing.T) {
		t.Parallel()
		m := MavenMatcher{Mirrors: nexus}.Match(mvn(http.MethodGet, log4jParentPOM, "https://nexus.example.com"))

		require.True(t, m.Matched)
		assert.Equal(t, "org.apache.logging.log4j:log4j", m.Coordinate.Name)
		assert.Equal(t, "2.24.3", m.Coordinate.Version)
		assert.Empty(t, m.Alternates, "a known mirror leaves nothing to guess")
	})

	t.Run("when a rule names the log4j:log4j reading a guess would make", func(t *testing.T) {
		t.Parallel()
		m := MavenMatcher{Mirrors: nexus}.Match(mvn(http.MethodGet, log4jParentPOM, "https://nexus.example.com"))

		p, err := New(MavenMatcher{Mirrors: nexus}, &denyChecker{name: "log4j:log4j"}, "group/proj")
		require.NoError(t, err)
		res, _ := p.checkPolicy(t.Context(), m)
		assert.False(t, res.Blocked(), "log4j 2's parent POM must not be blocked as Log4j 1")

		p, err = New(MavenMatcher{Mirrors: nexus}, &denyChecker{name: "org.apache.logging.log4j:log4j"}, "group/proj")
		require.NoError(t, err)
		res, coord := p.checkPolicy(t.Context(), m)
		assert.True(t, res.Blocked())
		assert.Equal(t, "org.apache.logging.log4j:log4j", coord.Name)
	})

	t.Run("when two known mirrors nest on one host", func(t *testing.T) {
		t.Parallel()
		nested := knownMirrors(t, false, "https://nexus.example.com/repository/", "https://nexus.example.com/repository/central/")

		m := MavenMatcher{Mirrors: nested}.Match(mvn(http.MethodGet, "/repository/central/org/slf4j/slf4j-api/2.0.9/slf4j-api-2.0.9.jar", "https://nexus.example.com"))

		assert.Equal(t, "org.slf4j:slf4j-api", m.Coordinate.Name, "the longest mirror decides the primary reading")
		assert.Equal(t, []string{"central.org.slf4j:slf4j-api"}, candidateNames(m)[1:])
	})

	t.Run("when the path only shares a prefix with a known mirror", func(t *testing.T) {
		t.Parallel()
		m := MavenMatcher{Mirrors: nexus}.Match(mvn(http.MethodGet, "/repository/public-snapshots/org/slf4j/slf4j-api/2.0.9/slf4j-api-2.0.9.jar", "https://nexus.example.com"))

		assert.Equal(t, "repository.public-snapshots.org.slf4j:slf4j-api", m.Coordinate.Name, "a mirror path only matches on a segment boundary")
		assert.NotEmpty(t, m.Alternates)
	})

	t.Run("when the mirror URL has no trailing slash", func(t *testing.T) {
		t.Parallel()
		bare := knownMirrors(t, false, "https://nexus.example.com/repository/public")

		under := MavenMatcher{Mirrors: bare}.Match(mvn(http.MethodGet, log4jParentPOM, "https://nexus.example.com"))
		assert.Empty(t, under.Alternates)
		beside := MavenMatcher{Mirrors: bare}.Match(mvn(http.MethodGet, "/repository/public-snapshots/org/slf4j/slf4j-api/2.0.9/slf4j-api-2.0.9.jar", "https://nexus.example.com"))
		assert.NotEmpty(t, beside.Alternates, "a mirror path only matches on a segment boundary")
	})

	t.Run("when the request names the default port", func(t *testing.T) {
		t.Parallel()
		httpMirror := knownMirrors(t, false, "http://nexus.example.com/repository/public/")
		for _, tc := range []struct {
			mirrors *MavenMirrors
			host    string
		}{
			{nexus, "https://nexus.example.com:443"},
			{httpMirror, "http://nexus.example.com:80"},
			{httpMirror, "http://nexus.example.com"},
		} {
			m := MavenMatcher{Mirrors: tc.mirrors}.Match(mvn(http.MethodGet, log4jParentPOM, tc.host))
			assert.Equal(t, "org.apache.logging.log4j:log4j", m.Coordinate.Name, tc.host)
			assert.Empty(t, m.Alternates, tc.host)
		}
	})

	t.Run("when the request is to another host, port or scheme", func(t *testing.T) {
		t.Parallel()
		for _, host := range []string{"https://other.example.com", "https://nexus.example.com:8443", "http://nexus.example.com"} {
			m := MavenMatcher{Mirrors: nexus}.Match(mvn(http.MethodGet, log4jParentPOM, host))
			assert.NotEmpty(t, m.Alternates, "%s isn't a known mirror, so the prefix is guessed", host)
		}
	})

	t.Run("when the download under a known mirror isn't an artifact", func(t *testing.T) {
		t.Parallel()
		m := MavenMatcher{Mirrors: nexus}.Match(mvn(http.MethodGet, "/repository/public/org/slf4j/slf4j-api/maven-metadata.xml", "https://nexus.example.com"))

		assert.False(t, m.Matched)
	})

	t.Run("when the filename under a known mirror claims another version", func(t *testing.T) {
		t.Parallel()
		m := MavenMatcher{Mirrors: nexus}.Match(mvn(http.MethodGet, "/repository/public/org/slf4j/slf4j-api/1/slf4j-api-10.jar", "https://nexus.example.com"))

		assert.True(t, m.Matched)
		assert.False(t, m.Pass, "a coordinate the path can't be trusted for fails closed")
	})

	t.Run("when the request is an upload under a known mirror", func(t *testing.T) {
		t.Parallel()
		m := MavenMatcher{Mirrors: nexus}.Match(mvn(http.MethodPut, "/repository/public/org/slf4j/slf4j-api/2.0.9/slf4j-api-2.0.9.jar", "https://nexus.example.com"))

		assert.NotEmpty(t, m.Alternates, "Maven never deploys through a mirror, so an upload's prefix is guessed")
	})

	t.Run("when the path under a known mirror has an empty or dot segment", func(t *testing.T) {
		t.Parallel()
		for _, path := range []string{
			"/repository/public/org//slf4j/slf4j-api/2.0.9/slf4j-api-2.0.9.jar",
			"/repository/public/org/./slf4j/slf4j-api/2.0.9/slf4j-api-2.0.9.jar",
			"/repository/public/x/../org/slf4j/slf4j-api/2.0.9/slf4j-api-2.0.9.jar",
		} {
			m := MavenMatcher{Mirrors: nexus}.Match(mvn(http.MethodGet, path, "https://nexus.example.com"))
			assert.NotEmpty(t, m.Alternates, path)
		}
	})

	t.Run("when the catch-all mirror is external:*", func(t *testing.T) {
		t.Parallel()
		local := knownMirrors(t, true, "https://nexus.example.com/repository/public/", "http://localhost:8081/repository/local/")

		remote := MavenMatcher{Mirrors: local}.Match(mvn(http.MethodGet, log4jParentPOM, "https://nexus.example.com"))
		assert.Empty(t, remote.Alternates)

		onLocalhost := MavenMatcher{Mirrors: local}.Match(mvn(http.MethodGet, "/repository/local/org/slf4j/slf4j-api/2.0.9/slf4j-api-2.0.9.jar", "http://localhost:8081"))
		assert.NotEmpty(t, onLocalhost.Alternates, "external:* doesn't cover repositories on localhost")
	})

	t.Run("when no mirrors are known", func(t *testing.T) {
		t.Parallel()
		for _, mm := range []MavenMatcher{{}, {Mirrors: &MavenMirrors{}}} {
			m := mm.Match(mvn(http.MethodGet, log4jParentPOM, "https://nexus.example.com"))
			assert.Contains(t, candidateNames(m), "log4j:log4j", "without known mirrors the prefix is still guessed")
		}
	})
}

func TestMavenMirrorKnownURLsThroughTunnel(t *testing.T) {
	t.Parallel()

	get := func(t *testing.T, checker policy.Checker) (int, *Proxy) {
		t.Helper()
		upstream := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		t.Cleanup(upstream.Close)
		p := startProxyWithMatcher(t, upstream, MavenMatcher{Mirrors: knownMirrors(t, false, upstream.URL+"/repository/public/")}, checker)
		client := newClient(t, p)
		defer client.CloseIdleConnections()
		resp, err := client.Get(upstream.URL + log4jParentPOM)
		require.NoError(t, err)
		defer resp.Body.Close()
		return resp.StatusCode, p
	}

	t.Run("when a rule names Log4j 1", func(t *testing.T) {
		t.Parallel()
		status, p := get(t, policy.NewCachingChecker(&denyChecker{name: "log4j:log4j"}, nil))

		assert.Equal(t, http.StatusOK, status)
		assert.Empty(t, p.Verdicts())
	})

	t.Run("when a rule names the real parent POM", func(t *testing.T) {
		t.Parallel()
		status, p := get(t, policy.NewCachingChecker(&denyChecker{name: "org.apache.logging.log4j:log4j"}, nil))

		assert.Equal(t, http.StatusForbidden, status)
		verdicts := p.Verdicts()
		require.Len(t, verdicts, 1)
		assert.Equal(t, "org.apache.logging.log4j:log4j", verdicts[0].Package)
	})
}
