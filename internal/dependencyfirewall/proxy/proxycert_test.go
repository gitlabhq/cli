//go:build !integration

package proxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const tarballPath = "/left-pad/-/left-pad-1.3.0.tgz"

// certRecordingRegistry requests, without requiring, a client certificate and
// counts the ones it receives. A client that holds any certificate offers it
// to such a server, so a zero count proves the certificate was withheld.
type certRecordingRegistry struct {
	server    *httptest.Server
	hits      atomic.Int64
	peerCerts atomic.Int64
}

func startCertRecordingRegistry(t *testing.T) *certRecordingRegistry {
	t.Helper()
	r := &certRecordingRegistry{}
	r.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.hits.Add(1)
		r.peerCerts.Add(int64(len(req.TLS.PeerCertificates)))
		_, _ = io.WriteString(w, "TARBALL")
	}))
	r.server.TLS = &tls.Config{ClientAuth: tls.RequestClientCert}
	r.server.StartTLS()
	t.Cleanup(r.server.Close)
	return r
}

// startMTLSCorporateProxy starts a TLS corporate proxy that requires basic
// auth and a client certificate from its own client CA. It returns the CA
// that signed the proxy's server certificate and a client certificate the
// proxy accepts.
func startMTLSCorporateProxy(t *testing.T) (*corporateProxy, *x509.Certificate, tls.Certificate) {
	t.Helper()
	clientCA, err := newCertAuthority()
	require.NoError(t, err)
	clientCAs := x509.NewCertPool()
	clientCAs.AddCert(clientCA.cert)
	corporate, corporateCA := startTLSCorporateProxy(t, "corp", "secret", clientCAs)

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	serial, err := randomSerial()
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "glab user"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, clientCA.cert, &key.PublicKey, clientCA.key)
	require.NoError(t, err)
	return corporate, corporateCA, tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func requireTarball(t *testing.T, p *Proxy, registry *certRecordingRegistry) {
	t.Helper()
	resp, err := newClient(t, p).Get(registry.server.URL + tarballPath)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "TARBALL", string(body))
}

func TestProxyPresentsClientCertificateOnlyToMTLSOutboundProxy(t *testing.T) {
	t.Parallel()
	registry := startCertRecordingRegistry(t)
	corporate, corporateCA, cert := startMTLSCorporateProxy(t)

	p := startProxyTrusting(t, []*x509.Certificate{registry.server.Certificate(), corporateCA},
		WithUpstreamProxy(http.ProxyURL(corporate.URL(t, "corp", "secret"))),
		WithUpstreamProxyCertificate(cert))
	requireTarball(t, p, registry)

	assert.Equal(t, []string{registry.server.Listener.Addr().String()}, corporate.Tunnels())
	assert.Zero(t, registry.peerCerts.Load(), "the registry must never receive the proxy client certificate")
}

func TestProxyFailsWhenMTLSOutboundProxyGetsNoCertificate(t *testing.T) {
	t.Parallel()
	registry := startCertRecordingRegistry(t)
	corporate, corporateCA, _ := startMTLSCorporateProxy(t)

	p := startProxyTrusting(t, []*x509.Certificate{registry.server.Certificate(), corporateCA},
		WithUpstreamProxy(http.ProxyURL(corporate.URL(t, "corp", "secret"))))
	resp, err := newClient(t, p).Get(registry.server.URL + tarballPath)
	if err == nil {
		resp.Body.Close()
		t.Fatalf("expected the request to fail when the outbound proxy gets no client certificate, got status %d", resp.StatusCode)
	}

	assert.Empty(t, corporate.Tunnels())
	assert.Zero(t, registry.hits.Load())
}

func TestProxyWithClientCertificateStillVerifiesOutboundProxy(t *testing.T) {
	t.Parallel()
	registry := startCertRecordingRegistry(t)
	corporate, _, cert := startMTLSCorporateProxy(t)

	p := startProxyTrusting(t, []*x509.Certificate{registry.server.Certificate()},
		WithUpstreamProxy(http.ProxyURL(corporate.URL(t, "corp", "secret"))),
		WithUpstreamProxyCertificate(cert))
	resp, err := newClient(t, p).Get(registry.server.URL + tarballPath)
	if err == nil {
		resp.Body.Close()
		t.Fatalf("expected the request to fail when the outbound proxy's certificate is untrusted, got status %d", resp.StatusCode)
	}

	assert.Empty(t, corporate.Tunnels())
	assert.Zero(t, registry.hits.Load())
}

func TestProxyNeverPresentsClientCertificateToDirectRegistry(t *testing.T) {
	t.Parallel()
	proxied := startCertRecordingRegistry(t)
	direct := startCertRecordingRegistry(t)
	corporate, corporateCA, cert := startMTLSCorporateProxy(t)
	corporateURL := corporate.URL(t, "corp", "secret")
	directHost := direct.server.Listener.Addr().String()
	// A host listed in NO_PROXY makes http.ProxyFromEnvironment return no proxy.
	noProxyForDirect := func(req *http.Request) (*url.URL, error) {
		if req.URL.Host == directHost {
			return nil, nil
		}
		return corporateURL, nil
	}

	p := startProxyTrusting(t, []*x509.Certificate{proxied.server.Certificate(), corporateCA},
		WithUpstreamProxy(noProxyForDirect),
		WithUpstreamProxyCertificate(cert))
	requireTarball(t, p, proxied)
	requireTarball(t, p, direct)

	assert.Equal(t, []string{proxied.server.Listener.Addr().String()}, corporate.Tunnels())
	assert.Zero(t, proxied.peerCerts.Load(), "the proxied registry must never receive the proxy client certificate")
	assert.Zero(t, direct.peerCerts.Load(), "a direct registry must never receive the proxy client certificate")
}
