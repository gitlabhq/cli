package proxy

import (
	"net/http"
	"regexp"
	"slices"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
)

// MavenMatcher recognizes Maven artifact downloads and uploads against a
// public Maven upstream (repo.maven.apache.org and mirrors). The path shape
// is ".../<group/path>/<artifact>/<version>/<artifact>-<version>.<ext>",
// where the group's dots are path segments. Enforcement targets the primary
// artifacts listed in mavenPrimaryExts (.jar, .pom, .aar, .war); checksum and
// signature sidecars pass through.
type MavenMatcher struct {
	// Mirrors, when it knows them, are the mirror URLs Maven downloads
	// through; without them every prefix a mirror could add is tried.
	Mirrors *MavenMirrors
}

// mavenPrimaryExts are the artifact extensions that trigger a policy check.
// Sidecars (.sha1, .md5, .sha256, .sha512, .asc) are intentionally omitted.
var mavenPrimaryExts = []string{".jar", ".pom", ".aar", ".war"}

func (mm MavenMatcher) Match(req *http.Request) Match {
	if req == nil {
		return Match{}
	}
	op := policy.Download
	if req.Method == http.MethodPut || req.Method == http.MethodPost {
		op = policy.Upload
	}
	if m, ok := mm.Mirrors.match(req, op); ok {
		return m
	}
	name, version, result, central := mavenCoordinate(req.URL.Path)
	switch result {
	case mavenNotArtifact:
		return Match{}
	case mavenUnparseable:
		return unparseableMatch("maven", op, "unparseable maven artifact coordinate")
	}
	// The exact Central layout has no prefix to recover, and readings that drop
	// segments would only collide with relocated groups like dom4j:dom4j.
	var alternates []policy.Coordinate
	if !central {
		alternates = mavenGroupAlternates(name, version)
	}
	return Match{
		Matched:    true,
		Pass:       true,
		Coordinate: policy.Coordinate{Ecosystem: "maven", Name: name, Version: version},
		Operation:  op,
		Alternates: alternates,
	}
}

// mavenMaxGroupDepth caps mavenGroupAlternates so a pathologically deep path
// can't generate unbounded policy calls: at most twice this many readings.
const mavenMaxGroupDepth = 10

// mavenGroupAlternates builds the group readings a mirror's path prefix could
// produce, shortest first, since a rule's real target is far more likely short.
func mavenGroupAlternates(name, version string) []policy.Coordinate {
	group, artifact, ok := strings.Cut(name, ":")
	if !ok {
		return nil
	}
	parts := strings.Split(group, ".")
	if len(parts) < 2 {
		return nil
	}
	var alts []policy.Coordinate
	for start := len(parts) - 1; start >= 1; start-- {
		// The publisher chooses the groupId's depth but a repository prefix is
		// short, so keep short readings and those that drop few leading segments.
		if len(parts)-start > mavenMaxGroupDepth && start > mavenMaxGroupDepth {
			continue
		}
		seg := parts[start:]
		// A doubled slash leaves an empty segment; that's not a real group.
		if slices.Contains(seg, "") {
			continue
		}
		alts = append(alts, policy.Coordinate{
			Ecosystem: "maven",
			Name:      strings.Join(seg, ".") + ":" + artifact,
			Version:   version,
		})
	}
	return alts
}

// mavenCoordinateResult classifies a request path relative to the Maven
// artifact shape: not a Maven artifact fetch, a filename claiming a coordinate
// that does not match its path (fail closed), or a parsed coordinate.
type mavenCoordinateResult int

const (
	mavenNotArtifact mavenCoordinateResult = iota
	mavenUnparseable
	mavenParsed
)

// mavenCoordinate also reports whether path is the exact Central layout under
// a leading maven2/, whose coordinate needs no alternates.
func mavenCoordinate(path string) (string, string, mavenCoordinateResult, bool) {
	// path is req.URL.Path, which Go has already percent-decoded once. Decoding
	// again would let a double-encoded request (e.g. %2520) present the matcher
	// a different path than the upstream Maven server sees, and because an
	// unmatched request bypasses the firewall that is a policy-bypass vector.
	// Match against the once-decoded path the origin actually receives.
	rest := strings.Trim(path, "/")
	central := strings.HasPrefix(rest, "maven2/")
	name, version, result := mavenLayoutCoordinate(strings.TrimPrefix(rest, "maven2/"))
	return name, version, result, central && result == mavenParsed
}

// mavenLayoutCoordinate parses rest, a path relative to a repository's root,
// as the Maven repository layout.
func mavenLayoutCoordinate(rest string) (string, string, mavenCoordinateResult) {
	if rest == "" {
		return "", "", mavenNotArtifact
	}
	segs := strings.Split(rest, "/")
	// Sidecar and metadata requests (checksums, signatures,
	// maven-metadata.xml) are not artifact fetches and pass through untouched.
	file := segs[len(segs)-1]
	if !mavenIsPrimary(file) {
		return "", "", mavenNotArtifact
	}
	// Every request in a gradle or mvn session reaches this matcher, so a
	// primary extension alone does not make it repository traffic: GitHub
	// release jars, Ivy layouts and tool downloads carry no coordinate and are
	// forwarded like any other download.
	if len(segs) < 4 {
		return "", "", mavenNotArtifact
	}
	version := segs[len(segs)-2]
	artifact := segs[len(segs)-3]
	group := strings.Join(segs[:len(segs)-3], ".")
	if !mavenFileMatchesCoordinate(file, artifact, version) {
		// A filename that names the directory's artifact but not its version
		// (foo-10.jar under foo/1/) claims a coordinate we cannot trust: fail
		// closed.
		if strings.HasPrefix(file, artifact+"-") {
			return "", "", mavenUnparseable
		}
		return "", "", mavenNotArtifact
	}
	return group + ":" + artifact, version, mavenParsed
}

// snapshotSuffix is the version-directory marker for a Maven snapshot.
const snapshotSuffix = "-SNAPSHOT"

// mavenSnapshotTimestamp matches the "<yyyyMMdd>.<HHmmss>-<buildnumber>"
// segment Maven substitutes for "SNAPSHOT" in a deployed snapshot artifact's
// filename, for example "20240101.123456-1" in
// "slf4j-api-1.0-20240101.123456-1.jar".
var mavenSnapshotTimestamp = regexp.MustCompile(`^\d{8}\.\d{6}-\d+`)

// mavenFileMatchesCoordinate reports whether file names the artifact at
// version. The direct case requires "<artifact>-<version>" as the filename
// prefix followed by a boundary: "." for the extension (slf4j-api-1.0.jar) or
// "-" for a classifier (slf4j-api-1.0-sources.jar). The boundary matters
// because one version can prefix another ("1" prefixes "10"): without it
// "foo-10.jar" would match version "1" and report the wrong coordinate, and
// since unmatched means allow, mask the version actually being fetched.
// Snapshot deployments are the exception: under a "<base>-SNAPSHOT" version
// directory, Maven names each uploaded artifact with a unique timestamp
// instead of the literal "SNAPSHOT" (for example
// "slf4j-api-1.0-20240101.123456-1.jar" under "1.0-SNAPSHOT/"), so the direct
// prefix check would miss it and let every timestamped snapshot download
// bypass policy. Accept that timestamped form too so snapshots are evaluated.
func mavenFileMatchesCoordinate(file, artifact, version string) bool {
	if rest, ok := strings.CutPrefix(file, artifact+"-"+version); ok {
		if strings.HasPrefix(rest, ".") || strings.HasPrefix(rest, "-") {
			return true
		}
	}
	if base, ok := strings.CutSuffix(version, snapshotSuffix); ok {
		rest, matched := strings.CutPrefix(file, artifact+"-"+base+"-")
		if matched && mavenSnapshotTimestamp.MatchString(rest) {
			return true
		}
	}
	return false
}

func mavenIsPrimary(file string) bool {
	for _, ext := range mavenPrimaryExts {
		if strings.HasSuffix(file, ext) {
			return true
		}
	}
	return false
}
