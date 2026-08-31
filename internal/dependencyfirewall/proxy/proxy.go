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
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
)

// policyCheckTimeout bounds a single policy evaluation (the REST call to the
// dependency_firewall/evaluate endpoint). Without it a hung or unreachable
// backend would block the tunnel goroutine and its TLS connection
// indefinitely; on timeout the check returns an error and the proxy fails
// closed (blocks) rather than hanging.
const policyCheckTimeout = 60 * time.Second

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
	matcher  Matcher
	checker  policy.Checker

	projectID string

	// ctx is the proxy-lifetime context; it bounds policy checks made on
	// hijacked MITM tunnels, which have no request-scoped context of their
	// own. cancel tears it down on Stop so in-flight checks are cancelled.
	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	verdicts []verdict.Entry
	seen     map[string]struct{}
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
		p.upstream.TLSClientConfig = &tls.Config{
			RootCAs:    pool,
			MinVersion: tls.VersionTLS12,
			NextProtos: []string{"http/1.1"}, // see New(): tunnel is HTTP/1.1
		}
	}
}

func New(matcher Matcher, checker policy.Checker, projectID string, opts ...Option) (*Proxy, error) {
	ca, err := newCertAuthority()
	if err != nil {
		return nil, err
	}
	p := &Proxy{
		ca: ca,
		// Upstream verification uses the system trust store by default; the
		// package manager verifies the proxy via its configured CA bundle, and
		// the proxy in turn verifies the real registry.
		//
		// Force HTTP/1.1 upstream: the tunnel we serve back to the client is
		// HTTP/1.1 (bufio.NewReader + http.ReadRequest + resp.Write), so an
		// HTTP/2 response from the upstream would be re-serialized as HTTP/2
		// framing over an HTTP/1 tunnel and the client sees
		// `UnknownProtocol('HTTP/2.0')`.
		upstream: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
				NextProtos: []string{"http/1.1"},
			},
			TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{},
		},
		matcher:   matcher,
		checker:   checker,
		projectID: projectID,
		seen:      map[string]struct{}{},
	}
	p.ctx, p.cancel = context.WithCancel(context.Background())
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
	if p.cancel != nil {
		p.cancel()
	}
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

func (p *Proxy) record(e verdict.Entry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.seen[e.Key()]; ok {
		return
	}
	p.seen[e.Key()] = struct{}{}
	p.verdicts = append(p.verdicts, e)
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

	// A hijacked MITM tunnel outlives the CONNECT request and serves many
	// requests, so r.Context() (cancelled when handleConnect returns) is the
	// wrong lifetime. Use the proxy-lifetime context, which Stop cancels.
	p.serveTunnel(p.ctx, tlsConn, authority) //nolint:contextcheck // MITM tunnel is bounded by the proxy lifetime, not the CONNECT request
}

func (p *Proxy) serveTunnel(ctx context.Context, conn net.Conn, authority string) {
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

		// Match before the round trip: an upload carries its identity in the
		// body, which the matcher reads and restores before RoundTrip.
		m := p.matcher.Match(req)

		if m.Matched && !m.Pass {
			// The matcher recognized an in-scope request but did not clear it
			// for the policy check — e.g. an over-limit upload body it could
			// not inspect. Fail closed.
			p.record(verdict.Entry{
				Package: m.Coordinate.Name,
				Version: m.Coordinate.Version,
				Verdict: verdict.Blocked,
				Status:  http.StatusForbidden,
				Reason:  m.Reason,
			})
			_, _ = io.WriteString(conn, blockResponse(m.Coordinate.Ecosystem, m.Reason))
			drainAndClose(req.Body)
			return
		}
		if m.Matched {
			res := p.checkPolicy(ctx, m)
			if res.Blocked() {
				p.record(verdict.Entry{
					Package: m.Coordinate.Name,
					Version: m.Coordinate.Version,
					Verdict: verdict.Blocked,
					Status:  http.StatusForbidden,
					Reason:  res.Reason,
				})
				_, _ = io.WriteString(conn, blockResponse(m.Coordinate.Ecosystem, res.Reason))
				drainAndClose(req.Body)
				return
			}
			if res.Verdict == verdict.Warning {
				// Status is left unset: a warning allows the request through,
				// but the upstream round trip has not happened yet, so the real
				// response status is unknown here. Recording StatusOK would be a
				// guess that could contradict a later non-200 upstream response.
				p.record(verdict.Entry{
					Package: m.Coordinate.Name,
					Version: m.Coordinate.Version,
					Verdict: verdict.Warning,
					Reason:  res.Reason,
				})
			}
		}
		req.Header.Set(firewallHeader, "allowed")

		resp, err := p.upstream.RoundTrip(req)
		if err != nil {
			return
		}

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

// checkPolicy runs the policy check for a matched request under a bounded
// context so a hung or unreachable backend degrades to a block rather than
// hanging the tunnel goroutine. It fails closed: any error obtaining a
// decision is mapped to a Blocked result. The deferred cancel scopes the
// context to this call, which matters because the caller invokes it once per
// request in a long-lived tunnel loop.
func (p *Proxy) checkPolicy(ctx context.Context, m Match) policy.Result {
	checkCtx, cancel := context.WithTimeout(ctx, policyCheckTimeout)
	defer cancel()
	res, err := p.checker.Check(checkCtx, policy.Request{
		Coordinate: m.Coordinate,
		ProjectID:  p.projectID,
		Operation:  m.Operation,
	})
	if err != nil {
		// Fail closed: a policy decision we cannot obtain is treated as a
		// block, never allowed through. The CachingChecker also enforces
		// this, but the proxy must not depend on its wrapper for its
		// security posture.
		dbg.Debugf("dependency firewall policy check failed for %s: %v", m.Coordinate.Key(), err)
		return policy.Result{
			Verdict: verdict.Blocked,
			Reason:  fmt.Sprintf("policy check failed: %v", err),
		}
	}
	return res
}

// blockDrainLimit bounds how much of a blocked upload's request body the proxy
// reads and discards before closing the connection. On a blocked upload the
// client may still be streaming its (potentially large) body; a bare Close
// leaves those bytes unread, so some clients see a connection reset and never
// read the synthesized 403. Draining a bounded amount lets the client's write
// drain and its read of the 403 succeed, while the cap keeps a hostile or huge
// upload from tying up the tunnel goroutine.
const blockDrainLimit = 1 << 20 // 1 MiB

// drainAndClose reads and discards up to blockDrainLimit bytes from body, then
// closes it. body may be nil. It is used after a synthesized 403 so the client
// reliably reads the block response instead of hitting a connection reset from
// unread request bytes.
func drainAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, blockDrainLimit))
	_ = body.Close()
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
