//go:build !integration

package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// corporateProxy is a CONNECT-only forward proxy that requires basic auth, like
// the outbound proxy in a network with no direct route to public registries.
type corporateProxy struct {
	server  *httptest.Server
	baseURL string
	auth    string

	mu      sync.Mutex
	tunnels []string
}

func newCorporateProxy(user, password string) *corporateProxy {
	cp := &corporateProxy{auth: "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password))}
	cp.server = httptest.NewUnstartedServer(http.HandlerFunc(cp.handle))
	return cp
}

func startCorporateProxy(t *testing.T, user, password string) *corporateProxy {
	t.Helper()
	cp := newCorporateProxy(user, password)
	cp.server.Start()
	t.Cleanup(cp.server.Close)
	cp.baseURL = cp.server.URL
	return cp
}

// startTLSCorporateProxy serves the proxy over TLS with a certificate from its
// own CA, which it returns. httptest's built-in certificate is shared with the
// upstream, so trusting the upstream would silently trust the proxy too.
func startTLSCorporateProxy(t *testing.T, user, password string) (*corporateProxy, *x509.Certificate) {
	t.Helper()
	ca, err := newCertAuthority()
	require.NoError(t, err)
	leaf, err := ca.leafFor("localhost")
	require.NoError(t, err)

	cp := newCorporateProxy(user, password)
	cp.server.TLS = &tls.Config{Certificates: []tls.Certificate{*leaf}}
	cp.server.Config.ErrorLog = log.New(io.Discard, "", 0)
	cp.server.StartTLS()
	t.Cleanup(cp.server.Close)
	_, port, err := net.SplitHostPort(cp.server.Listener.Addr().String())
	require.NoError(t, err)
	cp.baseURL = "https://" + net.JoinHostPort("localhost", port)
	return cp, ca.cert
}

func (cp *corporateProxy) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		http.Error(w, "CONNECT only", http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get("Proxy-Authorization") != cp.auth {
		w.Header().Set("Proxy-Authenticate", `Basic realm="corporate"`)
		w.WriteHeader(http.StatusProxyAuthRequired)
		return
	}
	cp.mu.Lock()
	cp.tunnels = append(cp.tunnels, r.Host)
	cp.mu.Unlock()

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	upstream, err := net.Dial("tcp", r.Host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer upstream.Close()
	client, _, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer client.Close()
	_, _ = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, client); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, upstream); done <- struct{}{} }()
	<-done
}

func (cp *corporateProxy) Tunnels() []string {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	return append([]string(nil), cp.tunnels...)
}

func (cp *corporateProxy) URL(t *testing.T, user, password string) *url.URL {
	t.Helper()
	u, err := url.Parse(cp.baseURL)
	require.NoError(t, err)
	u.User = url.UserPassword(user, password)
	return u
}

func startProxyThrough(t *testing.T, upstream *httptest.Server, outbound *url.URL, extraRoots ...*x509.Certificate) *Proxy {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(upstream.Certificate())
	for _, c := range extraRoots {
		pool.AddCert(c)
	}
	p, err := New(stubMatcher{}, stubChecker{}, "group/proj",
		WithUpstreamRootCAs(pool), WithUpstreamProxy(http.ProxyURL(outbound)))
	require.NoError(t, err)
	require.NoError(t, p.Start())
	t.Cleanup(p.Stop)
	return p
}

func TestProxyReachesUpstreamThroughOutboundProxy(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "TARBALL")
	}))
	defer upstream.Close()
	corporate := startCorporateProxy(t, "corp", "secret")

	p := startProxyThrough(t, upstream, corporate.URL(t, "corp", "secret"))
	resp, err := newClient(t, p).Get(upstream.URL + "/left-pad/-/left-pad-1.3.0.tgz")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "TARBALL", string(body))
	assert.Equal(t, []string{upstream.Listener.Addr().String()}, corporate.Tunnels())
}

func TestProxyDoesNotFallBackToDirectWhenOutboundProxyRefuses(t *testing.T) {
	t.Parallel()
	var upstreamHits int
	var mu sync.Mutex
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		upstreamHits++
		mu.Unlock()
		_, _ = io.WriteString(w, "TARBALL")
	}))
	defer upstream.Close()
	corporate := startCorporateProxy(t, "corp", "secret")

	p := startProxyThrough(t, upstream, corporate.URL(t, "corp", "wrong"))
	resp, err := newClient(t, p).Get(upstream.URL + "/left-pad/-/left-pad-1.3.0.tgz")
	if err == nil {
		resp.Body.Close()
		t.Fatalf("expected the request to fail when the outbound proxy refuses it, got status %d", resp.StatusCode)
	}

	mu.Lock()
	defer mu.Unlock()
	assert.Zero(t, upstreamHits)
	assert.Empty(t, corporate.Tunnels())
}

func TestProxyReachesUpstreamThroughTrustedTLSOutboundProxy(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "TARBALL")
	}))
	defer upstream.Close()
	corporate, corporateCA := startTLSCorporateProxy(t, "corp", "secret")

	p := startProxyThrough(t, upstream, corporate.URL(t, "corp", "secret"), corporateCA)
	resp, err := newClient(t, p).Get(upstream.URL + "/left-pad/-/left-pad-1.3.0.tgz")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "TARBALL", string(body))
	assert.Equal(t, []string{upstream.Listener.Addr().String()}, corporate.Tunnels())
}

func TestProxyFailsWhenTLSOutboundProxyIsUntrusted(t *testing.T) {
	t.Parallel()
	var upstreamHits int
	var mu sync.Mutex
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		upstreamHits++
		mu.Unlock()
		_, _ = io.WriteString(w, "TARBALL")
	}))
	defer upstream.Close()
	corporate, _ := startTLSCorporateProxy(t, "corp", "secret")

	p := startProxyThrough(t, upstream, corporate.URL(t, "corp", "secret"))
	resp, err := newClient(t, p).Get(upstream.URL + "/left-pad/-/left-pad-1.3.0.tgz")
	if err == nil {
		resp.Body.Close()
		t.Fatalf("expected the request to fail when the outbound proxy's certificate is untrusted, got status %d", resp.StatusCode)
	}

	mu.Lock()
	defer mu.Unlock()
	assert.Zero(t, upstreamHits)
	assert.Empty(t, corporate.Tunnels())
}

func TestProxyUsesEnvironmentOutboundProxyByDefault(t *testing.T) {
	t.Parallel()
	p, err := New(stubMatcher{}, stubChecker{}, "group/proj")
	require.NoError(t, err)

	require.NotNil(t, p.upstream.Proxy)
	assert.Equal(t,
		reflect.ValueOf(http.ProxyFromEnvironment).Pointer(),
		reflect.ValueOf(p.upstream.Proxy).Pointer())
}
