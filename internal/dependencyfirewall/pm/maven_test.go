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

// TestMavenCATrustRoutesThroughProxy pins the security-critical routing path:
// Maven ignores HTTP(S)_PROXY and the JVM proxy properties, so the generated
// settings.xml passed via MAVEN_ARGS (-s) is the only thing sending mvn
// traffic through the inspection proxy. A regression that dropped it would
// silently fail open, so assert the exact wiring.
func TestMavenCATrustRoutesThroughProxy(t *testing.T) {
	ca := selfSignedTestCA(t)
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caPath, caPEM(t, ca), 0o600))

	m := Maven()
	m.Environment("http://127.0.0.1:52999")
	env := m.CATrustEnviron(caPath)

	var mavenArgs string
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "MAVEN_ARGS="); ok {
			mavenArgs = v
		}
	}
	require.NotEmpty(t, mavenArgs, "MAVEN_ARGS must carry the -s settings.xml that routes mvn through the proxy")

	settingsPath := caPath + ".settings.xml"
	assert.Equal(t, "-s "+settingsPath, mavenArgs)

	raw, err := os.ReadFile(settingsPath)
	require.NoError(t, err, "the proxy settings.xml must be written")
	settings := string(raw)
	assert.Contains(t, settings, "<host>127.0.0.1</host>")
	assert.Contains(t, settings, "<port>52999</port>")
}

// TestMavenCATrustFailOpenIsSilentButOmitsArgs guards the fail path: when the
// proxy URL is missing or unparsable, writeMavenProxySettings fails and no
// MAVEN_ARGS -s entry is added (the failure is logged via dbg, exercised by
// the debug build). The truststore MAVEN_OPTS is still emitted.
func TestMavenCATrustFailOpenOmitsProxyArgs(t *testing.T) {
	ca := selfSignedTestCA(t)
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caPath, caPEM(t, ca), 0o600))

	m := Maven() // Environment never called, so proxyURL is empty.
	env := m.CATrustEnviron(caPath)

	for _, e := range env {
		assert.False(t, strings.HasPrefix(e, "MAVEN_ARGS="),
			"no MAVEN_ARGS -s entry when the proxy settings.xml could not be written")
	}
}
