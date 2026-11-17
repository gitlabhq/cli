package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sync"
)

// proxyCertDialer presents a client certificate to https:// outbound proxies
// only. http.Transport handshakes with an https:// proxy using
// TLSClientConfig, the same config it uses for the registry inside the
// tunnel, so a certificate set there would reach every registry. Instead,
// proxy reports an https:// proxy as http:// on the same address, and
// dialContext performs the TLS handshake itself when it dials that address.
// The transport still sends CONNECT, Proxy-Authorization and the registry
// handshake over the returned connection.
type proxyCertDialer struct {
	next      func(*http.Request) (*url.URL, error)
	tlsConfig *tls.Config
	dialer    net.Dialer

	mu      sync.Mutex
	proxies map[string]struct{}
}

// presentCertToProxy makes t present cert to https:// outbound proxies, which
// it still verifies against t's RootCAs.
func presentCertToProxy(t *http.Transport, cert tls.Certificate) {
	cfg := t.TLSClientConfig.Clone()
	cfg.Certificates = []tls.Certificate{cert}
	d := &proxyCertDialer{next: t.Proxy, tlsConfig: cfg, proxies: map[string]struct{}{}}
	t.Proxy = d.proxy
	t.DialContext = d.dialContext
}

func (d *proxyCertDialer) proxy(req *http.Request) (*url.URL, error) {
	u, err := d.next(req)
	if err != nil || u == nil || u.Scheme != "https" {
		return u, err
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	addr := net.JoinHostPort(u.Hostname(), port)
	d.mu.Lock()
	d.proxies[addr] = struct{}{}
	d.mu.Unlock()

	plain := *u
	plain.Scheme = "http"
	plain.Host = addr
	return &plain, nil
}

func (d *proxyCertDialer) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	conn, err := d.dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	_, isProxy := d.proxies[addr]
	d.mu.Unlock()
	if !isProxy {
		return conn, nil
	}

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, errors.Join(err, conn.Close())
	}
	cfg := d.tlsConfig.Clone()
	cfg.ServerName = host
	tlsConn := tls.Client(conn, cfg)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return nil, errors.Join(err, conn.Close())
	}
	return tlsConn, nil
}
