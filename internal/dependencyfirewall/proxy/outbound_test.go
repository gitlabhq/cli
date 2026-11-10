//go:build !integration

package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
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
	for _, k := range []string{"HTTPS_PROXY", "https_proxy"} {
		t.Setenv(k, "http://egress.example:3128")
	}
	for _, k := range []string{"HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(k, "")
	}
	p, err := New(stubMatcher{}, stubChecker{}, "group/proj")
	require.NoError(t, err)

	for _, target := range []string{"https://registry.example/pkg", "http://registry.example/pkg"} {
		got, err := p.upstream.Proxy(httptest.NewRequest(http.MethodGet, target, http.NoBody))
		require.NoError(t, err)
		require.NotNil(t, got, target)
		assert.Equal(t, "http://egress.example:3128", got.String(), target)
	}
}

type warningRecorder struct {
	mu    sync.Mutex
	lines []string
}

func (w *warningRecorder) Logf(format string, a ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lines = append(w.lines, fmt.Sprintf(format, a...))
}

func (w *warningRecorder) Lines() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.lines...)
}

func startWarningProxy(t *testing.T, outbound *url.URL, warn policy.Logf, upstreams ...*httptest.Server) *Proxy {
	t.Helper()
	pool := x509.NewCertPool()
	for _, u := range upstreams {
		pool.AddCert(u.Certificate())
	}
	p, err := New(stubMatcher{}, stubChecker{}, "group/proj",
		WithUpstreamRootCAs(pool), WithUpstreamProxy(http.ProxyURL(outbound)), WithUpstreamFailureWarning(warn))
	require.NoError(t, err)
	require.NoError(t, p.Start())
	t.Cleanup(p.Stop)
	return p
}

func getExpectingFailure(t *testing.T, client *http.Client, rawURL string) {
	t.Helper()
	resp, err := client.Get(rawURL)
	if err == nil {
		resp.Body.Close()
		t.Fatalf("expected the request to %s to fail, got status %d", rawURL, resp.StatusCode)
	}
}

func TestProxyWarnsOncePerRegistryWhenUpstreamFails(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "TARBALL")
	}))
	defer upstream.Close()
	corporate := startCorporateProxy(t, "corp-user", "right-password")
	warnings := &warningRecorder{}

	p := startWarningProxy(t, corporate.URL(t, "corp-user", "wrong-password"), warnings.Logf, upstream)
	client := newClient(t, p)
	getExpectingFailure(t, client, upstream.URL+"/left-pad/-/left-pad-1.3.0.tgz")
	getExpectingFailure(t, client, upstream.URL+"/is-odd/-/is-odd-3.0.1.tgz")

	lines := warnings.Lines()
	require.Len(t, lines, 1, "repeated failures to one registry must warn once")
	assert.Contains(t, lines[0], upstream.Listener.Addr().String())
	assert.Contains(t, lines[0], "Proxy Authentication Required")
	assert.NotContains(t, lines[0], "corp-user")
	assert.NotContains(t, lines[0], "password")
}

func TestProxyWarnsForEachFailingRegistry(t *testing.T) {
	t.Parallel()
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "TARBALL")
	})
	first := httptest.NewTLSServer(handler)
	defer first.Close()
	second := httptest.NewTLSServer(handler)
	defer second.Close()
	corporate := startCorporateProxy(t, "corp-user", "right-password")
	warnings := &warningRecorder{}

	p := startWarningProxy(t, corporate.URL(t, "corp-user", "wrong-password"), warnings.Logf, first, second)
	client := newClient(t, p)
	getExpectingFailure(t, client, first.URL+"/left-pad/-/left-pad-1.3.0.tgz")
	getExpectingFailure(t, client, second.URL+"/left-pad/-/left-pad-1.3.0.tgz")
	getExpectingFailure(t, client, first.URL+"/is-odd/-/is-odd-3.0.1.tgz")

	lines := warnings.Lines()
	require.Len(t, lines, 2, "each failing registry must warn once")
	assert.Contains(t, lines[0], first.Listener.Addr().String())
	assert.Contains(t, lines[1], second.Listener.Addr().String())
}

func TestProxyDoesNotWarnWhenClientAbortsUpload(t *testing.T) {
	t.Parallel()
	arrived := make(chan struct{}, 1)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		_, _ = io.Copy(io.Discard, r.Body)
	}))
	defer upstream.Close()
	corporate := startCorporateProxy(t, "corp-user", "right-password")
	warnings := &warningRecorder{}
	p := startWarningProxy(t, corporate.URL(t, "corp-user", "right-password"), warnings.Logf, upstream)

	body, bodyWriter := io.Pipe()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, upstream.URL+"/left-pad", body)
	require.NoError(t, err)
	req.ContentLength = 1 << 20
	done := make(chan error, 1)
	go func() {
		resp, err := newClient(t, p).Do(req)
		if err == nil {
			resp.Body.Close()
		}
		done <- err
	}()
	_, err = bodyWriter.Write([]byte("partial upload"))
	require.NoError(t, err)
	<-arrived
	bodyWriter.CloseWithError(errors.New("package manager aborted the upload"))
	require.Error(t, <-done)
	p.Stop()

	assert.Empty(t, warnings.Lines(), "a request the package manager aborted is not a registry failure")
}

func TestProxyKeepsPlainHTTPDirectInCGI(t *testing.T) {
	t.Setenv("REQUEST_METHOD", "GET")
	for _, k := range []string{"HTTPS_PROXY", "https_proxy"} {
		t.Setenv(k, "http://egress.example:3128")
	}
	for _, k := range []string{"HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(k, "")
	}
	p, err := New(stubMatcher{}, stubChecker{}, "group/proj")
	require.NoError(t, err)

	got, err := p.upstream.Proxy(httptest.NewRequest(http.MethodGet, "http://registry.example/pkg", http.NoBody))

	require.NoError(t, err, "httpproxy refuses an HTTP_PROXY value in a CGI environment, so the fallback must not set one")
	assert.Nil(t, got)
}

func TestProxyReadsUppercaseProxyVariablesFirst(t *testing.T) {
	proxyFor := func(t *testing.T, p *Proxy, target string) string {
		t.Helper()
		got, err := p.upstream.Proxy(httptest.NewRequest(http.MethodGet, target, http.NoBody))
		require.NoError(t, err)
		if got == nil {
			return ""
		}
		return got.String()
	}
	t.Setenv("REQUEST_METHOD", "")
	t.Setenv("HTTPS_PROXY", "http://upper.example:3128")
	t.Setenv("https_proxy", "http://lower.example:3128")
	t.Setenv("NO_PROXY", "direct.example")
	t.Setenv("no_proxy", "other.example")

	t.Run("when HTTP_PROXY is unset", func(t *testing.T) {
		t.Setenv("HTTP_PROXY", "")
		t.Setenv("http_proxy", "")
		p, err := New(stubMatcher{}, stubChecker{}, "group/proj")
		require.NoError(t, err)

		assert.Equal(t, "http://upper.example:3128", proxyFor(t, p, "https://registry.example/pkg"))
		assert.Equal(t, "http://upper.example:3128", proxyFor(t, p, "http://registry.example/pkg"))
		assert.Empty(t, proxyFor(t, p, "https://direct.example/pkg"), "NO_PROXY wins over no_proxy")
		assert.Equal(t, "http://upper.example:3128", proxyFor(t, p, "https://other.example/pkg"), "no_proxy applies only when NO_PROXY is unset")
	})

	t.Run("when both spellings of HTTP_PROXY are set", func(t *testing.T) {
		t.Setenv("HTTP_PROXY", "http://upper-http.example:3128")
		t.Setenv("http_proxy", "http://lower-http.example:3128")
		p, err := New(stubMatcher{}, stubChecker{}, "group/proj")
		require.NoError(t, err)

		assert.Equal(t, "http://upper-http.example:3128", proxyFor(t, p, "http://registry.example/pkg"))
	})
}

func TestWithUpstreamProxyForOverridesOnlyItsScheme(t *testing.T) {
	t.Parallel()
	fixed := func(raw string) func(*http.Request) (*url.URL, error) {
		return func(*http.Request) (*url.URL, error) { return url.Parse(raw) }
	}
	p, err := New(stubMatcher{}, stubChecker{}, "group/proj",
		WithUpstreamProxy(fixed("http://default.example:3128")),
		WithUpstreamProxyFor("http", fixed("http://plain.example:3128")),
	)
	require.NoError(t, err)

	plain, err := p.upstream.Proxy(httptest.NewRequest(http.MethodGet, "http://registry.example/pkg", http.NoBody))
	require.NoError(t, err)
	assert.Equal(t, "http://plain.example:3128", plain.String())

	secure, err := p.upstream.Proxy(httptest.NewRequest(http.MethodGet, "https://registry.example/pkg", http.NoBody))
	require.NoError(t, err)
	assert.Equal(t, "http://default.example:3128", secure.String())
}
