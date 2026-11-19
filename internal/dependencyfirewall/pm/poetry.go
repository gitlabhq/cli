package pm

import "gitlab.com/gitlab-org/cli/internal/dependencyfirewall/proxy"

// poetryManager runs poetry through the proxy. poetry reads REQUESTS_CA_BUNDLE
// for CA trust and HTTPS_PROXY for routing, plus PIP_PROXY and PIP_CERT so its
// legacy installer — which can shell out to pip — routes through and trusts
// the proxy too; its sources are left untouched.
type poetryManager struct{}

// Poetry returns the poetry package manager.
func Poetry() PackageManager { return poetryManager{} }

func (poetryManager) Name() string   { return "poetry" }
func (poetryManager) Binary() string { return "poetry" }

// Environment pins PIP_PROXY in addition to the engine's universal HTTPS_PROXY.
// poetry's own resolver honors HTTPS_PROXY, but its legacy installer can shell
// out to pip, which sets session.trust_env = False (ignoring the proxy env
// vars) whenever it sees a PIP_PROXY env var or a "proxy =" pip.conf line.
// Pinning PIP_PROXY keeps that pip fallback routed through the MITM instead of
// silently failing open on a green job, matching the pip manager.
func (poetryManager) Environment(proxyURL string) []string {
	return []string{"PIP_PROXY=" + proxyURL}
}

func (poetryManager) CATrustEnviron(caPath string) []string {
	return []string{
		"PIP_CERT=" + caPath,
		"REQUESTS_CA_BUNDLE=" + caPath,
	}
}

func (poetryManager) ExistingBundleVars() []string { return []string{"PIP_CERT", "REQUESTS_CA_BUNDLE"} }

func (poetryManager) CleanupCAFiles(string) {}

func (poetryManager) Matcher() proxy.Matcher { return proxy.PyPIMatcher{} }
