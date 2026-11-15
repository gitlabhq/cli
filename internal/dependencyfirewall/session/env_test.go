//go:build !integration

package session

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func envMap(t *testing.T, env []string) map[string]string {
	t.Helper()
	m := map[string]string{}
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		require.True(t, ok, "env entry without =: %q", e)
		m[k] = v
	}
	return m
}

func TestBuildJobEnvRoutesAndTrustsAdditively(t *testing.T) {
	t.Parallel()
	m := envMap(t, BuildJobEnv("http://127.0.0.1:9999", "/state/ca-bundle.pem", "corp.internal"))

	// Plain HTTP is routed too, so a plain-HTTP registry is checked as well.
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		assert.Equal(t, "http://127.0.0.1:9999", m[k], k)
	}
	// npm does not just follow HTTPS_PROXY, so its own routing variable is set.
	assert.Equal(t, "http://127.0.0.1:9999", m["NPM_CONFIG_PROXY"])

	// Every trust variable points at the merged bundle, which keeps the caller's
	// own anchors, including a NODE_EXTRA_CA_CERTS or CURL_CA_BUNDLE it replaces.
	for _, k := range []string{"NODE_EXTRA_CA_CERTS", "CURL_CA_BUNDLE", "REQUESTS_CA_BUNDLE", "PIP_CERT", "CARGO_HTTP_CAINFO", "GIT_SSL_CAINFO", "SSL_CERT_FILE", "BUNDLE_SSL_CA_CERT"} {
		assert.Equal(t, "/state/ca-bundle.pem", m[k], k)
	}

	for _, k := range []string{"NO_PROXY", "no_proxy"} {
		for _, host := range []string{"corp.internal", "localhost", "127.0.0.1", "::1"} {
			assert.Contains(t, strings.Split(m[k], ","), host, k)
		}
	}
}

func TestMergeNoProxyAddsLoopbackAndDedups(t *testing.T) {
	t.Parallel()
	// The caller's NO_PROXY and no_proxy arrive joined, so either can be empty
	// and both can repeat a host.
	got := strings.Split(mergeNoProxy("corp.internal, localhost,,corp.internal,svc.local"), ",")
	assert.Equal(t, []string{"corp.internal", "localhost", "svc.local", "127.0.0.1", "::1"}, got)
}

func TestWriteMergedBundleIncludesProxyCAAndCallerBundles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	caPath := filepath.Join(dir, "proxy-ca.pem")
	caPEM := testCAPEM(t)
	require.NoError(t, os.WriteFile(caPath, caPEM, 0o644))
	userBundle := filepath.Join(dir, "user.pem")
	userPEM := testCAPEM(t)
	require.NoError(t, os.WriteFile(userBundle, userPEM, 0o644))

	var logged []string
	logf := func(format string, a ...any) { logged = append(logged, fmt.Sprintf(format, a...)) }
	missing := filepath.Join(dir, "missing.pem")
	out, err := WriteMergedBundle(dir, caPath, []string{userBundle, userBundle, missing}, logf)
	require.NoError(t, err)

	merged, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Contains(t, string(merged), string(caPEM))
	assert.Equal(t, 1, bytes.Count(merged, userPEM), "a repeated caller bundle is added once")

	var missingLogged bool
	for _, l := range logged {
		assert.True(t, strings.HasSuffix(l, "\n"), "log line ends in a newline: %q", l)
		missingLogged = missingLogged || strings.Contains(l, missing)
	}
	assert.True(t, missingLogged, "an unreadable caller bundle is reported, got %q", logged)
}

func TestWriteMergedBundleFailsWithoutProxyCA(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := WriteMergedBundle(dir, filepath.Join(dir, "absent.pem"), nil, func(string, ...any) {})
	assert.Error(t, err)
}

func TestCallerBundlePaths(t *testing.T) {
	t.Parallel()
	got := CallerBundlePaths([]string{
		"SSL_CERT_FILE=/etc/corp.pem",
		"BUNDLE_SSL_CA_CERT=/etc/bundler.pem",
		"REQUESTS_CA_BUNDLE=",
		"PATH=/usr/bin",
	})
	assert.Equal(t, []string{"/etc/corp.pem", "/etc/bundler.pem"}, got)
}

func TestShellExportsQuotesValues(t *testing.T) {
	t.Parallel()
	out := ShellExports([]string{"A=simple", "B=with spaces", "C=with'quote"})
	assert.Equal(t, "export A='simple'\nexport B='with spaces'\nexport C='with'\\''quote'\n", out)
}

// testCAPEM returns a unique self-signed CA certificate in PEM, for bundle tests.
func testCAPEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestSystemTrustAnchorNameIsPerStateDir(t *testing.T) {
	t.Parallel()
	// The anchor name embeds the state dir's base name so two concurrent jobs on
	// one host stage distinct files and stop removes only its own run's CA.
	a := systemTrustAnchorName("/tmp/glab-df-job-proxy-aaa111")
	b := systemTrustAnchorName("/tmp/glab-df-job-proxy-bbb222")
	assert.NotEqual(t, a, b)
	assert.Equal(t, "glab-df-proxy-glab-df-job-proxy-aaa111.crt", a)
	assert.True(t, strings.HasSuffix(a, ".crt"))
}
