package policy

import (
	"context"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
)

// fakeChecker is a placeholder policy source driven by GLAB_DF_FAKE_*
// environment variables. It is selected when any such variable is set and
// is used for testing. It never returns an error.
type fakeChecker struct {
	block []matcher
	warn  []matcher
	deflt verdict.Verdict // "" (allow), verdict.Blocked, or verdict.Warning
}

// matcher is one parsed GLAB_DF_FAKE_* list entry: ecosystem + name, with
// an optional exact version. A zero version matches any version.
type matcher struct {
	ecosystem string
	name      string
	version   string // "" = any version
}

func (m matcher) matches(c Coordinate) bool {
	if m.ecosystem != c.Ecosystem || m.name != c.Name {
		return false
	}
	return m.version == "" || m.version == c.Version
}

func newFakeChecker(environ []string) Checker {
	env := envMap(environ)
	return fakeChecker{
		block: parseList(env["GLAB_DF_FAKE_BLOCK"]),
		warn:  parseList(env["GLAB_DF_FAKE_WARN"]),
		deflt: parseDefault(env["GLAB_DF_FAKE_DEFAULT"]),
	}
}

func envMap(environ []string) map[string]string {
	m := make(map[string]string, len(environ))
	for _, e := range environ {
		k, v, ok := strings.Cut(e, "=")
		if ok {
			m[k] = v
		}
	}
	return m
}

// parseList turns "eco:name@version,eco:name" into matchers. Malformed
// entries (missing ecosystem or name) are skipped.
func parseList(raw string) []matcher {
	var out []matcher
	for item := range strings.SplitSeq(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		eco, rest, ok := strings.Cut(item, ":")
		if !ok || eco == "" {
			continue
		}
		name := rest
		version := ""
		// Split on the last "@" so scoped npm names (for example
		// "@babel/core@7.24.0") keep their leading "@" in the name.
		if at := strings.LastIndex(rest, "@"); at > 0 {
			name, version = rest[:at], rest[at+1:]
		}
		if name == "" {
			continue
		}
		out = append(out, matcher{ecosystem: eco, name: name, version: version})
	}
	return out
}

// parseDefault returns the default verdict for GLAB_DF_FAKE_DEFAULT. An
// empty (unset) value means allow; any other unrecognized value is treated
// as allow but logged, so a typo (e.g. "blck") is surfaced rather than
// silently masking a misconfiguration.
func parseDefault(raw string) verdict.Verdict {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", "allow":
		return ""
	case "block":
		return verdict.Blocked
	case "warn":
		return verdict.Warning
	default:
		dbg.Debugf("policy: unrecognized GLAB_DF_FAKE_DEFAULT value %q, treating as allow", raw)
		return ""
	}
}

// bestMatch returns the strongest matching entry's verdict. Exact-version
// matches take precedence over any-version matches; within the same
// specificity, block is consulted before warn. Returns ("", false) when
// nothing matches.
func (f fakeChecker) bestMatch(c Coordinate) (verdict.Verdict, bool) {
	// Exact version first.
	if hasExact(f.block, c) {
		return verdict.Blocked, true
	}
	if hasExact(f.warn, c) {
		return verdict.Warning, true
	}
	// Then any-version.
	if hasAny(f.block, c) {
		return verdict.Blocked, true
	}
	if hasAny(f.warn, c) {
		return verdict.Warning, true
	}
	return "", false
}

func hasExact(ms []matcher, c Coordinate) bool {
	for _, m := range ms {
		if m.version != "" && m.matches(c) {
			return true
		}
	}
	return false
}

func hasAny(ms []matcher, c Coordinate) bool {
	for _, m := range ms {
		if m.version == "" && m.matches(c) {
			return true
		}
	}
	return false
}

func (f fakeChecker) Check(_ context.Context, r Request) (Result, error) {
	v, matched := f.bestMatch(r.Coordinate)
	source := "GLAB_DF_FAKE_BLOCK/WARN"
	if !matched {
		v = f.deflt
		source = "GLAB_DF_FAKE_DEFAULT"
	}
	switch v {
	case verdict.Blocked:
		return Result{Verdict: verdict.Blocked, Reason: "blocked by " + source}, nil
	case verdict.Warning:
		return Result{Verdict: verdict.Warning, Reason: "warned by " + source}, nil
	default:
		return Result{}, nil
	}
}
