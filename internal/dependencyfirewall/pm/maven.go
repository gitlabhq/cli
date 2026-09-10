package pm

import (
	"net/url"
	"os"

	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/proxy"
)

// mavenManager runs mvn through the proxy. Maven's resolver ignores the
// HTTP(S)_PROXY environment variables AND the -Dhttp(s).proxyHost JVM system
// properties, so the proxy must be delivered through a settings.xml <proxy>
// block passed via MAVEN_ARGS (-s). The MITM CA is trusted through a JVM
// truststore folded into MAVEN_OPTS. The user's own settings.xml is left
// untouched.
type mavenManager struct {
	proxyURL string
}

// Maven returns a fresh maven package manager (it stashes the proxy URL from
// Environment for CATrustEnviron to build the settings.xml and truststore).
func Maven() PackageManager { return &mavenManager{} }

func (m *mavenManager) Name() string   { return "maven" }
func (m *mavenManager) Binary() string { return "mvn" }

// Environment stashes the proxy URL; the settings.xml and truststore are
// emitted from CATrustEnviron (called last by Run, after the CA bundle
// exists) so both the routing and the trust anchor land together.
func (m *mavenManager) Environment(proxyURL string) []string {
	m.proxyURL = proxyURL
	return nil
}

func (m *mavenManager) CATrustEnviron(caPath string) []string {
	var env []string

	// Routing: a settings.xml <proxy> block. Written next to the CA bundle so
	// Run, which removes caPath and its siblings, cleans it up too. Maven
	// ignores HTTP(S)_PROXY and the JVM proxy properties, so this file is the
	// only thing routing mvn through the proxy: surface a failure via dbg so a
	// silent firewall bypass is diagnosable rather than fail-open unnoticed.
	settingsPath, ok := writeMavenProxySettings(caPath+".settings.xml", m.proxyURL)
	if !ok {
		dbg.Debugf("dependency firewall: failed to write maven proxy settings.xml at %s; maven traffic will not be routed through the proxy", caPath+".settings.xml")
	} else {
		env = append(env, mavenArgsEnv("-s "+settingsPath))
	}

	// CA trust: a PKCS#12 truststore folded into MAVEN_OPTS.
	if opts := jvmTrustOpts(caPath); opts != "" {
		env = append(env, "MAVEN_OPTS="+opts)
	}

	return env
}

// ExistingBundleVars is empty: Maven trusts the proxy through a generated JVM
// truststore (MAVEN_OPTS), not a user-provided CA-bundle environment variable,
// so there is nothing for the engine to prepend.
func (m *mavenManager) ExistingBundleVars() []string { return nil }

// CleanupCAFiles removes the settings.xml and PKCS#12 truststore this manager
// writes next to the CA bundle in CATrustEnviron.
func (m *mavenManager) CleanupCAFiles(caPath string) {
	_ = os.Remove(caPath + ".settings.xml")
	_ = os.Remove(caPath + ".p12")
}

func (m *mavenManager) Matcher() proxy.Matcher { return proxy.MavenMatcher{} }

// mavenArgsEnv appends extra to any inherited MAVEN_ARGS so a user's existing
// arguments are preserved.
func mavenArgsEnv(extra string) string {
	if existing := os.Getenv("MAVEN_ARGS"); existing != "" {
		return "MAVEN_ARGS=" + existing + " " + extra
	}
	return "MAVEN_ARGS=" + extra
}

// writeMavenProxySettings writes a minimal settings.xml whose only content is
// an active http+https <proxy> pointing at proxyURL, and returns its path. It
// returns ok=false when proxyURL cannot be parsed.
func writeMavenProxySettings(path, proxyURL string) (string, bool) {
	u, err := url.Parse(proxyURL)
	if err != nil || u.Hostname() == "" || u.Port() == "" {
		return "", false
	}
	settings := "<settings xmlns=\"http://maven.apache.org/SETTINGS/1.0.0\">\n" +
		"  <proxies>\n" +
		mavenProxyElem("df-https", "https", u.Hostname(), u.Port()) +
		mavenProxyElem("df-http", "http", u.Hostname(), u.Port()) +
		"  </proxies>\n" +
		"</settings>\n"
	if err := os.WriteFile(path, []byte(settings), 0o600); err != nil {
		return "", false
	}
	return path, true
}

func mavenProxyElem(id, protocol, host, port string) string {
	return "    <proxy>\n" +
		"      <id>" + id + "</id>\n" +
		"      <active>true</active>\n" +
		"      <protocol>" + protocol + "</protocol>\n" +
		"      <host>" + host + "</host>\n" +
		"      <port>" + port + "</port>\n" +
		"    </proxy>\n"
}
