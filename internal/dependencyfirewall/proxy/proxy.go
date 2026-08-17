package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
)

type certAuthority struct {
	cert    *x509.Certificate
	key     *rsa.PrivateKey
	certDER []byte

	mu    sync.Mutex
	cache map[string]*tls.Certificate
}

func newCertAuthority() (*certAuthority, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "glab Dependency Firewall CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &certAuthority{cert: cert, key: key, certDER: der, cache: map[string]*tls.Certificate{}}, nil
}

// randomSerial returns a cryptographically random 128-bit certificate serial
// number, as recommended by RFC 5280 §4.1.2.2.
func randomSerial() (*big.Int, error) {
	serialBytes := make([]byte, 16)
	if _, err := rand.Read(serialBytes); err != nil {
		return nil, err
	}
	return new(big.Int).SetBytes(serialBytes), nil
}

func (ca *certAuthority) leafFor(host string) (*tls.Certificate, error) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	if c, ok := ca.cache[host]; ok {
		return c, nil
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{host},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return nil, err
	}
	leaf := &tls.Certificate{Certificate: [][]byte{der, ca.certDER}, PrivateKey: key}
	ca.cache[host] = leaf
	return leaf, nil
}

type Proxy struct {
	ca       *certAuthority
	listener net.Listener
	server   *http.Server
	upstream *http.Transport

	mu       sync.Mutex
	verdicts []verdict.Entry
}

// Option configures a Proxy at construction time.
type Option func(*Proxy)

// WithUpstreamRootCAs overrides the certificate pool the proxy uses to verify
// the upstream registry's TLS certificate. When unset, the proxy verifies
// against the system trust store, which is the correct default. This option
// exists for tests (which need to trust an httptest.Server's self-signed cert)
// and for future support of enterprise CA bundles.
func WithUpstreamRootCAs(pool *x509.CertPool) Option {
	return func(p *Proxy) {
		p.upstream.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
}

func New(opts ...Option) (*Proxy, error) {
	ca, err := newCertAuthority()
	if err != nil {
		return nil, err
	}
	p := &Proxy{
		ca: ca,
		// Upstream verification uses the system trust store by default; the
		// package manager verifies the proxy via its configured CA bundle, and
		// the proxy in turn verifies the real registry.
		upstream: &http.Transport{},
	}
	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}

func (p *Proxy) CACertificate() *x509.Certificate { return p.ca.cert }

func (p *Proxy) Addr() string {
	if p.listener == nil {
		return ""
	}
	return p.listener.Addr().String()
}

func (p *Proxy) Start() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	p.listener = ln
	p.server = &http.Server{
		Handler:           http.HandlerFunc(p.handle),
		ReadHeaderTimeout: 30 * time.Second,
	}
	go func() { _ = p.server.Serve(ln) }()
	return nil
}

func (p *Proxy) Stop() {
	if p.server != nil {
		if err := p.server.Shutdown(context.Background()); err != nil {
			dbg.Debugf("dependency firewall proxy shutdown: %v", err)
		}
	}
}

func (p *Proxy) Verdicts() []verdict.Entry {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]verdict.Entry, len(p.verdicts))
	copy(out, p.verdicts)
	return out
}

func (p *Proxy) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	http.Error(w, "only CONNECT supported", http.StatusMethodNotAllowed)
}

func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	authority := r.Host
	if r.URL.Host != "" {
		authority = r.URL.Host
	}
	host, _, err := net.SplitHostPort(authority)
	if err != nil {
		host = authority
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer clientConn.Close()

	_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	leaf, err := p.ca.leafFor(host)
	if err != nil {
		return
	}
	tlsConn := tls.Server(clientConn, &tls.Config{Certificates: []tls.Certificate{*leaf}}) //nolint:gosec // leaf cert dictates negotiated version; min version not required for localhost MITM
	if err := tlsConn.Handshake(); err != nil {
		return
	}
	defer tlsConn.Close()

	p.serveTunnel(tlsConn, authority)
}

func (p *Proxy) serveTunnel(conn net.Conn, authority string) {
	br := bufio.NewReader(conn)
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}

		req.URL.Scheme = "https"
		req.URL.Host = authority
		req.RequestURI = ""
		req.Header.Del("Accept-Encoding")

		resp, err := p.upstream.RoundTrip(req)
		if err != nil {
			return
		}

		// Tarball downloads can be very large (100s of MB), so stream them
		// straight through instead of buffering the whole payload in memory.
		if isBinaryDownload(resp) {
			err := resp.Write(conn)
			resp.Body.Close()
			if err != nil {
				return
			}
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return
		}

		resp.Body = io.NopCloser(bytes.NewReader(body))
		resp.ContentLength = int64(len(body))
		// Only strip Content-Encoding when the Go http.Transport transparently
		// decompressed the body for us (resp.Uncompressed). For any encoding
		// Transport does not auto-decode (brotli, deflate, etc.), the buffered
		// body is still encoded and the client needs the header to decode it.
		if resp.Uncompressed {
			resp.Header.Del("Content-Encoding")
		}
		resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
		resp.TransferEncoding = nil
		if err := resp.Write(conn); err != nil {
			return
		}
	}
}

// isBinaryDownload reports whether resp is a successful binary package payload
// (a tarball) that should be streamed rather than buffered for inspection.
func isBinaryDownload(resp *http.Response) bool {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/octet-stream") {
		return true
	}
	return strings.HasSuffix(resp.Request.URL.Path, ".tgz")
}
