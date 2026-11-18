package pm

import "gitlab.com/gitlab-org/cli/internal/dependencyfirewall/proxy"

// pyPIManager runs a pip-family tool through the proxy. Both pip and pipenv
// route through PIP_PROXY, PIP_CERT and REQUESTS_CA_BUNDLE and differ only in
// name/binary; pipenv delegates to pip, which reads the same variables. The
// user's real pip.conf/Pipfile and index URL are left untouched.
type pyPIManager struct {
	name   string
	binary string
}

// Pip returns the pip package manager.
func Pip() PackageManager { return pyPIManager{name: "pip", binary: "pip"} }

// Pipenv returns the pipenv package manager.
func Pipenv() PackageManager { return pyPIManager{name: "pipenv", binary: "pipenv"} }

func (m pyPIManager) Name() string   { return m.name }
func (m pyPIManager) Binary() string { return m.binary }

// Environment pins PIP_PROXY, not just the engine's universal HTTPS_PROXY.
// pip does not merely follow HTTPS_PROXY: when it has a proxy of its own — a
// PIP_PROXY env var or a "proxy =" line in any pip.conf/PIP_CONFIG_FILE — pip
// sets session.trust_env = False and ignores the proxy env vars entirely
// (pip's index_command: "if options.proxy: session.proxies = {...};
// session.trust_env = False"). A CI image exporting PIP_PROXY or shipping a
// pip.conf would then route every fetch around the MITM on a green job — the
// same env-tier fail-open class the npm/yarn managers pin against. Setting
// PIP_PROXY here wins over pip.conf (pip's env layer beats its config files),
// the value is non-empty (no empty-as-unset hazard), and CA trust survives
// trust_env=False because PIP_CERT is applied session-level, not env-gated.
func (m pyPIManager) Environment(proxyURL string) []string {
	return []string{"PIP_PROXY=" + proxyURL}
}

// rewriteArgs takes pip's --proxy option out of args, because it outranks
// PIP_PROXY and would send pip's traffic around the inspection proxy.
// resolveUpstreamProxy makes its value the inspection proxy's upstream.
func (m pyPIManager) rewriteArgs(args []string) []string {
	if m.binary != "pip" {
		return args
	}
	_, rest, _ := pipProxyArg(args)
	return rest
}

func (m pyPIManager) CATrustEnviron(caPath string) []string {
	return []string{
		"PIP_CERT=" + caPath,
		"REQUESTS_CA_BUNDLE=" + caPath,
	}
}

// ExistingBundleVars lists both CA vars CATrustEnviron overwrites so a user
// who set only REQUESTS_CA_BUNDLE (common behind a corporate TLS-terminating
// proxy) keeps their anchors, not just one who set PIP_CERT.
func (m pyPIManager) ExistingBundleVars() []string { return []string{"PIP_CERT", "REQUESTS_CA_BUNDLE"} }

func (m pyPIManager) CleanupCAFiles(string) {}

func (m pyPIManager) Matcher() proxy.Matcher { return proxy.PyPIMatcher{} }
