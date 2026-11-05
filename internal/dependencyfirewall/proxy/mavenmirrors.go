package proxy

import (
	"cmp"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"

	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
)

// MavenMirrors holds the mirror URLs Maven sends every repository download
// through, once they are known, so MavenMatcher can read a download under one
// exactly instead of trying every prefix a mirror could have added. The zero
// value knows no mirrors. It is safe for concurrent use.
type MavenMirrors struct {
	set atomic.Pointer[mavenMirrorSet]
}

type mavenMirrorSet struct {
	bases []mavenBase // longest path first
	// externalOnly is set when the catch-all mirror is external:*, which
	// Maven doesn't apply to repositories on localhost.
	externalOnly bool
}

// mavenBase is a mirror URL reduced to what a request is compared on.
type mavenBase struct {
	scheme, host, port, path string
}

// Set makes downloads under urls match exactly. externalOnly reports that the
// catch-all mirror is external:*, so a request to a loopback host may come
// from a repository Maven didn't mirror and is left to enumeration.
func (m *MavenMirrors) Set(urls []*url.URL, externalOnly bool) {
	set := &mavenMirrorSet{externalOnly: externalOnly}
	for _, u := range urls {
		if b, ok := newMavenBase(u); ok {
			set.bases = append(set.bases, b)
		}
	}
	slices.SortStableFunc(set.bases, func(a, b mavenBase) int { return cmp.Compare(len(b.path), len(a.path)) })
	known := make([]string, len(set.bases))
	for i, b := range set.bases {
		known[i] = b.scheme + "://" + net.JoinHostPort(b.host, b.port) + b.path
	}
	dbg.Debugf("dependency firewall: matching maven downloads exactly under %s (external only: %t)", strings.Join(known, ", "), externalOnly)
	m.set.Store(set)
}

// newMavenBase normalises u's scheme, host and port, and gives its path a
// trailing slash so a prefix match ends on a segment boundary.
func newMavenBase(u *url.URL) (mavenBase, bool) {
	b := mavenBase{scheme: strings.ToLower(u.Scheme), host: strings.ToLower(u.Hostname()), port: u.Port(), path: u.Path}
	if b.port == "" {
		switch b.scheme {
		case "https":
			b.port = "443"
		case "http":
			b.port = "80"
		}
	}
	if b.host == "" || b.port == "" {
		return mavenBase{}, false
	}
	if !strings.HasSuffix(b.path, "/") {
		b.path += "/"
	}
	return b, true
}

// match reads a download under the known mirror URLs. ok is false when m
// knows no mirrors or req isn't under one, leaving req to enumeration.
func (m *MavenMirrors) match(req *http.Request, op policy.Operation) (Match, bool) {
	// Maven mirrors only resolution: an upload goes to a deploy repository,
	// whose URL can sit under a mirror's.
	if m == nil || op != policy.Download {
		return Match{}, false
	}
	set := m.set.Load()
	if set == nil {
		return Match{}, false
	}
	origin, ok := newMavenBase(req.URL)
	if !ok || set.externalOnly && IsLoopbackHost(origin.host) {
		return Match{}, false
	}
	var under bool
	var coords []policy.Coordinate
	for _, b := range set.bases {
		if b.scheme != origin.scheme || b.host != origin.host || b.port != origin.port {
			continue
		}
		rest, ok := strings.CutPrefix(req.URL.Path, b.path)
		if !ok {
			continue
		}
		// Maven never sends empty or dot segments, and a server that collapses
		// them would serve a different coordinate than the path names.
		if slices.ContainsFunc(strings.Split(strings.TrimSuffix(rest, "/"), "/"), func(s string) bool {
			return s == "" || s == "." || s == ".."
		}) {
			return Match{}, false
		}
		under = true
		name, version, result := mavenLayoutCoordinate(rest)
		switch result {
		case mavenUnparseable:
			return unparseableMatch("maven", op, "unparseable maven artifact coordinate"), true
		case mavenParsed:
			c := policy.Coordinate{Ecosystem: "maven", Name: name, Version: version}
			if !slices.Contains(coords, c) {
				coords = append(coords, c)
			}
		}
	}
	if !under {
		return Match{}, false
	}
	if len(coords) == 0 {
		return Match{}, true
	}
	exact := Match{Matched: true, Pass: true, Coordinate: coords[0], Operation: op}
	if len(coords) > 1 {
		exact.Alternates = coords[1:]
	}
	return exact, true
}

// IsLoopbackHost reports whether host is localhost or a loopback address, a
// superset of the hosts Maven treats as local (localhost and 127.0.0.1).
func IsLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
