//go:build !integration

package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Not parallel: it clears PATH and the JVM option variables.
func TestJVMTrustEnvRoutesMavenAndGradle(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, k := range []string{"MAVEN_ARGS", "MAVEN_OPTS", "GRADLE_OPTS", "JAVA_TOOL_OPTIONS", "JDK_JAVA_OPTIONS"} {
		t.Setenv(k, "")
	}
	dir := t.TempDir()
	caPath := filepath.Join(dir, "proxy-ca.pem")
	require.NoError(t, os.WriteFile(caPath, testCAPEM(t), 0o644))

	env, err := JVMTrustEnv(dir, "http://127.0.0.1:9999", caPath, func(string, ...any) {})
	require.NoError(t, err)
	m := envMap(t, env)

	// Exactly one -gs: Maven takes the first, so a second would be ignored.
	args := strings.Fields(m["MAVEN_ARGS"])
	require.Equal(t, []string{"-gs", filepath.Join(dir, "maven-ca.pem.settings.xml")}, args)
	settings, err := os.ReadFile(args[1])
	require.NoError(t, err)
	assert.Contains(t, string(settings), "<host>127.0.0.1</host>")
	assert.Contains(t, m["MAVEN_OPTS"], "javax.net.ssl.trustStore=")

	assert.Contains(t, m["GRADLE_OPTS"], "-Dhttps.proxyHost=127.0.0.1")
	assert.Contains(t, m["GRADLE_OPTS"], "javax.net.ssl.trustStore=")
	// JAVA_TOOL_OPTIONS would apply to every JVM in the job, not just Gradle.
	assert.NotContains(t, m, "JAVA_TOOL_OPTIONS")
	assert.NotContains(t, m, "JDK_JAVA_OPTIONS")
}

func TestJVMTrustEnvReportsAnEcosystemItCannotCover(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	caPath := filepath.Join(dir, "proxy-ca.pem")
	require.NoError(t, os.WriteFile(caPath, testCAPEM(t), 0o644))
	// A directory where Maven's CA copy goes makes that write fail.
	require.NoError(t, os.Mkdir(filepath.Join(dir, "maven-ca.pem"), 0o755))

	env, err := JVMTrustEnv(dir, "http://127.0.0.1:9999", caPath, func(string, ...any) {})
	require.ErrorContains(t, err, "maven")
	m := envMap(t, env)
	assert.NotContains(t, m, "MAVEN_ARGS")
	assert.NotEmpty(t, m["GRADLE_OPTS"], "gradle is still covered")
}
