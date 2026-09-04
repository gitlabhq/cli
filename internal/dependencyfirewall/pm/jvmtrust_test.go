//go:build !integration

package pm

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"software.sslmate.com/src/go-pkcs12"
)

// selfSignedTestCA builds a throwaway self-signed CA certificate for tests.
func selfSignedTestCA(t *testing.T) *x509.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "glab-df-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return cert
}

// caPEM PEM-encodes a certificate for tests that need the on-disk form.
func caPEM(t *testing.T, ca *x509.Certificate) []byte {
	t.Helper()
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw})
}

func TestWriteJVMTrustStoreAt(t *testing.T) {
	t.Parallel()
	ca := selfSignedTestCA(t)
	path := filepath.Join(t.TempDir(), "ts.p12")

	password, err := writeJVMTrustStoreAt(path, ca)
	require.NoError(t, err)
	assert.NotEmpty(t, password)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	certs, err := pkcs12.DecodeTrustStore(raw, password)
	require.NoError(t, err)
	// The proxy CA must be present among the trusted certs.
	found := false
	for _, c := range certs {
		if c.Equal(ca) {
			found = true
		}
	}
	assert.True(t, found, "proxy CA should be in the truststore")
}

func TestJVMTrustArgs(t *testing.T) {
	t.Parallel()
	args := jvmTrustArgs("/tmp/ts.p12", "secret")
	joined := strings.Join(args, " ")
	assert.Contains(t, joined, "-Djavax.net.ssl.trustStore=/tmp/ts.p12")
	assert.Contains(t, joined, "-Djavax.net.ssl.trustStorePassword=secret")
	assert.Contains(t, joined, "-Djavax.net.ssl.trustStoreType=PKCS12")
}

func TestJVMTrustOpts(t *testing.T) {
	t.Parallel()
	ca := selfSignedTestCA(t)
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caFile, caPEM(t, ca), 0o600))

	opts := jvmTrustOpts(caFile, "MAVEN_OPTS")
	require.NotEmpty(t, opts)
	assert.Contains(t, opts, "-Djavax.net.ssl.trustStore="+caFile+".p12")
	assert.Contains(t, opts, "-Djavax.net.ssl.trustStoreType=PKCS12")

	// The generated PKCS#12 store must actually trust the CA.
	tsPath := caFile + ".p12"
	raw, err := os.ReadFile(tsPath)
	require.NoError(t, err)
	certs, err := pkcs12.DecodeTrustStore(raw, jvmTrustStorePassword)
	require.NoError(t, err)
	found := false
	for _, c := range certs {
		if c.Equal(ca) {
			found = true
		}
	}
	assert.True(t, found, "CA from the bundle should be in the JVM truststore")
}

func TestJVMTrustOptsPrependsExisting(t *testing.T) {
	ca := selfSignedTestCA(t)
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caFile, caPEM(t, ca), 0o600))

	t.Setenv("MAVEN_OPTS", "-Xmx512m")
	opts := jvmTrustOpts(caFile, "MAVEN_OPTS")
	assert.True(t, strings.HasPrefix(opts, "-Xmx512m "),
		"existing MAVEN_OPTS must be preserved ahead of the truststore flags")
}

func TestJVMTrustOptsEmptyOnNoCerts(t *testing.T) {
	t.Parallel()
	caFile := filepath.Join(t.TempDir(), "empty.pem")
	require.NoError(t, os.WriteFile(caFile, []byte("not a certificate"), 0o600))

	assert.Empty(t, jvmTrustOpts(caFile, "MAVEN_OPTS"),
		"a bundle with no parsable certificates yields no JVM flags")
}

// TestSystemRootsReturnsHostRoots guards the cross-platform sourcing: on a
// normally-configured host the JVM truststore must carry the public roots in
// addition to the proxy CA, otherwise -Djavax.net.ssl.trustStore (which
// replaces the default store) breaks every non-intercepted TLS connection.
func TestSystemRootsReturnsHostRoots(t *testing.T) {
	t.Parallel()
	roots := systemRoots()
	if len(roots) == 0 {
		t.Skipf("no system roots available on %s test host; sourcing exercised elsewhere", runtime.GOOS)
	}
	for _, c := range roots {
		require.NotNil(t, c)
	}
}

// TestEncodeJVMTrustStoreIncludesSystemRoots verifies the encoded truststore
// carries both the proxy CA and the host system roots, so the JVM keeps
// validating public dependencies alongside proxy-intercepted traffic.
func TestEncodeJVMTrustStoreIncludesSystemRoots(t *testing.T) {
	t.Parallel()
	sysRoots := systemRoots()
	if len(sysRoots) == 0 {
		t.Skipf("no system roots available on %s test host", runtime.GOOS)
	}
	proxyCA := selfSignedTestCA(t)

	path := filepath.Join(t.TempDir(), "ts.p12")
	_, err := writeJVMTrustStoreAt(path, proxyCA)
	require.NoError(t, err)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	certs, err := pkcs12.DecodeTrustStore(raw, jvmTrustStorePassword)
	require.NoError(t, err)

	foundProxy := false
	for _, c := range certs {
		if c.Equal(proxyCA) {
			foundProxy = true
		}
	}
	assert.True(t, foundProxy, "proxy CA must be trusted")
	assert.GreaterOrEqual(t, len(certs), len(sysRoots)+1,
		"truststore must contain the system roots plus the proxy CA")
}
