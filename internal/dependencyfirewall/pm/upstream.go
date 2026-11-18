package pm

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"golang.org/x/net/http/httpproxy"

	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/proxy"
)

// upstreamProxy is the outbound proxy a package manager would have used on its
// own. Run points the manager's proxy settings at the inspection proxy, so the
// inspection proxy uses this one for its own connection to the registry.
type upstreamProxy struct {
	// Source names the setting the proxy came from, for diagnostics.
	Source string
	// URL is the proxy as the manager reads it. A value without a scheme is
	// taken as http://, as the managers do. Empty means the manager connects
	// directly, without falling back to HTTPS_PROXY or HTTP_PROXY.
	URL string
	// NoProxy lists the hosts the manager reaches directly, in NO_PROXY syntax.
	NoProxy string
	// NonProxyHosts lists more hosts this proxy skips, as JVM-style patterns
	// in which "*" matches any run of characters, ignoring case.
	NonProxyHosts []string
	// Fallbacks are tried in order for a host this proxy skips, as Maven does
	// with its other proxies. A host that every proxy skips is reached
	// directly.
	Fallbacks []upstreamProxy
}

// upstreamProxyResolver is implemented by managers that read a proxy setting of
// their own ahead of HTTPS_PROXY and HTTP_PROXY, from their environment
// variables or their user and global config files. For any other manager, the
// inspection proxy uses glab's HTTPS_PROXY, HTTP_PROXY and NO_PROXY.
type upstreamProxyResolver interface {
	// resolveUpstreamProxy reports the proxy the manager would use for HTTPS
	// requests when run in dir with environ and args, or false when it has
	// none beyond HTTPS_PROXY. It fails when a config file it reads is
	// unreadable or malformed.
	resolveUpstreamProxy(environ []string, dir string, args []string) (upstreamProxy, bool, error)
	// proxyVariables lists the manager's own proxy variables that its
	// resolvers read, for help text.
	proxyVariables() []string
	// proxyConfigFiles lists the user and global config files its resolvers
	// read, for help text.
	proxyConfigFiles() []string
}

// plainHTTPProxyResolver is implemented by managers that pick a different
// proxy for plain-HTTP registries than for HTTPS ones. Other resolvers' HTTPS
// proxy carries plain-HTTP requests too, as npm, pnpm, pip and cargo do.
type plainHTTPProxyResolver interface {
	// resolvePlainHTTPProxy reports the proxy the manager would use for
	// plain-HTTP requests when run in dir with environ, or false when it has
	// none beyond HTTP_PROXY.
	resolvePlainHTTPProxy(environ []string, dir string) (upstreamProxy, bool, error)
}

// OwnProxyVariables lists the proxy variables of manager's own that glab reads
// ahead of HTTPS_PROXY and HTTP_PROXY, or nil when it reads none.
func OwnProxyVariables(manager PackageManager) []string {
	if r, ok := manager.(upstreamProxyResolver); ok {
		return r.proxyVariables()
	}
	return nil
}

// OwnProxyConfigFiles lists the user and global config files of manager's own
// that glab reads proxy settings from, or nil when it reads none. glab skips
// any of them that is inside the project being installed.
func OwnProxyConfigFiles(manager PackageManager) []string {
	if r, ok := manager.(upstreamProxyResolver); ok {
		return r.proxyConfigFiles()
	}
	return nil
}

// OwnProxyHelpNote returns what the help for manager's proxy settings must
// add, such as settings that only some of its versions use, or "".
func OwnProxyHelpNote(manager PackageManager) string {
	if n, ok := manager.(interface{ proxyHelpNote() string }); ok {
		return n.proxyHelpNote()
	}
	return ""
}

// PlainHTTPUsesHTTPSProxy reports whether manager sends plain-HTTP requests
// through HTTPS_PROXY ahead of HTTP_PROXY, as npm and pnpm do.
func PlainHTTPUsesHTTPSProxy(manager PackageManager) bool {
	_, ok := manager.(npmManager)
	return ok
}

// upstreamProxyOptions returns the proxy options that send the inspection
// proxy's registry traffic through the outbound proxy the manager would have
// used for each scheme, read from environ before Run overrides it.
func upstreamProxyOptions(manager PackageManager, environ []string, dir string, args []string) ([]proxy.Option, error) {
	resolver, ok := manager.(upstreamProxyResolver)
	if !ok {
		return nil, nil
	}
	httpsProxy, httpsOK, err := resolver.resolveUpstreamProxy(environ, dir, args)
	if err != nil {
		return nil, fmt.Errorf("failed to read the %s proxy settings: %w", manager.Name(), err)
	}
	// npm and pnpm send plain HTTP through the same proxy, even a bare
	// HTTPS_PROXY ahead of HTTP_PROXY (verified against npm 10.9, pnpm 10.12).
	plainProxy, plainOK := httpsProxy, httpsOK
	if r, ok := manager.(plainHTTPProxyResolver); ok {
		if plainProxy, plainOK, err = r.resolvePlainHTTPProxy(environ, dir); err != nil {
			return nil, fmt.Errorf("failed to read the %s proxy settings: %w", manager.Name(), err)
		}
	}
	var opts []proxy.Option
	for _, s := range []struct {
		scheme   string
		upstream upstreamProxy
		ok       bool
	}{{"https", httpsProxy, httpsOK}, {"http", plainProxy, plainOK}} {
		if !s.ok {
			continue
		}
		proxyFunc, err := s.upstream.proxyFunc()
		if err != nil {
			return nil, fmt.Errorf("invalid outbound proxy in %s: %w", s.upstream.Source, err)
		}
		if s.upstream.URL == "" {
			dbg.Debugf("dependency firewall: reaching %s registries directly, as %s disables the proxy", s.scheme, s.upstream.Source)
		} else {
			dbg.Debugf("dependency firewall: reaching %s registries through the outbound proxy from %s", s.scheme, s.upstream.Source)
		}
		opts = append(opts, proxy.WithUpstreamProxyFor(s.scheme, proxyFunc))
	}
	return opts, nil
}

// proxyFunc returns a proxy selector for the inspection proxy's upstream
// transport. It validates every URL up front so a typo fails the run with a
// clear message, instead of every registry request failing later.
func (u upstreamProxy) proxyFunc() (func(*http.Request) (*url.URL, error), error) {
	if u.URL == "" {
		return func(*http.Request) (*url.URL, error) { return nil, nil }, nil
	}
	if err := validateProxyURL(u.URL); err != nil {
		return nil, err
	}
	// upstreamProxyOptions names u.Source, so only a fallback names its own.
	for _, f := range u.Fallbacks {
		if err := validateProxyURL(f.URL); err != nil {
			return nil, fmt.Errorf("invalid outbound proxy in %s: %w", f.Source, err)
		}
	}
	candidates := append([]upstreamProxy{u}, u.Fallbacks...)
	picks := make([]func(*url.URL) (*url.URL, error), 0, len(candidates))
	for _, c := range candidates {
		pick := (&httpproxy.Config{HTTPProxy: c.URL, HTTPSProxy: c.URL, NoProxy: c.NoProxy}).ProxyFunc()
		nonProxyHosts := c.NonProxyHosts
		picks = append(picks, func(target *url.URL) (*url.URL, error) {
			if matchesHostPattern(nonProxyHosts, target.Hostname()) {
				return nil, nil
			}
			return pick(target)
		})
	}
	return func(r *http.Request) (*url.URL, error) {
		for _, pick := range picks {
			if p, err := pick(r.URL); p != nil || err != nil {
				return p, err
			}
		}
		return nil, nil
	}, nil
}

// matchesHostPattern reports whether host matches one of patterns, where "*"
// matches any run of characters and case is ignored.
func matchesHostPattern(patterns []string, host string) bool {
	host = strings.ToLower(host)
	for _, p := range patterns {
		parts := strings.Split(strings.ToLower(p), "*")
		if len(parts) == 1 {
			if parts[0] == host {
				return true
			}
			continue
		}
		rest, ok := strings.CutPrefix(host, parts[0])
		if !ok {
			continue
		}
		for _, mid := range parts[1 : len(parts)-1] {
			i := strings.Index(rest, mid)
			if i < 0 {
				ok = false
				break
			}
			rest = rest[i+len(mid):]
		}
		if ok && strings.HasSuffix(rest, parts[len(parts)-1]) {
			return true
		}
	}
	return false
}

// validateProxyURL checks raw as httpproxy reads it, taking a value without a
// scheme as http://. Errors never include raw, because a proxy URL can carry
// credentials.
func validateProxyURL(raw string) error {
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return errors.New("not a valid proxy URL")
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
		return nil
	default:
		return errors.New("unsupported proxy scheme; use http, https, socks5 or socks5h")
	}
}

// firstSetEnv returns the name and value of the first key in keys with a
// non-empty value in environ, and false when none is set. Package managers
// treat an empty value as unset. With fold, keys match case-insensitively, as
// npm's npm_config_* variables do.
func firstSetEnv(environ []string, fold bool, keys ...string) (string, string, bool) {
	for _, want := range keys {
		for _, e := range slices.Backward(environ) {
			k, v, found := strings.Cut(e, "=")
			if !found || v == "" {
				continue
			}
			if k == want || (fold && strings.EqualFold(k, want)) {
				return k, v, true
			}
		}
	}
	return "", "", false
}

// joinNoProxy joins the non-empty NO_PROXY-style lists in lists.
func joinNoProxy(lists ...string) string {
	var parts []string
	for _, l := range lists {
		if l != "" {
			parts = append(parts, l)
		}
	}
	return strings.Join(parts, ",")
}

// envNoProxy returns NO_PROXY, or no_proxy when NO_PROXY is unset, matching
// http.ProxyFromEnvironment.
func envNoProxy(environ []string) string {
	_, v, _ := firstSetEnv(environ, false, "NO_PROXY", "no_proxy")
	return v
}

func (npmManager) proxyVariables() []string {
	return []string{"npm_config_https_proxy", "npm_config_proxy"}
}

func (m npmManager) proxyConfigFiles() []string {
	if m.binary != "npm" {
		return nil
	}
	return []string{"~/.npmrc", "$PREFIX/etc/npmrc"}
}

// resolveUpstreamProxy for npm and pnpm. For HTTPS requests both use
// https-proxy, then proxy, then HTTPS_PROXY, and skip the proxy for a host in
// either noproxy or NO_PROXY (verified against npm 10.9 and pnpm 10.12). npm
// also reads these settings from its user and global npmrc files. pnpm's
// config files are not read yet, so for pnpm only npm_config_* variables are.
func (m npmManager) resolveUpstreamProxy(environ []string, dir string, _ []string) (upstreamProxy, bool, error) {
	config := configLayers{npmEnvLayer(environ)}
	if m.binary == "npm" {
		var err error
		if config, err = readNpmConfig(environ, dir); err != nil {
			return upstreamProxy{}, false, err
		}
	}
	upstream, ok := npmUpstreamProxy(config, environ)
	return upstream, ok, nil
}

func (yarnManager) proxyVariables() []string {
	return []string{"YARN_HTTPS_PROXY", "YARN_PROXY", "npm_config_https_proxy", "npm_config_proxy"}
}

func (yarnManager) proxyConfigFiles() []string {
	return []string{"~/.yarnrc", "~/.npmrc", "$PREFIX/etc/yarnrc", "$PREFIX/etc/npmrc", "~/.yarnrc.yml"}
}

func (yarnManager) proxyHelpNote() string {
	return "For Yarn 2 and later, glab reads only YARN_HTTPS_PROXY and ~/.yarnrc.yml."
}

// resolveUpstreamProxy for Yarn. Both lines read YARN_HTTPS_PROXY in any case.
// Berry then reads httpsProxy in ~/.yarnrc.yml. Classic reads https-proxy,
// then proxy, from YARN_HTTPS_PROXY, YARN_PROXY and its user and global
// .yarnrc files, then from npm_config_* variables and the user and global
// .npmrc files. Neither applies NO_PROXY to these settings (verified against
// Yarn 4.9 and 1.22).
func (yarnManager) resolveUpstreamProxy(environ []string, dir string, _ []string) (upstreamProxy, bool, error) {
	berry, err := yarnIsBerry(dir)
	if err != nil {
		return upstreamProxy{}, false, err
	}
	if berry {
		return berryUpstreamProxy(environ, newConfigFiles(dir))
	}
	return yarnClassicUpstreamProxy(environ, newConfigFiles(dir))
}

// resolvePlainHTTPProxy for Yarn. For plain-HTTP requests Classic uses proxy,
// then HTTP_PROXY (verified against Yarn 1.22). Berry sends plain-HTTP
// requests through a CONNECT tunnel, so they take the HTTPS path.
func (yarnManager) resolvePlainHTTPProxy(environ []string, dir string) (upstreamProxy, bool, error) {
	berry, err := yarnIsBerry(dir)
	if berry || err != nil {
		return upstreamProxy{}, false, err
	}
	return yarnClassicPlainHTTPProxy(environ, newConfigFiles(dir))
}

func (pyPIManager) proxyVariables() []string { return []string{"PIP_PROXY"} }

func (pyPIManager) proxyConfigFiles() []string {
	return []string{"pip.conf", "$PIP_CONFIG_FILE"}
}

func (m pyPIManager) proxyHelpNote() string {
	if m.binary != "pip" {
		return ""
	}
	return "glab reads pip's --proxy option ahead of all of these."
}

// resolveUpstreamProxy for pip and pipenv, as pip 26.0 reads its settings:
// pip's --proxy option, then PIP_PROXY, then a proxy in its config files. Any
// of these makes pip ignore HTTPS_PROXY and NO_PROXY, except that an empty
// --proxy clears the other two and leaves pip on HTTPS_PROXY. pip 26.3 reads
// an empty --proxy as a direct connection instead; glab keeps HTTPS_PROXY.
func (m pyPIManager) resolveUpstreamProxy(environ []string, dir string, args []string) (upstreamProxy, bool, error) {
	command := ""
	if m.binary == "pip" {
		if value, _, ok := pipProxyArg(args); ok {
			if value == "" {
				dbg.Debugf("dependency firewall: pip's --proxy is empty, so glab keeps its default outbound proxy")
				return upstreamProxy{}, false, nil
			}
			return upstreamProxy{Source: "--proxy", URL: value}, true, nil
		}
		command = pipCommand(args)
	}
	return pipUpstreamProxy(environ, dir, m.binary, command, m.binary == "pip" && pipIsolated(args))
}

func (cargoManager) proxyVariables() []string { return []string{"CARGO_HTTP_PROXY"} }

func (cargoManager) proxyConfigFiles() []string {
	return []string{"$CARGO_HOME/config.toml", "~/.gitconfig"}
}

// resolveUpstreamProxy for cargo. CARGO_HTTP_PROXY outranks HTTPS_PROXY, and
// libcurl still skips it for a host in NO_PROXY or no_proxy (verified against
// cargo 1.99).
func (cargoManager) resolveUpstreamProxy(environ []string, dir string, _ []string) (upstreamProxy, bool, error) {
	return cargoUpstreamProxy(environ, dir)
}

func (*mavenManager) proxyVariables() []string { return nil }

func (*mavenManager) proxyConfigFiles() []string {
	return []string{"~/.m2/settings.xml", "${maven.home}/conf/settings.xml"}
}

func (*mavenManager) proxyHelpNote() string {
	return "glab doesn't support proxy passwords encrypted with settings-security.xml."
}

// resolveUpstreamProxy for Maven: the active <proxy> in the user and global
// settings files.
func (*mavenManager) resolveUpstreamProxy(environ []string, dir string, args []string) (upstreamProxy, bool, error) {
	return mavenUpstreamProxy(environ, dir, args)
}

func (*gradleManager) proxyVariables() []string { return nil }

func (*gradleManager) proxyConfigFiles() []string {
	return []string{"$GRADLE_USER_HOME/gradle.properties", "the Gradle installation's gradle.properties"}
}

func (*gradleManager) proxyHelpNote() string {
	return "glab also reads them from -D options on the gradle command line."
}

// resolveUpstreamProxy for Gradle: the https.proxyHost system properties from
// the command line and the user and installation gradle.properties.
func (*gradleManager) resolveUpstreamProxy(environ []string, dir string, args []string) (upstreamProxy, bool, error) {
	return gradleUpstreamProxy(environ, dir, args)
}
