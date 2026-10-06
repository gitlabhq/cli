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

	// tunnels tracks in-flight hijacked MITM tunnel goroutines. http.Server
	// Shutdown does not wait for hijacked connections, so Stop drains this
	// (bounded) before the caller reads Verdicts(): a policy check still in
	// flight when the child exits would otherwise record its verdict after the
	// snapshot and a late Block would never reach the CI log.
	tunnels sync.WaitGroup

	mu       sync.Mutex
	verdicts []verdict.Entry
	seen     map[string]struct{}
}

// tunnelDrainTimeout bounds how long Stop waits for in-flight MITM tunnels to
// finish after the proxy context is cancelled. A hung upstream or a client
// that will not close should not block shutdown forever; the cancelled context
// already unblocks policy checks, so a well-behaved tunnel exits well within
// this window.
const tunnelDrainTimeout = 5 * time.Second

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
	// Shutdown does not wait for hijacked MITM tunnels, so drain them here
	// (bounded) before the caller reads Verdicts(). The cancelled context has
	// already unblocked any in-flight policy check, so a well-behaved tunnel
	// records its verdict and returns promptly; the timeout guards against a
	// hung upstream holding shutdown open forever.
	done := make(chan struct{})
	go func() {
		p.tunnels.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(tunnelDrainTimeout):
		dbg.Debugf("dependency firewall proxy: timed out draining in-flight tunnels")
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
	//
	// Track the tunnel so Stop can drain in-flight ones before the caller
	// snapshots Verdicts(): http.Server.Shutdown does not wait for hijacked
	// connections, so without this a policy check completing after the child
	// exits could record a Block that never reaches the CI log.
	p.tunnels.Add(1)
	defer p.tunnels.Done()
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

		if isStreamable(resp) {
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
	checkCtx, cancel := context.WithTimeout(ctx, policy.CheckTimeout)
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

// binaryArtifactExts are the download suffixes that identify a package
// artifact payload across ecosystems. A response for one of these is streamed
// straight to the client rather than buffered for inspection, regardless of
// its Content-Type: some registries serve them with a text or otherwise
// unhelpful type (for example files.pythonhosted.org serves wheels as
// binary/octet-stream and crates.io serves .crate files as application/gzip).
//
// The Maven entries are the binary members of mavenPrimaryExts; ".pom" is
// omitted because it is small XML metadata, not an artifact payload.
var binaryArtifactExts = []string{
	".whl", ".crate", ".tgz", ".tar.gz", ".tar.bz2", ".zip", ".gem",
	".jar", ".war", ".aar", ".nupkg",
}

// binaryContentTypes are the Content-Type prefixes that identify a binary
// artifact payload. These cover the octet-stream and archive types registries
// return for package downloads, including the application/java-archive that
// repo1.maven.org returns for .jar/.war. Metadata/index responses
// (application/json, text/html) are deliberately excluded so they still take
// the buffer-and-rewrite path that fixes up Content-Length after the
// transport's automatic decompression.
var binaryContentTypes = []string{
	"application/octet-stream",
	"binary/octet-stream",
	"application/gzip",
	"application/x-gzip",
	"application/zip",
	"application/java-archive",
}

// isStreamable reports whether resp is a binary package payload that can be
// streamed straight to the client rather than buffered for the
// Content-Length/Content-Encoding fixup. Buffering large artifacts would stall
// the tunnel and can trip a client read-timeout on multi-megabyte downloads,
// so genuine artifacts are streamed; but the streaming path writes the
// response verbatim, so it is only safe when the response is self-delimiting
// and needs no rewrite:
//
//   - Known Content-Length. The proxy strips the client's Accept-Encoding and
//     the transport may transparently decompress the body, leaving
//     ContentLength == -1. Writing an unknown-length body over the keep-alive
//     HTTP/1.1 tunnel gives the client no framing to detect end-of-body, so it
//     stalls until its read timeout. Those responses must take the buffered
//     path, which sets Content-Length.
//   - Not transport-decompressed (resp.Uncompressed == false). When the
//     transport gunzipped the body it also dropped Content-Length and left a
//     now-inaccurate Content-Encoding, both of which only the buffered path
//     repairs.
//
// Real artifacts (wheels, crates, tarballs, gems, jars) are served
// pre-compressed with an accurate Content-Length and are not
// transport-decompressed, so they stream; metadata/index responses and the
// PEP 658 ".whl.metadata" sidecar (served without a length and often gzipped)
// fall through to buffering.
func isStreamable(resp *http.Response) bool {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}
	if resp.ContentLength < 0 || resp.Uncompressed {
		return false
	}
	if !isBinaryArtifact(resp) {
		return false
	}
	return true
}

// isBinaryArtifact reports whether resp looks like a binary package payload,
// by request path suffix or Content-Type. Metadata/index responses
// (application/json, text/html) are deliberately excluded.
func isBinaryArtifact(resp *http.Response) bool {
	path := resp.Request.URL.Path
	for _, ext := range binaryArtifactExts {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	contentType := resp.Header.Get("Content-Type")
	for _, ct := range binaryContentTypes {
		if strings.HasPrefix(contentType, ct) {
			return true
		}
	}
	return false
}
