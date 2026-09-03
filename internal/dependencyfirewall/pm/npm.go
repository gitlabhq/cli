package pm

import "gitlab.com/gitlab-org/cli/internal/dependencyfirewall/proxy"

type npmManager struct {
	name   string
	binary string
}

// NPM returns the npm package manager.
func NPM() PackageManager { return npmManager{name: "npm", binary: "npm"} }

// Pnpm returns the pnpm package manager. pnpm honors HTTPS_PROXY and
// NODE_EXTRA_CA_CERTS the same as npm, so it differs only in name/binary.
func Pnpm() PackageManager { return npmManager{name: "pnpm", binary: "pnpm"} }

func (m npmManager) Name() string   { return m.name }
func (m npmManager) Binary() string { return m.binary }

func (m npmManager) Environment(proxyURL string) []string {
	// npm resolves each config key independently, with npm_config_* env vars
	// ranking above every .npmrc file. Pinning only https-proxy leaves the
	// rest of the routing family open: an inherited npm_config_noproxy or a
	// repo-committed ".npmrc" "noproxy=" line would route fetches straight
	// past the MITM proxy — no coordinates matched, no verdicts, a green job.
	// Pin the whole family from the env tier so every routing key resolves to
	// the inspection proxy.
	//
	// NPM_CONFIG_NOPROXY is deliberately set to "localhost" rather than empty:
	// npm treats an empty env value as unset and lets a project .npmrc win
	// (verified against npm 10: an empty value shows "overridden by project"),
	// which would reopen the bypass. A non-empty value wins over .npmrc, and
	// "localhost" never matches a real registry host, so the registry is still
	// proxied while any committed noproxy= line is neutralized.
	return []string{
		"HTTPS_PROXY=" + proxyURL,
		"NPM_CONFIG_PROXY=" + proxyURL,
		"NPM_CONFIG_HTTPS_PROXY=" + proxyURL,
		"NPM_CONFIG_NOPROXY=localhost",
	}
}

func (m npmManager) CATrustEnviron(caPath string) []string {
	return []string{"NODE_EXTRA_CA_CERTS=" + caPath}
}

func (m npmManager) ExistingBundleVar() string { return "NODE_EXTRA_CA_CERTS" }

func (m npmManager) CleanupCAFiles(string) {}

func (m npmManager) Matcher() proxy.Matcher { return proxy.NPMMatcher{} }
