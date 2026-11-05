//go:build !integration

package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
)

// mvn builds a request against Maven Central by default; pass host to
// simulate a mirror instead.
func mvn(method, path string, host ...string) *http.Request {
	h := "https://repo.maven.apache.org"
	if len(host) > 0 {
		h = host[0]
	}
	return httptest.NewRequest(method, h+path, http.NoBody)
}

func TestMavenMatchJarDownload(t *testing.T) {
	t.Parallel()
	m := MavenMatcher{}.Match(mvn(http.MethodGet,
		"/maven2/org/slf4j/slf4j-api/2.0.13/slf4j-api-2.0.13.jar"))
	assert.True(t, m.Matched)
	assert.Equal(t, policy.Coordinate{Ecosystem: "maven", Name: "org.slf4j:slf4j-api", Version: "2.0.13"}, m.Coordinate)
	assert.Equal(t, policy.Download, m.Operation)
}

func TestMavenMatchPomDownload(t *testing.T) {
	t.Parallel()
	m := MavenMatcher{}.Match(mvn(http.MethodGet,
		"/maven2/org/slf4j/slf4j-api/2.0.13/slf4j-api-2.0.13.pom"))
	assert.True(t, m.Matched)
	assert.Equal(t, "org.slf4j:slf4j-api", m.Coordinate.Name)
}

func TestMavenSidecarChecksumIsNoMatch(t *testing.T) {
	t.Parallel()
	m := MavenMatcher{}.Match(mvn(http.MethodGet,
		"/maven2/org/slf4j/slf4j-api/2.0.13/slf4j-api-2.0.13.jar.sha1"))
	assert.False(t, m.Matched)
}

func TestMavenMetadataIsNoMatch(t *testing.T) {
	t.Parallel()
	m := MavenMatcher{}.Match(mvn(http.MethodGet,
		"/maven2/org/slf4j/slf4j-api/maven-metadata.xml"))
	assert.False(t, m.Matched)
}

func TestMavenUploadPut(t *testing.T) {
	t.Parallel()
	m := MavenMatcher{}.Match(mvn(http.MethodPut,
		"/maven2/org/slf4j/slf4j-api/2.0.13/slf4j-api-2.0.13.jar"))
	assert.True(t, m.Matched)
	assert.Equal(t, policy.Upload, m.Operation)
	assert.Equal(t, "org.slf4j:slf4j-api", m.Coordinate.Name)
}

func TestMavenSnapshotLiteralDownload(t *testing.T) {
	t.Parallel()
	m := MavenMatcher{}.Match(mvn(http.MethodGet,
		"/maven2/org/slf4j/slf4j-api/1.0-SNAPSHOT/slf4j-api-1.0-SNAPSHOT.jar"))
	assert.True(t, m.Matched)
	assert.Equal(t, policy.Coordinate{Ecosystem: "maven", Name: "org.slf4j:slf4j-api", Version: "1.0-SNAPSHOT"}, m.Coordinate)
}

func TestMavenSnapshotTimestampedDownloadIsMatched(t *testing.T) {
	t.Parallel()
	// A deployed snapshot artifact carries a timestamp instead of the literal
	// "SNAPSHOT"; it must still be matched so the download is policy-checked
	// rather than bypassing the firewall.
	m := MavenMatcher{}.Match(mvn(http.MethodGet,
		"/maven2/org/slf4j/slf4j-api/1.0-SNAPSHOT/slf4j-api-1.0-20240101.123456-1.jar"))
	assert.True(t, m.Matched)
	assert.Equal(t, policy.Coordinate{Ecosystem: "maven", Name: "org.slf4j:slf4j-api", Version: "1.0-SNAPSHOT"}, m.Coordinate)
}

func TestMavenSnapshotTimestampedSidecarIsNoMatch(t *testing.T) {
	t.Parallel()
	m := MavenMatcher{}.Match(mvn(http.MethodGet,
		"/maven2/org/slf4j/slf4j-api/1.0-SNAPSHOT/slf4j-api-1.0-20240101.123456-1.jar.sha1"))
	assert.False(t, m.Matched)
}

func TestMavenDoesNotDoubleDecodePath(t *testing.T) {
	t.Parallel()
	// req.URL.Path is already decoded once by Go's parser. The matcher must
	// evaluate that once-decoded path (what the upstream server receives) and
	// not decode it a second time. Simulate a parser output where a segment
	// still contains a literal "%2e": upstream sees "...slf4j-api-2.0.13%2ejar"
	// (an unrecognized extension, not a .jar), so the matcher must not match.
	// A second PathUnescape would turn "%2ejar" into ".jar" and wrongly match.
	req := mvn(http.MethodGet,
		"/maven2/org/slf4j/slf4j-api/2.0.13/slf4j-api-2.0.13.jar")
	req.URL.Path = "/maven2/org/slf4j/slf4j-api/2.0.13/slf4j-api-2.0.13%2ejar"
	m := MavenMatcher{}.Match(req)
	assert.False(t, m.Matched, "matcher must not perform a second percent-decode of req.URL.Path")
}

func TestMavenVersionPrefixDoesNotMatchLongerFilenameVersionFailsClosed(t *testing.T) {
	t.Parallel()
	// The version directory is "1" but the filename is version "10". A plain
	// prefix check accepts "foo-10.jar" for version "1" because it starts with
	// "foo-1"; the matcher requires a boundary after the version so it does not
	// report the wrong coordinate. The request still fetches a primary artifact
	// (.jar), so rather than forwarding it uninspected it fails closed.
	m := MavenMatcher{}.Match(mvn(http.MethodGet,
		"/maven2/com/example/foo/1/foo-10.jar"))
	assert.True(t, m.Matched)
	assert.False(t, m.Pass, "a primary artifact whose coordinate cannot be parsed must fail closed")
	assert.Equal(t, "maven", m.Coordinate.Ecosystem)
}

func TestMavenClassifierArtifactIsMatched(t *testing.T) {
	t.Parallel()
	// "<artifact>-<version>-<classifier>.<ext>" is a valid Maven filename; the
	// "-" after the version is a legitimate boundary and must still match.
	m := MavenMatcher{}.Match(mvn(http.MethodGet,
		"/maven2/org/slf4j/slf4j-api/2.0.13/slf4j-api-2.0.13-sources.jar"))
	assert.True(t, m.Matched)
	assert.Equal(t, policy.Coordinate{Ecosystem: "maven", Name: "org.slf4j:slf4j-api", Version: "2.0.13"}, m.Coordinate)
}

func TestMavenAarAndWarAreMatched(t *testing.T) {
	t.Parallel()
	aar := MavenMatcher{}.Match(mvn(http.MethodGet,
		"/maven2/com/example/widget/1.2.3/widget-1.2.3.aar"))
	assert.True(t, aar.Matched)
	assert.Equal(t, "com.example:widget", aar.Coordinate.Name)

	war := MavenMatcher{}.Match(mvn(http.MethodGet,
		"/maven2/com/example/app/1.2.3/app-1.2.3.war"))
	assert.True(t, war.Matched)
	assert.Equal(t, "com.example:app", war.Coordinate.Name)
}

func TestMavenPrimaryOutsideRepositoryLayoutIsForwarded(t *testing.T) {
	t.Parallel()
	// The matcher sees every request in a gradle or mvn session, not only
	// repository traffic. A primary extension whose path does not name a
	// group/artifact/version coordinate is an ordinary download, so it is
	// forwarded rather than blocked.
	urls := map[string]string{
		"github release":     "https://github.com/foo/bar/releases/download/v1.2.3/bar-all.jar",
		"ivy layout":         "https://repo.typesafe.com/typesafe/ivy-releases/org.scala-sbt/sbt/1.0.0/jars/sbt.jar",
		"tool download":      "https://download.example.com/tools/lombok.jar",
		"too few segments":   "https://repo.maven.apache.org/maven2/foo-1.0.jar",
		"other artifact dir": "https://repo.maven.apache.org/maven2/com/example/foo/1.0/bar-1.0.jar",
	}
	for name, u := range urls {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m := MavenMatcher{}.Match(httptest.NewRequest(http.MethodGet, u, http.NoBody))
			assert.False(t, m.Matched)
		})
	}
}

func TestMavenFilenameClaimingOtherVersionFailsClosed(t *testing.T) {
	t.Parallel()
	// The filename names the artifact in its directory but a different
	// version, so the coordinate cannot be trusted: fail closed.
	m := MavenMatcher{}.Match(mvn(http.MethodGet, "/maven2/com/example/foo/1.0/foo-2.0.jar"))
	assert.True(t, m.Matched)
	assert.False(t, m.Pass)
	assert.Equal(t, "maven", m.Coordinate.Ecosystem)
}
