package login

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

// sbtCredentialRealm is the realm sbt records the credentials under. It is
// what the Artifact Registry sends on the maven route that both --gradle and
// --sbt use: `Basic realm="artifact-registry"`, lower case and hyphenated.
//
// The realm has to match that header because Ivy keys its credentials store on
// (host, realm), so `sbt publish` finds no entry when they disagree. The
// ordinary resolve is looser: sbt's own lookup keys on the host alone
// (Credentials.forHost), and coursier's DirectCredentials.matches checks the
// host and user, with the realm gating only the credentials it resends when
// following a redirect.
const sbtCredentialRealm = "artifact-registry"

// sbtCredentialLine renders the line loginSbt writes for host.
func sbtCredentialLine(realm, host, token string) string {
	return fmt.Sprintf(`credentials += Credentials("%s", "%s", "__token__", "%s")`, realm, host, token)
}

// sbtCredentialLineRegexp builds the pattern that finds a line previously
// written by sbtCredentialLine for realm, capturing the host that appears as
// the second quoted argument.
//
// realm goes through regexp.QuoteMeta because it is a constant interpolated
// into a pattern rather than a literal. A realm carrying "(", ")" or "[" would
// still compile without it, and would still be written into the file, but would
// stop matching the line this file writes: every login would then append
// another entry holding a live token instead of refreshing the existing one.
//
// The trailing \r? tolerates a CRLF credentials.sbt. upsertLines splits on "\n"
// only, so on a CRLF file every line arrives with a trailing carriage return,
// and an anchored pattern without this would never match a line it had written.
//
// Whitespace is tolerated wherever Scala ignores it, and so is a trailing
// comment in either spelling, "//" or "/* */", which the second group captures
// so the caller can keep it. All of these are the same credential to sbt, so
// matching them strictly would append a second entry for the same host instead
// of refreshing the first. Credentials.forHost then takes the first entry it
// finds for the host, so sbt would keep using the stale token while this
// command reported a fresh login.
func sbtCredentialLineRegexp(realm string) *regexp.Regexp {
	return regexp.MustCompile(`^[ \t]*credentials[ \t]*\+=[ \t]*Credentials\([ \t]*"` + regexp.QuoteMeta(realm) + `"[ \t]*,[ \t]*"([^"]*)"[ \t]*,[ \t]*"__token__"[ \t]*,[ \t]*"[^"]*"[ \t]*\)[ \t]*((?://|/\*)[^\r]*?)?[ \t]*\r?$`)
}

// sbtCommentedLines reports, for each line, whether it starts inside a /* */
// block comment, and how deep in one the file ends.
//
// sbt compiles credentials.sbt as Scala, so a credentials line inside a
// comment is not a credential. Refreshing one in place would write a live
// token into a comment: sbt would still have nothing to authenticate with,
// and this command would report a login that no build has. Appending after a
// comment the file never closes has the same effect, which is what the
// returned depth is for.
//
// Scala nests block comments, so the depth counts up and down rather than
// toggling, and a "//" hides the rest of its own line, including a "/*" that
// would otherwise open a block.
//
// The scan is string-literal-blind, and that cuts both ways. A "/*" inside a
// string literal opens a comment here that Scala never opened, so live entries
// below it look commented and the next login appends instead of refreshing. In
// the other direction the "//" every URL carries hides the rest of its line,
// including a real "/*" after it, so an entry inside that block comment is
// refreshed in place: a live token written where sbt never reads it. Neither
// case can arise from a line this command writes; both need a hand-written one
// that puts a comment marker inside a string.
func sbtCommentedLines(lines []string) ([]bool, int) {
	commented := make([]bool, len(lines))
	depth := 0

	for i, line := range lines {
		commented[i] = depth > 0

		for j := 0; j < len(line); j++ {
			switch {
			case depth == 0 && strings.HasPrefix(line[j:], "//"):
				j = len(line)
			case strings.HasPrefix(line[j:], "/*"):
				depth++
				j++
			case depth > 0 && strings.HasPrefix(line[j:], "*/"):
				depth--
				j++
			}
		}
	}

	return commented, depth
}

var sbtCredentialLineRe = sbtCredentialLineRegexp(sbtCredentialRealm)

// sbtCredentialsPathFor returns the credentials.sbt this command writes.
//
// The directory is sbt's global base, which sbt computes as
// defaultGlobalBase/<binary version> and lets -Dsbt.global.base override.
// Neither is reachable from here: the override is a JVM property passed on the
// sbt command line or through SBT_OPTS, and the version belongs to whichever
// sbt the build runs. So this assumes a stock sbt 1.x, whose global base is
// ~/.sbt/1.0 ("Settings that should be applied to all projects can go in
// $HOME/.sbt/1.0/global.sbt, or any file in $HOME/.sbt/1.0 with a .sbt
// extension"). On an sbt that moved its global base, this file is one nothing
// loads, so --sbt writes a token no build uses; the command's help says so.
func sbtCredentialsPathFor(home string) string {
	return filepath.Join(home, ".sbt", "1.0", "credentials.sbt")
}

// loginSbt upserts a `credentials += Credentials("artifact-registry", host,
// "__token__", token)` line in sbt's global credentials.sbt for registry's
// host, matched by the host appearing as the second quoted argument, so a
// different host's line is left untouched.
func loginSbt(registry *url.URL, token string) error {
	// Hostname(), not Host: sbt's IvyAuthenticator and coursier both look
	// credentials up by the port-less requesting host
	// (Authenticator.getRequestingHost / URI.getHost). Writing "host:port"
	// here for a registry on a non-standard port would leave sbt finding no
	// match at build time and sending the request unauthenticated, with no
	// error at login time.
	//
	// Lower-cased for the same reason the match has to be reliable: host names
	// are case-insensitive, so the same registry typed two ways must land on one
	// entry rather than two that disagree about which token is current.
	host := strings.ToLower(registry.Hostname())

	line := sbtCredentialLine(sbtCredentialRealm, host, token)

	home, err := homeDir()
	if err != nil {
		return err
	}

	return upsertLines(sbtCredentialsPathFor(home), func(lines []string) ([]string, error) {
		commented, depth := sbtCommentedLines(lines)
		if depth > 0 {
			return nil, errors.New("found an unterminated /* comment: the credentials line would be appended inside it, where sbt never reads it; close the comment, or add the line by hand")
		}

		found := false
		for i, l := range lines {
			if commented[i] {
				continue
			}
			m := sbtCredentialLineRe.FindStringSubmatch(l)
			if m == nil || m[1] != host {
				continue
			}
			// The entry keeps the indentation and the trailing comment it had:
			// only the token is this command's to change, and a refresh that
			// drops the note somebody left on the line is a silent edit.
			indent := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
			lines[i] = indent + line
			if m[2] != "" {
				lines[i] += " " + m[2]
			}
			found = true
		}
		if !found {
			lines = append(lines, line)
		}
		return lines, nil
	})
}
