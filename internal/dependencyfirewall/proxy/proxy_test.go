//go:build !integration

package proxy

import (
	"bytes"
	"compress/gzip"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProxyForwardsGzippedBody verifies the proxy transparently forwards a
// gzip-encoded response body to the client on the buffered path.
func TestProxyForwardsGzippedBody(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/warn-pkg" {
			var buf bytes.Buffer
			gz := gzip.NewWriter(&buf)
			_, _ = gz.Write([]byte(`{"warning":"license review required"}`))
			assert.NoError(t, gz.Close())
			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(buf.Bytes())
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"name":"ok"}`)
	}))
	defer upstream.Close()

	p, err := New(WithUpstreamRootCAs(certPool(upstream)))
	require.NoError(t, err)
	require.NoError(t, p.Start())
	defer p.Stop()

	client := proxyClient(t, p)

	resp, err := client.Get(upstream.URL + "/warn-pkg")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Contains(t, string(body), "license review required")
}

// TestProxyPreservesUndecompressedContentEncoding guards against the proxy
// stripping Content-Encoding on the buffered response path when the upstream
// returned a payload the Go http.Transport did NOT decompress (anything other
// than the transport's own transparent gzip). In that case the body bytes are
// still encoded and the client needs the Content-Encoding header to decode
// them. Only when Transport itself decompressed (resp.Uncompressed == true)
// is the buffered body plain and Content-Encoding safe to remove.
func TestProxyPreservesUndecompressedContentEncoding(t *testing.T) {
	payload := []byte("opaque-non-gzip-bytes")
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "br") // brotli — Transport does not auto-decompress
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer upstream.Close()

	p, err := New(WithUpstreamRootCAs(certPool(upstream)))
	require.NoError(t, err)
	require.NoError(t, p.Start())
	defer p.Stop()

	client := proxyClient(t, p)
	// Disable client transparent decoding so we observe the wire response.
	tr, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	tr.DisableCompression = true

	resp, err := client.Get(upstream.URL + "/some-pkg")
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, "br", resp.Header.Get("Content-Encoding"),
		"proxy must not strip Content-Encoding when it did not decompress the body")
	assert.Equal(t, payload, body, "body must reach the client unchanged (still encoded)")
}

func TestProxyStreamsTarballByteIdentical(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 3<<20) // 3 MiB tarball
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer upstream.Close()

	p, err := New(WithUpstreamRootCAs(certPool(upstream)))
	require.NoError(t, err)
	require.NoError(t, p.Start())
	defer p.Stop()

	client := proxyClient(t, p)

	resp, err := client.Get(upstream.URL + "/stream-pkg/-/stream-pkg-1.0.0.tgz")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, payload, body, "tarball must be forwarded byte-identical")
}

func proxyClient(t *testing.T, p *Proxy) *http.Client {
	t.Helper()
	proxyURL, err := url.Parse("http://" + p.Addr())
	require.NoError(t, err)

	roots := x509.NewCertPool()
	roots.AddCert(p.CACertificate())

	return &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{RootCAs: roots, InsecureSkipVerify: true}, //nolint:gosec // test client trusts proxy's MITM leaf certs
		},
	}
}

// certPool returns an x509 pool trusting only the given httptest.Server's
// self-signed certificate, for use with WithUpstreamRootCAs.
func certPool(s *httptest.Server) *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(s.Certificate())
	return pool
}

// TestProxyRejectsUntrustedUpstreamByDefault ensures the proxy verifies the
// upstream registry's certificate. Without WithUpstreamRootCAs, an
// httptest.Server's self-signed cert must not be accepted: this is the
// regression guard for InsecureSkipVerify sneaking back in.
func TestProxyRejectsUntrustedUpstreamByDefault(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	p, err := New()
	require.NoError(t, err)
	require.NoError(t, p.Start())
	defer p.Stop()

	client := proxyClient(t, p)
	resp, err := client.Get(upstream.URL + "/some-pkg")
	if err == nil {
		// The proxy currently swallows RoundTrip errors and closes the tunnel;
		// the client sees a broken connection rather than a Go TLS error.
		resp.Body.Close()
		t.Fatalf("expected request to fail because upstream cert is untrusted, got status %d", resp.StatusCode)
	}
}
