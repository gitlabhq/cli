package login

import (
	"os"
	"path/filepath"
	"strings"
)

// gradlePropertyKey returns the key that line assigns to, or "" when line is
// not a "key=value" assignment at all.
//
// Whitespace around the key and the "=" is tolerated because
// java.util.Properties tolerates it: a plain "key=" prefix test misses
// "myAliasPassword = old", so a refresh would append a second assignment and
// leave the superseded token sitting in the file forever. Later assignments win
// in a properties file, so the login would still work, which is exactly what
// makes the stale credential easy to miss.
//
// A ":" or bare-space separator, which Properties also accepts, is still not
// recognized. Those are rare in a hand-written gradle.properties, and the cost
// of missing one is the duplicate-plus-stale-line case above rather than a
// broken login.
func gradlePropertyKey(line string) string {
	key, _, ok := strings.Cut(line, "=")
	if !ok {
		return ""
	}
	return strings.TrimSpace(key)
}

// gradlePropertiesValue escapes value for the right-hand side of a
// java.util.Properties assignment, where "\" is the escape character rather
// than a literal.
//
// Properties reads an unescaped "\" before an ordinary character as that
// character ("\b" is read back as "b"), and one at the end of a line as a join
// with the line below. So a --registry ending in "\" would swallow the
// assignment written after it: the URL property would absorb
// "{alias}Username=__token__", which would then never be defined, and Gradle
// would authenticate with no username after a login that printed a green
// check.
//
// Only "\" is escaped. The other characters Properties treats specially, "=",
// ":" and leading whitespace, are only special in a key, and every key here is
// an alias that registryAliasRe or defaultRegistryAlias has already restricted
// to letters, digits, ".", "_" and "-".
func gradlePropertiesValue(value string) string {
	return strings.ReplaceAll(value, `\`, `\\`)
}

// gradleIsComment reports whether line is a comment, which
// java.util.Properties recognizes by its first non-blank character being "#"
// or "!".
func gradleIsComment(line string) bool {
	trimmed := strings.TrimLeft(line, " \t\f")
	return strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "!")
}

// gradleContinuedLines reports, for each line, whether it continues the
// previous line's value rather than starting an assignment of its own, and
// whether the file's last line leaves a continuation open with nothing after
// it.
//
// java.util.Properties joins a line ending in an odd number of backslashes
// with the line after it, so a "myAliasPassword=x" arriving as such a
// follow-on line is part of somebody else's value. Rewriting one would put the
// token inside that unrelated value and leave the alias's own property unset:
// Gradle would authenticate with nothing, and this command would report a
// login no build has.
//
// A comment never joins, which the spec is explicit about: "a comment line
// cannot be extended in this manner; every natural line that is a comment must
// have its own comment indicator". Reading "# see docs \" as a join would hide
// the real assignment below it, so a refresh would append a second one and
// leave the superseded token in the file. A line that is itself a continuation
// is not a comment however much its first character looks like one, so the
// carve-out only applies where a logical line starts.
func gradleContinuedLines(lines []string) ([]bool, bool) {
	continued := make([]bool, len(lines))

	joins := false
	for i, line := range lines {
		continued[i] = joins

		trimmed := strings.TrimSuffix(line, "\r")
		if !joins && gradleIsComment(trimmed) {
			joins = false
			continue
		}
		joins = (len(trimmed)-len(strings.TrimRight(trimmed, `\`)))%2 == 1
	}

	return continued, joins
}

// gradlePropertiesPathFor returns the gradle.properties this command writes,
// under the Gradle User Home.
//
// GRADLE_USER_HOME wins over ~/.gradle, because that is where Gradle then
// reads it: "By default, the Gradle User Home (~/.gradle ...) stores global
// configuration properties ... It can be set with the environment variable
// GRADLE_USER_HOME". Ignoring it on a machine that sets it, which is common in
// CI, writes a gradle.properties no build ever opens, and the command still
// prints a green check.
func gradlePropertiesPathFor(home string) string {
	dir := os.Getenv("GRADLE_USER_HOME")
	if dir == "" {
		dir = filepath.Join(home, ".gradle")
	}
	return filepath.Join(dir, "gradle.properties")
}

// loginGradle upserts three properties in the Gradle User Home's
// gradle.properties: "{alias}Url", "{alias}Username" (always "__token__"), and
// "{alias}Password" (token). Matching keys are updated in place; unrelated
// lines and their order are preserved.
func loginGradle(registry, alias, token string) error {
	home, err := homeDir()
	if err != nil {
		return err
	}

	path := gradlePropertiesPathFor(home)

	props := []struct{ key, value string }{
		{alias + "Url", gradlePropertiesValue(registry)},
		{alias + "Username", "__token__"},
		{alias + "Password", gradlePropertiesValue(token)},
	}

	return upsertLines(path, func(lines []string) ([]string, error) {
		continued, _ := gradleContinuedLines(lines)

		found := make(map[string]struct{}, len(props))
		// A replaced property's own continuation lines go with it. The new
		// value is one line, so leaving them would strand the tail of the
		// superseded token in the file as a stray property of its own.
		drop := make([]bool, len(lines))
		for i, line := range lines {
			if continued[i] {
				continue
			}
			key := gradlePropertyKey(line)
			for _, p := range props {
				if key != p.key {
					continue
				}
				lines[i] = p.key + "=" + p.value
				found[p.key] = struct{}{}
				for j := i + 1; j < len(lines) && continued[j]; j++ {
					drop[j] = true
				}
			}
		}

		kept := lines[:0]
		for i, line := range lines {
			if !drop[i] {
				kept = append(kept, line)
			}
		}
		lines = kept

		// Recomputed, since dropping a continuation line can have taken the
		// open continuation with it.
		_, dangling := gradleContinuedLines(lines)

		if len(found) < len(props) && dangling {
			// The last line leaves a continuation open, so the first property
			// appended below would be read as the rest of that property's
			// value: the assignment would never be defined and the unrelated
			// property would be corrupted. An empty line closes the
			// continuation without changing the value it belongs to, since
			// Properties strips a continuation line's leading whitespace and an
			// empty one contributes nothing.
			lines = append(lines, "")
		}

		for _, p := range props {
			if _, ok := found[p.key]; !ok {
				lines = append(lines, p.key+"="+p.value)
			}
		}
		return lines, nil
	})
}
