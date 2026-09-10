//go:build !integration

package pm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gradleOpts runs Environment then CATrustEnviron and returns the resulting
// GRADLE_OPTS value (or "" if none was produced).
func gradleOpts(t *testing.T, proxyURL, caPath string) (string, bool) {
	t.Helper()
	m := Gradle()
	m.Environment(proxyURL)
	for _, e := range m.CATrustEnviron(caPath) {
		if v, ok := strings.CutPrefix(e, "GRADLE_OPTS="); ok {
			return v, true
		}
	}
	return "", false
}

// TestGradleCATrustRoutesThroughProxy pins the security-critical routing path.
// Gradle honors the JVM proxy system properties, so GRADLE_OPTS must carry the
// -Dhttp(s).proxyHost/proxyPort that route gradle through the inspection proxy
// as well as the truststore that makes it trust the proxy CA. A regression that
// dropped the proxy args would fail open, so assert the exact wiring.
func TestGradleCATrustRoutesThroughProxy(t *testing.T) {
	ca := selfSignedTestCA(t)
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caPath, caPEM(t, ca), 0o600))

	opts, ok := gradleOpts(t, "http://127.0.0.1:52999", caPath)
	require.True(t, ok, "GRADLE_OPTS must be produced when the proxy resolves")

	assert.Contains(t, opts, "-Dhttp.proxyHost=127.0.0.1")
	assert.Contains(t, opts, "-Dhttp.proxyPort=52999")
	assert.Contains(t, opts, "-Dhttps.proxyHost=127.0.0.1")
	assert.Contains(t, opts, "-Dhttps.proxyPort=52999")
	assert.Contains(t, opts, "-Djavax.net.ssl.trustStore=",
		"the truststore must be trusted alongside the proxy routing")
}

// TestGradleCATrustFailsClosedWithoutProxy guards against the fail-open bug:
// if the proxy URL is missing or unparsable, CATrustEnviron must not emit a
// truststore-only GRADLE_OPTS (which would make gradle trust the proxy CA while
// connecting directly, silently bypassing the firewall). It must return nothing.
func TestGradleCATrustFailsClosedWithoutProxy(t *testing.T) {
	ca := selfSignedTestCA(t)
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caPath, caPEM(t, ca), 0o600))

	// Environment never called: proxyURL is empty.
	m := Gradle()
	assert.Empty(t, m.CATrustEnviron(caPath),
		"no proxy means no GRADLE_OPTS at all, not truststore-only (fail-closed)")

	// Unparsable / hostless proxy URLs must also fail closed.
	for _, bad := range []string{"", "://nope", "http://", "not a url"} {
		_, ok := gradleOpts(t, bad, caPath)
		assert.Falsef(t, ok, "proxy %q must not yield GRADLE_OPTS", bad)
	}
}
