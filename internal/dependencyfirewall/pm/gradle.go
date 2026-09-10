package pm

import (
	"net/url"
	"os"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/proxy"
)

// gradleManager runs gradle through the proxy. Unlike Maven's resolver, the
// Gradle daemon honors the -Dhttp(s).proxyHost/proxyPort JVM system
// properties, so both routing and the MITM truststore are delivered via
// GRADLE_OPTS; the user's build files and ~/.gradle are left untouched.
type gradleManager struct {
	proxyURL string
}

// Gradle returns a fresh gradle package manager.
func Gradle() PackageManager { return &gradleManager{} }

func (m *gradleManager) Name() string   { return "gradle" }
func (m *gradleManager) Binary() string { return "gradle" }

func (m *gradleManager) Environment(proxyURL string) []string {
	m.proxyURL = proxyURL
	return nil
}

func (m *gradleManager) CATrustEnviron(caPath string) []string {
	// Proxy routing is a hard requirement: the truststore only makes gradle
	// trust the proxy CA, it does not route traffic. If routing cannot be
	// established, adding the truststore alone would make gradle trust the
	// proxy while connecting directly to the real repository — a silent
	// firewall bypass (fail-open). So resolve the proxy first and bail out
	// entirely if it fails, rather than emitting truststore-only GRADLE_OPTS.
	u, err := url.Parse(m.proxyURL)
	if err != nil || u.Hostname() == "" || u.Port() == "" {
		if err != nil {
			dbg.Debugf("dependency firewall: failed to parse proxy URL %q for gradle: %v", m.proxyURL, err)
		} else {
			dbg.Debugf("dependency firewall: no usable proxy host/port for gradle (%q); not routing gradle through the proxy", m.proxyURL)
		}
		return nil
	}

	args := []string{
		"-Dhttp.proxyHost=" + u.Hostname(),
		"-Dhttp.proxyPort=" + u.Port(),
		"-Dhttps.proxyHost=" + u.Hostname(),
		"-Dhttps.proxyPort=" + u.Port(),
	}

	if raw, err := os.ReadFile(caPath); err != nil {
		dbg.Debugf("dependency firewall: failed to read CA bundle %s for gradle truststore: %v", caPath, err)
	} else if cas := parseCertsPEM(raw); len(cas) == 0 {
		dbg.Debugf("dependency firewall: CA bundle %s has no parsable certificates; gradle will not trust the proxy", caPath)
	} else {
		// Write the truststore next to the CA bundle so Run, which already
		// removes caPath and its ".p12" sibling, cleans it up too.
		tsPath := caPath + ".p12"
		if password, tsErr := writeJVMTrustStoreAt(tsPath, cas...); tsErr != nil {
			dbg.Debugf("dependency firewall: failed to write gradle truststore %s: %v", tsPath, tsErr)
		} else {
			args = append(args, jvmTrustArgs(tsPath, password)...)
		}
	}

	opts := strings.Join(args, " ")
	if existing := os.Getenv("GRADLE_OPTS"); existing != "" {
		opts = existing + " " + opts
	}
	return []string{"GRADLE_OPTS=" + opts}
}

// ExistingBundleVars is empty: Gradle trusts the proxy through a generated JVM
// truststore (GRADLE_OPTS), not a user-provided CA-bundle environment
// variable, so there is nothing for the engine to prepend.
func (m *gradleManager) ExistingBundleVars() []string { return nil }

// CleanupCAFiles removes the PKCS#12 truststore this manager writes next to
// the CA bundle in CATrustEnviron.
func (m *gradleManager) CleanupCAFiles(caPath string) {
	_ = os.Remove(caPath + ".p12")
}

func (m *gradleManager) Matcher() proxy.Matcher { return proxy.MavenMatcher{} }
