package pm

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/proxy"
)

//go:embed yarnplugin.cjs
var yarnFirewallPlugin []byte

type yarnManager struct{}

// Yarn returns the Yarn (Berry) package manager. It routes through the proxy
// via YARN_HTTP(S)_PROXY and trusts the proxy CA via NODE_EXTRA_CA_CERTS.
//
// Berry's per-host networkSettings override the global proxy and cannot be
// pinned from the environment, so Setup also injects a plugin
// (yarnplugin.cjs) through YARN_PLUGINS that empties them before each request.
func Yarn() PackageManager { return yarnManager{} }

func (yarnManager) Name() string   { return "yarn" }
func (yarnManager) Binary() string { return "yarn" }

func (yarnManager) Environment(proxyURL string) []string {
	// Yarn's two major lines read proxy configuration from different places, so
	// both routing families are pinned from the environment tier to keep either
	// from failing open:
	//
	//   - Berry (v2+) ignores the standard HTTP(S)_PROXY variables and honors
	//     only its own YARN_HTTP(S)_PROXY (the httpProxy/httpsProxy .yarnrc.yml
	//     keys). Without these Berry bypasses the inspection proxy entirely.
	//   - Classic (v1) reads .npmrc and, for npm backward compatibility, the
	//     npm_config_* environment variables (verified against Yarn 1.22).
	//     Classic 1.22 has no "noproxy" config key of its own — its only
	//     no-proxy handling is the NO_PROXY/no_proxy env vars, which the engine
	//     already strips — so the npm_config family is pinned mainly to keep a
	//     committed ".npmrc" proxy/https-proxy line from winning. The
	//     npm_config_noproxy pin (non-empty "localhost" sentinel, since an
	//     empty value is treated as unset) is harmless over-coverage that
	//     matches the npm manager rather than a Classic-specific hole.
	//
	// HTTPS_PROXY (uppercase) is already set by the engine's proxyEnviron; it is
	// not duplicated here.
	return []string{
		"YARN_HTTPS_PROXY=" + proxyURL,
		"YARN_HTTP_PROXY=" + proxyURL,
		"npm_config_proxy=" + proxyURL,
		"npm_config_https_proxy=" + proxyURL,
		"npm_config_noproxy=localhost",
	}
}

// Setup writes the firewall plugin to a temp file and lists it in
// YARN_PLUGINS ahead of any plugins the user already loads there.
func (yarnManager) Setup(_ context.Context, in setupInput) (setupResult, error) {
	f, err := os.CreateTemp("", "glab-df-yarn-plugin-*.cjs")
	if err != nil {
		return setupResult{}, fmt.Errorf("failed to create the yarn firewall plugin: %w", err)
	}
	_, err = f.Write(yarnFirewallPlugin)
	if err = errors.Join(err, f.Close()); err != nil {
		return setupResult{}, fmt.Errorf("failed to write the yarn firewall plugin: %w", errors.Join(err, os.Remove(f.Name())))
	}

	// Berry reads yarn_* variables in any case, letting the last one win, and
	// Run strips every case of YARN_PLUGINS from the child's environment.
	var existing string
	for _, e := range in.parent {
		if k, v, ok := strings.Cut(e, "="); ok && strings.EqualFold(k, "YARN_PLUGINS") {
			existing = v
		}
	}
	plugins := f.Name()
	if existing != "" {
		plugins += ";" + existing
	}
	return setupResult{
		env:  []string{"YARN_PLUGINS=" + plugins},
		stop: func() { _ = os.Remove(f.Name()) },
	}, nil
}

func (yarnManager) CATrustEnviron(caPath string) []string {
	return []string{"NODE_EXTRA_CA_CERTS=" + caPath}
}

func (yarnManager) ExistingBundleVars() []string { return []string{"NODE_EXTRA_CA_CERTS"} }

func (yarnManager) CleanupCAFiles(string) {}

func (yarnManager) Matcher() proxy.Matcher { return proxy.NPMMatcher{} }
