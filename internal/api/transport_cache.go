package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

// transportKey holds every input newTransport reads, so two clients with equal
// keys can share one connection pool.
type transportKey struct {
	// proxyURL is empty when the proxy comes from the environment.
	proxyURL       string
	caFile         string
	clientCertFile string
	clientKeyFile  string
	insecure       bool
	gitlabCom      bool
}

// TransportCache lets API clients with identical transport settings share a
// connection pool, so requests to the same host reuse a warm connection
// instead of paying for a new TLS handshake.
// Safe for concurrent use.
type TransportCache struct {
	mu    sync.Mutex
	byKey map[transportKey]*http.Transport
}

func NewTransportCache() *TransportCache {
	return &TransportCache{
		byKey: make(map[transportKey]*http.Transport),
	}
}

func (tc *TransportCache) transport(key transportKey) (*http.Transport, error) {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	if t, ok := tc.byKey[key]; ok {
		return t, nil
	}

	t, err := newTransport(key)
	if err != nil {
		return nil, err
	}

	tc.byKey[key] = t
	return t, nil
}

func newTransport(key transportKey) (*http.Transport, error) {
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: key.insecure,
	}

	if key.gitlabCom {
		tlsConfig.CipherSuites = secureCipherSuites
	}

	if key.caFile != "" {
		caCert, err := os.ReadFile(key.caFile)
		if err != nil {
			return nil, fmt.Errorf("error reading cert file: %w", err)
		}
		// use system cert pool as a baseline
		caCertPool, err := x509.SystemCertPool()
		if err != nil {
			return nil, err
		}
		caCertPool.AppendCertsFromPEM(caCert)
		tlsConfig.RootCAs = caCertPool
	}

	if key.clientCertFile != "" && key.clientKeyFile != "" {
		clientCert, err := tls.LoadX509KeyPair(key.clientCertFile, key.clientKeyFile)
		if err != nil {
			return nil, err
		}
		tlsConfig.Certificates = []tls.Certificate{clientCert}
	}

	// Set appropriate timeouts based on whether custom CA is used
	dialTimeout := 5 * time.Second
	keepAlive := 5 * time.Second
	idleTimeout := 30 * time.Second
	if key.caFile != "" {
		dialTimeout = 30 * time.Second
		keepAlive = 30 * time.Second
		idleTimeout = 90 * time.Second
	}

	proxy := http.ProxyFromEnvironment
	if key.proxyURL != "" {
		u, err := url.Parse(key.proxyURL)
		if err != nil {
			return nil, err
		}
		proxy = http.ProxyURL(u)
	}

	dialer := &net.Dialer{
		Timeout:   dialTimeout,
		KeepAlive: keepAlive,
	}
	tlsHandshakeTimeout := 10 * time.Second
	tlsDialer := &proxyAwareTLSDialer{
		dialer:           dialer,
		config:           tlsConfig,
		handshakeTimeout: tlsHandshakeTimeout,
	}

	return &http.Transport{
		Proxy:                 tlsDialer.recordHTTPSProxies(proxy),
		DialContext:           dialer.DialContext,
		DialTLSContext:        tlsDialer.DialTLSContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   25,
		IdleConnTimeout:       idleTimeout,
		TLSHandshakeTimeout:   tlsHandshakeTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       tlsConfig,
	}, nil
}

// proxyAwareTLSDialer opens the TLS connections http.Transport makes itself:
// direct HTTPS connections and connections to HTTPS proxies. The Transport
// offers the target's ALPN protocols to the proxy too, so a proxy that picks
// h2 receives an HTTP/1.1 CONNECT it cannot parse. The handshake with a proxy
// therefore offers only HTTP/1.1; the tunnelled handshake with GitLab still
// offers HTTP/2.
type proxyAwareTLSDialer struct {
	dialer           *net.Dialer
	config           *tls.Config
	handshakeTimeout time.Duration
	// httpsProxyAddrs holds the host:port of every https:// proxy the Proxy
	// func has returned, in the form the Transport passes to DialTLSContext.
	httpsProxyAddrs sync.Map
}

func (d *proxyAwareTLSDialer) recordHTTPSProxies(proxy func(*http.Request) (*url.URL, error)) func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		u, err := proxy(req)
		if err == nil && u != nil && u.Scheme == "https" {
			port := u.Port()
			if port == "" {
				port = "443"
			}
			d.httpsProxyAddrs.LoadOrStore(net.JoinHostPort(u.Hostname(), port), struct{}{})
		}
		return u, err
	}
}

func (d *proxyAwareTLSDialer) DialTLSContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}

	// The Transport adds its ALPN protocols to the shared config before its
	// first dial, so the clone already offers h2 when HTTP/2 is enabled.
	cfg := d.config.Clone()
	if cfg.ServerName == "" {
		cfg.ServerName = host
	}
	if _, ok := d.httpsProxyAddrs.Load(addr); ok {
		cfg.NextProtos = []string{"http/1.1"}
	}
	// http.Transport clears NextProtos for these requests when it dials
	// itself, but a custom DialTLSContext bypasses that.
	if ctx.Value(http1OnlyKey{}) != nil {
		cfg.NextProtos = nil
	}

	conn, err := d.dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}

	handshakeCtx, cancel := context.WithTimeout(ctx, d.handshakeTimeout)
	defer cancel()
	tlsConn := tls.Client(conn, cfg)
	if err := tlsConn.HandshakeContext(handshakeCtx); err != nil {
		if ctx.Err() == nil && handshakeCtx.Err() != nil {
			err = tlsHandshakeTimeoutError{}
		}
		closeErr := conn.Close()
		// HandshakeContext has already closed conn when its context ended.
		if errors.Is(closeErr, net.ErrClosed) {
			closeErr = nil
		}
		return nil, errors.Join(err, closeErr)
	}
	return tlsConn, nil
}

// tlsHandshakeTimeoutError is what http.Transport returns when its own
// TLSHandshakeTimeout expires. Unlike the handshake context's error, it does not
// match context.DeadlineExceeded, which callers read as their own deadline.
type tlsHandshakeTimeoutError struct{}

func (tlsHandshakeTimeoutError) Error() string   { return "net/http: TLS handshake timeout" }
func (tlsHandshakeTimeoutError) Timeout() bool   { return true }
func (tlsHandshakeTimeoutError) Temporary() bool { return true }

// http1OnlyKey marks a request http.Transport keeps off HTTP/2: a websocket
// upgrade. The Transport dials with the request's context values, and pools
// these connections separately, so no other request reuses one.
type http1OnlyKey struct{}

// requiresHTTP1 is http.Request.requiresHTTP1, which is unexported.
func requiresHTTP1(req *http.Request) bool {
	connection := strings.FieldsFunc(req.Header.Get("Connection"), func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
	return slices.ContainsFunc(connection, func(token string) bool { return strings.EqualFold(token, "upgrade") }) &&
		strings.EqualFold(req.Header.Get("Upgrade"), "websocket")
}

// pooledTransport hides CloseIdleConnections, which go-retryablehttp calls
// after every failed request, so that one client's failure does not drop the
// connections other clients share.
type pooledTransport struct {
	rt http.RoundTripper
}

func (t *pooledTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if requiresHTTP1(req) {
		req = req.WithContext(context.WithValue(req.Context(), http1OnlyKey{}, true))
	}
	return t.rt.RoundTrip(req)
}
