//go:build !integration

package pm

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testAuthority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newTestAuthority(t *testing.T) testAuthority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "glab test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return testAuthority{cert: cert, key: key}
}

func (a testAuthority) pem() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: a.cert.Raw})
}

func (a testAuthority) leaf(t *testing.T, host string) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, &key.PublicKey, a.key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return cert
}

func (a testAuthority) serverCert(t *testing.T, ip net.IP) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		IPAddresses:  []net.IP{ip},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, &key.PublicKey, a.key)
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func writeTestFile(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, content, 0o600))
	return path
}

func assertVerifies(t *testing.T, pool *x509.CertPool, leaf *x509.Certificate) {
	t.Helper()
	_, err := leaf.Verify(x509.VerifyOptions{DNSName: leaf.DNSNames[0], Roots: pool})
	assert.NoError(t, err)
}

func TestUpstreamRootCAsNoneConfigured(t *testing.T) {
	t.Parallel()
	pool, err := upstreamRootCAs(nil, "")
	require.NoError(t, err)
	assert.Nil(t, pool)
}

func TestUpstreamRootCAsEmptyVariableIsUnset(t *testing.T) {
	t.Parallel()
	pool, err := upstreamRootCAs([]string{"REQUESTS_CA_BUNDLE="}, "")
	require.NoError(t, err)
	assert.Nil(t, pool)
}

func TestUpstreamRootCAsTrustsEveryCertInBundle(t *testing.T) {
	t.Parallel()
	a, b := newTestAuthority(t), newTestAuthority(t)
	bundle := writeTestFile(t, "bundle.pem", append(a.pem(), b.pem()...))

	pool, err := upstreamRootCAs([]string{"REQUESTS_CA_BUNDLE=" + bundle}, "")
	require.NoError(t, err)
	require.NotNil(t, pool)
	assertVerifies(t, pool, a.leaf(t, "a.registry.test"))
	assertVerifies(t, pool, b.leaf(t, "b.registry.test"))
}

func TestUpstreamRootCAsTrustsCACertPath(t *testing.T) {
	t.Parallel()
	a := newTestAuthority(t)
	caCert := writeTestFile(t, "ca.pem", a.pem())

	pool, err := upstreamRootCAs(nil, caCert)
	require.NoError(t, err)
	require.NotNil(t, pool)
	assertVerifies(t, pool, a.leaf(t, "registry.test"))
}

func TestUpstreamRootCAsSkipsUnusableFiles(t *testing.T) {
	t.Parallel()
	a := newTestAuthority(t)
	env := []string{
		"SSL_CERT_FILE=" + filepath.Join(t.TempDir(), "missing.pem"),
		"PIP_CERT=" + writeTestFile(t, "garbage.pem", []byte("not a certificate")),
		"NODE_EXTRA_CA_CERTS=" + writeTestFile(t, "ca.pem", a.pem()),
	}

	pool, err := upstreamRootCAs(env, "")
	require.NoError(t, err)
	require.NotNil(t, pool)
	assertVerifies(t, pool, a.leaf(t, "registry.test"))
}

func TestUpstreamRootCAsOnlyUnusableFiles(t *testing.T) {
	t.Parallel()
	env := []string{"SSL_CERT_FILE=" + filepath.Join(t.TempDir(), "missing.pem")}
	caCert := writeTestFile(t, "garbage.pem", []byte("not a certificate"))

	pool, err := upstreamRootCAs(env, caCert)
	require.NoError(t, err)
	assert.Nil(t, pool)
}
