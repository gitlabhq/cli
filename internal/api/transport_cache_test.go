//go:build !integration

package api

import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/config"
)

// newConnCountingServer starts a TLS server and reports how many TCP
// connections clients have opened to it.
func newConnCountingServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *atomic.Int64) {
	t.Helper()

	var conns atomic.Int64
	srv := httptest.NewUnstartedServer(handler)
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			conns.Add(1)
		}
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, &conns
}

func serverHost(srv *httptest.Server) string {
	return strings.TrimPrefix(srv.URL, "https://")
}

// newServerConfig points host at srv and applies the extra host settings.
func newServerConfig(t *testing.T, srv *httptest.Server, settings map[string]string) config.Config {
	t.Helper()

	host := serverHost(srv)
	cfg := config.NewBlankConfig()
	require.NoError(t, cfg.Set(host, "api_host", host))
	require.NoError(t, cfg.Set(host, "token", "glpat-test"))
	for key, value := range settings {
		require.NoError(t, cfg.Set(host, key, value))
	}
	return cfg
}

func writeServerCA(t *testing.T, srv *httptest.Server, path string) {
	t.Helper()

	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	require.NoError(t, os.WriteFile(path, pemBytes, 0o600))
}

func mustGet(t *testing.T, client *Client, url string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	require.NoError(t, err)
	resp, err := client.HTTPClient().Do(req)
	require.NoError(t, err)
	defer func() { require.NoError(t, resp.Body.Close()) }()
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
}

func okHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func TestTransportCache_SameSettingsShareConnection(t *testing.T) {
	t.Parallel()

	srv, conns := newConnCountingServer(t, okHandler)
	cfg := newServerConfig(t, srv, map[string]string{"skip_tls_verify": "true"})
	cache := NewTransportCache()

	first, err := NewClientFromConfig(serverHost(srv), cfg, false, "glab-test", WithTransportCache(cache))
	require.NoError(t, err)
	second, err := NewClientFromConfig(serverHost(srv), cfg, false, "glab-test", WithTransportCache(cache))
	require.NoError(t, err)

	mustGet(t, first, srv.URL)
	mustGet(t, second, srv.URL)

	assert.Equal(t, int64(1), conns.Load())
}

func TestTransportCache_WithoutCacheClientsDoNotShareConnection(t *testing.T) {
	t.Parallel()

	srv, conns := newConnCountingServer(t, okHandler)
	cfg := newServerConfig(t, srv, map[string]string{"skip_tls_verify": "true"})

	first, err := NewClientFromConfig(serverHost(srv), cfg, false, "glab-test")
	require.NoError(t, err)
	second, err := NewClientFromConfig(serverHost(srv), cfg, false, "glab-test")
	require.NoError(t, err)

	mustGet(t, first, srv.URL)
	mustGet(t, second, srv.URL)

	assert.Equal(t, int64(2), conns.Load())
}

func TestTransportCache_CloseIdleConnectionsKeepsSharedPool(t *testing.T) {
	t.Parallel()

	srv, conns := newConnCountingServer(t, okHandler)
	cfg := newServerConfig(t, srv, map[string]string{"skip_tls_verify": "true"})
	cache := NewTransportCache()

	first, err := NewClientFromConfig(serverHost(srv), cfg, false, "glab-test", WithTransportCache(cache))
	require.NoError(t, err)
	second, err := NewClientFromConfig(serverHost(srv), cfg, false, "glab-test", WithTransportCache(cache))
	require.NoError(t, err)

	mustGet(t, first, srv.URL)
	first.HTTPClient().CloseIdleConnections()
	mustGet(t, second, srv.URL)

	assert.Equal(t, int64(1), conns.Load())
}

func TestTransportCache_DifferentSettingsDoNotShareConnection(t *testing.T) {
	t.Parallel()

	srv, conns := newConnCountingServer(t, okHandler)
	cache := NewTransportCache()
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	writeServerCA(t, srv, caPath)
	insecureCfg := newServerConfig(t, srv, map[string]string{"skip_tls_verify": "true"})
	caCfg := newServerConfig(t, srv, map[string]string{"ca_cert": caPath})

	insecure, err := NewClientFromConfig(serverHost(srv), insecureCfg, false, "glab-test", WithTransportCache(cache))
	require.NoError(t, err)
	withCA, err := NewClientFromConfig(serverHost(srv), caCfg, false, "glab-test", WithTransportCache(cache))
	require.NoError(t, err)

	mustGet(t, insecure, srv.URL)
	mustGet(t, withCA, srv.URL)

	assert.Equal(t, int64(2), conns.Load())
}

func TestTransportCache_CustomHeadersStayPerClient(t *testing.T) {
	t.Parallel()

	headers := make(chan string, 2)
	srv, conns := newConnCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Get("X-Custom")
		w.WriteHeader(http.StatusOK)
	})
	cache := NewTransportCache()
	plainCfg := newServerConfig(t, srv, map[string]string{"skip_tls_verify": "true"})
	headerCfg := config.NewFromString(fmt.Sprintf(`hosts:
  %q:
    api_host: %q
    token: glpat-test
    skip_tls_verify: true
    custom_headers:
      - name: X-Custom
        value: from-config
`, serverHost(srv), serverHost(srv)))

	withHeader, err := NewClientFromConfig(serverHost(srv), headerCfg, false, "glab-test", WithTransportCache(cache))
	require.NoError(t, err)
	plain, err := NewClientFromConfig(serverHost(srv), plainCfg, false, "glab-test", WithTransportCache(cache))
	require.NoError(t, err)

	mustGet(t, withHeader, srv.URL)
	mustGet(t, plain, srv.URL)

	assert.Equal(t, "from-config", <-headers)
	assert.Empty(t, <-headers)
	assert.Equal(t, int64(1), conns.Load())
}

func TestTransportCache_BuildErrorIsNotCached(t *testing.T) {
	t.Parallel()

	srv, _ := newConnCountingServer(t, okHandler)
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	cfg := newServerConfig(t, srv, map[string]string{"ca_cert": caPath})
	cache := NewTransportCache()

	_, err := NewClientFromConfig(serverHost(srv), cfg, false, "glab-test", WithTransportCache(cache))
	require.ErrorContains(t, err, "error reading cert file")

	writeServerCA(t, srv, caPath)
	client, err := NewClientFromConfig(serverHost(srv), cfg, false, "glab-test", WithTransportCache(cache))
	require.NoError(t, err)
	mustGet(t, client, srv.URL)
}

func TestProxyAwareTLSDialer_HandshakeTimeout(t *testing.T) {
	t.Parallel()

	// The listener never accepts, so the TCP connect succeeds and the TLS
	// handshake waits for a ServerHello that never comes.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	newDialer := func(timeout time.Duration) *proxyAwareTLSDialer {
		return &proxyAwareTLSDialer{dialer: &net.Dialer{}, config: &tls.Config{}, handshakeTimeout: timeout}
	}

	t.Run("its own timeout reports a TLS handshake timeout", func(t *testing.T) {
		t.Parallel()

		_, err := newDialer(50*time.Millisecond).DialTLSContext(t.Context(), "tcp", ln.Addr().String())

		require.EqualError(t, err, "net/http: TLS handshake timeout")
		require.NotErrorIs(t, err, context.DeadlineExceeded)
		var netErr net.Error
		require.ErrorAs(t, err, &netErr)
		assert.True(t, netErr.Timeout())
	})

	t.Run("the caller's deadline is reported as its own", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		_, err := newDialer(time.Minute).DialTLSContext(ctx, "tcp", ln.Addr().String())

		require.ErrorIs(t, err, context.DeadlineExceeded)
	})
}
