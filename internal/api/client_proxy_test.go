//go:build !integration

package api

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/config"
)

func TestNewClientFromConfigProxy(t *testing.T) {
	t.Run("configured proxy", func(t *testing.T) {
		cfg := config.NewBlankConfig()
		require.NoError(t, cfg.Set("gitlab.example.com", "token", "glpat-test"))
		require.NoError(t, cfg.Set("gitlab.example.com", "proxy", "https://proxy.local:8443"))

		client, err := NewClientFromConfig("gitlab.example.com", cfg, false, "glab-test")
		require.NoError(t, err)
		transport, ok := client.HTTPClient().Transport.(*http.Transport)
		require.True(t, ok)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://gitlab.example.com/api/v4", http.NoBody)
		require.NoError(t, err)

		proxyURL, err := transport.Proxy(req)
		require.NoError(t, err)
		require.NotNil(t, proxyURL)
		assert.Equal(t, "https://proxy.local:8443", proxyURL.String())
	})

	t.Run("missing host uses the environment", func(t *testing.T) {
		cfg := config.NewBlankConfig()
		require.NoError(t, cfg.Set("missing.example.com", "token", "glpat-test"))

		client, err := NewClientFromConfig("missing.example.com", cfg, false, "glab-test")
		require.NoError(t, err)
		assert.Nil(t, client.proxyURL)
	})

	t.Run("invalid configured proxy", func(t *testing.T) {
		cfg := config.NewBlankConfig()
		require.NoError(t, cfg.Set("bad.example.com", "token", "glpat-test"))
		require.NoError(t, cfg.Set("bad.example.com", "proxy", "://bad-url"))

		_, err := NewClientFromConfig("bad.example.com", cfg, false, "glab-test")
		require.ErrorContains(t, err, "failed to resolve proxy")
	})
}

// newHTTP2ConnectProxy starts an HTTPS CONNECT proxy whose TLS listener offers
// h2 in ALPN, as some corporate proxies do. It reports the protocol of each
// CONNECT request it tunnels.
func newHTTP2ConnectProxy(t *testing.T) (*httptest.Server, <-chan string) {
	t.Helper()

	connects := make(chan string, 1)
	proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT only", http.StatusMethodNotAllowed)
			return
		}
		connects <- r.Proto

		upstream, err := net.Dial("tcp", r.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer upstream.Close()

		downstream, brw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer downstream.Close()

		if _, err := downstream.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
			return
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = io.Copy(upstream, brw)
		}()
		_, _ = io.Copy(downstream, upstream)
		downstream.Close()
		<-done
	}))
	proxy.EnableHTTP2 = true
	proxy.TLS = &tls.Config{NextProtos: []string{"h2", "http/1.1"}}
	proxy.StartTLS()
	t.Cleanup(proxy.Close)
	return proxy, connects
}

func newHTTP2Server(t *testing.T) (*httptest.Server, <-chan string) {
	t.Helper()

	protos := make(chan string, 1)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protos <- r.Proto
		w.WriteHeader(http.StatusOK)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, protos
}

func TestNewClientFromConfig_HTTPSProxyOfferingHTTP2(t *testing.T) {
	t.Parallel()

	t.Run("tunnels through the proxy over HTTP/1.1", func(t *testing.T) {
		t.Parallel()

		target, protos := newHTTP2Server(t)
		proxy, connects := newHTTP2ConnectProxy(t)
		// httptest servers share one certificate, so this CA trusts the proxy too.
		caPath := filepath.Join(t.TempDir(), "ca.pem")
		writeServerCA(t, target, caPath)
		cfg := newServerConfig(t, target, map[string]string{"ca_cert": caPath, "proxy": proxy.URL})

		client, err := NewClientFromConfig(serverHost(target), cfg, false, "glab-test")
		require.NoError(t, err)
		mustGet(t, client, target.URL)

		assert.Equal(t, "HTTP/1.1", <-connects)
		assert.Equal(t, "HTTP/2.0", <-protos)
	})

	t.Run("direct connections keep HTTP/2", func(t *testing.T) {
		t.Parallel()

		target, protos := newHTTP2Server(t)
		caPath := filepath.Join(t.TempDir(), "ca.pem")
		writeServerCA(t, target, caPath)
		cfg := newServerConfig(t, target, map[string]string{"ca_cert": caPath})

		client, err := NewClientFromConfig(serverHost(target), cfg, false, "glab-test")
		require.NoError(t, err)
		mustGet(t, client, target.URL)

		assert.Equal(t, "HTTP/2.0", <-protos)
	})

	t.Run("websocket upgrades without a proxy use HTTP/1.1", func(t *testing.T) {
		t.Parallel()

		target, protos := newHTTP2Server(t)
		caPath := filepath.Join(t.TempDir(), "ca.pem")
		writeServerCA(t, target, caPath)
		cfg := newServerConfig(t, target, map[string]string{"ca_cert": caPath})

		client, err := NewClientFromConfig(serverHost(target), cfg, false, "glab-test", WithTransportCache(NewTransportCache()))
		require.NoError(t, err)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target.URL, http.NoBody)
		require.NoError(t, err)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		resp, err := client.HTTPClient().Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		assert.Equal(t, "HTTP/1.1", <-protos)
	})
}
