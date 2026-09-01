package proxy

import (
	"net/http"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
)

// GemMatcher recognizes RubyGems downloads ("/gems/<name>-<version>.gem")
// and pushes (POST "/api/v1/gems"). The push body carries the gem binary,
// so the upload coordinate has an empty Name/Version; the checker still
// receives an Upload request for a package-level decision.
type GemMatcher struct{}

const gemSuffix = ".gem"

func (GemMatcher) Match(req *http.Request) Match {
	if req == nil {
		return Match{}
	}
	// req.URL.Path is already percent-decoded once by Go's parser. Decoding
	// again would let a double-encoded request present the matcher a different
	// path than the upstream RubyGems server sees, and because an unmatched
	// request bypasses the firewall that is a policy-bypass vector. Match
	// against the once-decoded path the origin actually receives.
	path := strings.Trim(req.URL.Path, "/")

	if req.Method == http.MethodPost && (path == "api/v1/gems" || strings.HasSuffix(path, "/api/v1/gems")) {
		return Match{
			Matched:    true,
			Pass:       true,
			Coordinate: policy.Coordinate{Ecosystem: "gem"},
			Operation:  policy.Upload,
		}
	}

	if !strings.HasSuffix(path, gemSuffix) {
		return Match{}
	}
	file := path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		file = path[i+1:]
	}
	// Only the "gems/" directory serves .gem downloads; guard against
	// matching unrelated ".gem" paths. Require "gems" to be the file's
	// immediate parent directory, not merely present anywhere in the path,
	// so a path like "vendor/gems/subdir/rails-7.1.3.gem" does not match.
	dir := strings.TrimSuffix(strings.TrimSuffix(path, file), "/")
	if dir != "gems" && !strings.HasSuffix(dir, "/gems") {
		return Match{}
	}
	base := strings.TrimSuffix(file, gemSuffix)
	name, version, ok := splitGemNameVersion(base)
	if !ok {
		return Match{}
	}
	return Match{
		Matched:    true,
		Pass:       true,
		Coordinate: policy.Coordinate{Ecosystem: "gem", Name: name, Version: version},
		Operation:  policy.Download,
	}
}

// splitGemNameVersion splits "<name>-<version>[-<platform>...]" into name
// and version. The name ends at the first hyphen whose following character
// is a digit (the version start). The version then runs up to the next
// hyphen that begins a non-digit segment (the platform suffix), so
// "nokogiri-1.16.0-x86_64-linux" yields name "nokogiri", version "1.16.0".
//
// This is a heuristic: a gem whose name ends in a hyphen followed by a digit
// (for example "a1-2fast") mis-splits, because the first "-<digit>" boundary
// is taken as the version start. Gem filenames do not encode the name/version
// boundary unambiguously, so this cannot be fully correct from the path alone;
// the mis-split names are rare and the resulting coordinate still fails closed
// or over-blocks rather than allowing an unintended package.
func splitGemNameVersion(base string) (string, string, bool) {
	for idx := 0; idx < len(base); idx++ {
		if base[idx] != '-' {
			continue
		}
		if idx+1 < len(base) && base[idx+1] >= '0' && base[idx+1] <= '9' {
			name := base[:idx]
			version := base[idx+1:]
			if name == "" {
				return "", "", false
			}
			for vi := 0; vi < len(version); vi++ {
				if version[vi] == '-' && (vi+1 >= len(version) || version[vi+1] < '0' || version[vi+1] > '9') {
					version = version[:vi]
					break
				}
			}
			return name, version, true
		}
	}
	return "", "", false
}
