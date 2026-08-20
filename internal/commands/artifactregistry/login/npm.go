package login

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// npmrcPathFor returns the .npmrc this command writes: npm's user config.
//
// npm_config_userconfig wins over ~/.npmrc, because npm reads it there: the
// userconfig option defaults to "~/.npmrc" and "may be overridden by the
// npm_config_userconfig environment variable or the --userconfig command line
// option". Writing ~/.npmrc on a machine that sets it leaves the token in a
// file npm never reads, and the command still prints a green check. The
// command-line option is out of reach from here, so a user passing
// --userconfig has to point this command at the same file themselves.
func npmrcPathFor(home string) string {
	// npm lower-cases every npm_config_* variable it reads, so both spellings
	// reach it. Only the lower-cased one is authoritative when both are set:
	// that is the spelling npm's own documentation uses.
	for _, name := range []string{"npm_config_userconfig", "NPM_CONFIG_USERCONFIG"} {
		if path := os.Getenv(name); path != "" {
			return path
		}
	}
	return filepath.Join(home, ".npmrc")
}

// npmDefaultPort returns the port the WHATWG URL parser omits from the host
// for scheme, which is the port npm therefore leaves out of its .npmrc key.
// An unrecognized scheme has none, and an absent port compares equal to that,
// so the caller's comparison holds either way.
func npmDefaultPort(scheme string) string {
	switch strings.ToLower(scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	default:
		return ""
	}
}

// loginNpm upserts a "//{host}{path}/:_authToken=" line in npm's user config
// for registry, matched by exact line-prefix (not substring), so that
// "//registry.example.com/" never matches
// "//registry.example.com.evil.com/".
//
// The host keeps its port, unlike sbt's: npm's .npmrc keys are the registry
// URL including the port, and npm matches on that.
func loginNpm(registry *url.URL, token string) error {
	// Lower-cased because npm derives this key by parsing the registry URL,
	// which normalizes the host to lower case, while url.Parse preserves what
	// the user typed. Writing "//AR.Example.com/:_authToken=" would leave npm
	// looking up "//ar.example.com/:_authToken=" and finding nothing: a 401 at
	// install time with nothing wrong at login time.
	//
	// The port survives for the same reason it is dropped when it is the
	// scheme's default: npm's key is whatever the WHATWG URL parser calls the
	// host, and that keeps ":8443" while omitting ":443" on https and ":80" on
	// http. Writing the default port would be another key npm never looks up.
	host := strings.ToLower(registry.Host)
	if registry.Port() == npmDefaultPort(registry.Scheme) {
		host = strings.ToLower(registry.Hostname())
	}

	// EscapedPath, not Path: npm builds the key from the URL's pathname, which
	// keeps percent-encoding, and walks it up one segment at a time without
	// ever decoding. For a GitLab package endpoint carrying an encoded project
	// path, ".../projects/group%2Fproj/packages/npm/", the decoded spelling is
	// a key npm never looks up, so the request goes out anonymous. URL paths
	// are case-sensitive, so unlike the host this is left as typed.
	path := strings.TrimSuffix(registry.EscapedPath(), "/")

	prefix := "//" + host + path + "/:_authToken="
	line := prefix + token

	home, err := homeDir()
	if err != nil {
		return err
	}

	return upsertLines(npmrcPathFor(home), func(lines []string) ([]string, error) {
		found := false
		for i, l := range lines {
			// Leading whitespace is skipped because npm's ini parser trims
			// every key before matching it, so "  //ar.example.com/:_authToken="
			// is a live entry. Matching it strictly would append a second one
			// per login and leave the superseded token in the file.
			indent := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
			if !strings.HasPrefix(l[len(indent):], prefix) {
				continue
			}
			lines[i] = indent + line
			found = true
		}
		if !found {
			lines = append(lines, line)
		}
		return lines, nil
	})
}
