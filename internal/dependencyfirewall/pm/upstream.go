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
	// taken as http://, as the managers do.
	URL string
	// NoProxy lists the hosts the manager reaches directly, in NO_PROXY syntax.
	NoProxy string
}

// upstreamProxyResolver is implemented by managers that read a proxy setting of
// their own ahead of HTTPS_PROXY and HTTP_PROXY. For any other manager, the
// inspection proxy uses glab's HTTPS_PROXY, HTTP_PROXY and NO_PROXY.
type upstreamProxyResolver interface {
	// resolveUpstreamProxy reports the proxy the manager would use for HTTPS
	// requests given environ, or false when it has none beyond HTTPS_PROXY.
	resolveUpstreamProxy(environ []string) (upstreamProxy, bool)
	// proxyVariables lists the manager's own proxy variables that its
	// resolvers read, for help text.
	proxyVariables() []string
}

// plainHTTPProxyResolver is implemented by managers that pick a different
// proxy for plain-HTTP registries than for HTTPS ones. Other resolvers' HTTPS
// proxy carries plain-HTTP requests too, as npm, pnpm, pip and cargo do.
type plainHTTPProxyResolver interface {
	// resolvePlainHTTPProxy reports the proxy the manager would use for
	// plain-HTTP requests given environ, or false when it has none beyond
	// HTTP_PROXY.
	resolvePlainHTTPProxy(environ []string) (upstreamProxy, bool)
}

// OwnProxyVariables lists the proxy variables of manager's own that glab reads
// ahead of HTTPS_PROXY and HTTP_PROXY, or nil when it reads none.
func OwnProxyVariables(manager PackageManager) []string {
	if r, ok := manager.(upstreamProxyResolver); ok {
		return r.proxyVariables()
	}
	return nil
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
func upstreamProxyOptions(manager PackageManager, environ []string) ([]proxy.Option, error) {
	resolver, ok := manager.(upstreamProxyResolver)
	if !ok {
		return nil, nil
	}
	httpsProxy, httpsOK := resolver.resolveUpstreamProxy(environ)
	// npm and pnpm send plain HTTP through the same proxy, even a bare
	// HTTPS_PROXY ahead of HTTP_PROXY (verified against npm 10.9, pnpm 10.12).
	plainProxy, plainOK := httpsProxy, httpsOK
	if r, ok := manager.(plainHTTPProxyResolver); ok {
		plainProxy, plainOK = r.resolvePlainHTTPProxy(environ)
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
		dbg.Debugf("dependency firewall: reaching %s registries through the outbound proxy from %s", s.scheme, s.upstream.Source)
		opts = append(opts, proxy.WithUpstreamProxyFor(s.scheme, proxyFunc))
	}
	return opts, nil
}

// proxyFunc returns a proxy selector for the inspection proxy's upstream
// transport. It validates the URL up front so a typo fails the run with a clear
// message, instead of every registry request failing later.
func (u upstreamProxy) proxyFunc() (func(*http.Request) (*url.URL, error), error) {
	if err := validateProxyURL(u.URL); err != nil {
		return nil, err
	}
	pick := (&httpproxy.Config{HTTPProxy: u.URL, HTTPSProxy: u.URL, NoProxy: u.NoProxy}).ProxyFunc()
	return func(r *http.Request) (*url.URL, error) { return pick(r.URL) }, nil
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
		return fmt.Errorf("unsupported proxy scheme %q", u.Scheme)
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

// npmConfigEnv is firstSetEnv for npm_config_* keys. npm parses their values
// like .npmrc entries, so "false", "null" and "undefined" leave the setting
// unset and npm moves on to the next one: npm_config_proxy=false falls back to
// HTTPS_PROXY.
func npmConfigEnv(environ []string, keys ...string) (string, string, bool) {
	for _, key := range keys {
		k, v, ok := firstSetEnv(environ, true, key)
		if ok && !slices.Contains([]string{"false", "null", "undefined"}, v) {
			return k, v, true
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

// resolveUpstreamProxy for npm and pnpm. For HTTPS requests both use
// https-proxy, then proxy, then HTTPS_PROXY, and skip the proxy for a host in
// either noproxy or NO_PROXY (verified against npm 10.9 and pnpm 10.12). Only
// the env-var form of each setting is read here; .npmrc files are not.
func (m npmManager) resolveUpstreamProxy(environ []string) (upstreamProxy, bool) {
	key, value, ok := npmConfigEnv(environ, "npm_config_https_proxy", "npm_config_proxy")
	if !ok {
		key, value, ok = firstSetEnv(environ, true, "HTTPS_PROXY", "https_proxy")
	}
	if !ok {
		return upstreamProxy{}, false
	}
	_, noproxy, _ := npmConfigEnv(environ, "npm_config_noproxy")
	return upstreamProxy{Source: key, URL: value, NoProxy: joinNoProxy(noproxy, envNoProxy(environ))}, true
}

func (yarnManager) proxyVariables() []string {
	return []string{"YARN_HTTPS_PROXY", "npm_config_https_proxy", "npm_config_proxy"}
}

// resolveUpstreamProxy for Yarn. Both lines read YARN_HTTPS_PROXY in any case.
// Berry uses only YARN_HTTPS_PROXY for HTTPS requests; Classic uses
// YARN_HTTPS_PROXY, then npm_config_https_proxy, then npm_config_proxy.
// Neither applies NO_PROXY to these settings (verified against Yarn 4.9 and
// 1.22). When none is set, Classic falls back to HTTPS_PROXY, which the
// inspection proxy uses by default.
func (yarnManager) resolveUpstreamProxy(environ []string) (upstreamProxy, bool) {
	if key, value, ok := firstSetEnv(environ, true, "YARN_HTTPS_PROXY"); ok {
		return upstreamProxy{Source: key, URL: value}, true
	}
	if key, value, ok := npmConfigEnv(environ, "npm_config_https_proxy", "npm_config_proxy"); ok {
		return upstreamProxy{Source: key, URL: value}, true
	}
	return upstreamProxy{}, false
}

// resolvePlainHTTPProxy for Yarn. For plain-HTTP requests Classic uses
// npm_config_proxy, then HTTP_PROXY (verified against Yarn 1.22). Berry sends
// plain-HTTP requests through a CONNECT tunnel, so they take the HTTPS path.
func (yarnManager) resolvePlainHTTPProxy(environ []string) (upstreamProxy, bool) {
	key, value, ok := npmConfigEnv(environ, "npm_config_proxy")
	if !ok {
		return upstreamProxy{}, false
	}
	return upstreamProxy{Source: key, URL: value}, true
}

func (pyPIManager) proxyVariables() []string { return []string{"PIP_PROXY"} }

// resolveUpstreamProxy for pip and pipenv. A PIP_PROXY makes pip ignore
// HTTPS_PROXY and NO_PROXY entirely (verified against pip 24.3).
func (m pyPIManager) resolveUpstreamProxy(environ []string) (upstreamProxy, bool) {
	key, value, ok := firstSetEnv(environ, false, "PIP_PROXY")
	if !ok {
		return upstreamProxy{}, false
	}
	return upstreamProxy{Source: key, URL: value}, true
}

func (cargoManager) proxyVariables() []string { return []string{"CARGO_HTTP_PROXY"} }

// resolveUpstreamProxy for cargo. CARGO_HTTP_PROXY outranks HTTPS_PROXY, and
// libcurl still skips it for a host in NO_PROXY or no_proxy (verified against
// cargo 1.99).
func (cargoManager) resolveUpstreamProxy(environ []string) (upstreamProxy, bool) {
	key, value, ok := firstSetEnv(environ, false, "CARGO_HTTP_PROXY")
	if !ok {
		return upstreamProxy{}, false
	}
	return upstreamProxy{Source: key, URL: value, NoProxy: envNoProxy(environ)}, true
}
