//go:build !integration

package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
)

type stubChecker struct {
	result policy.Result
	err    error
}

func (s stubChecker) Check(context.Context, policy.Request) (policy.Result, error) {
	return s.result, s.err
}

type stubMatcher struct{}

func (stubMatcher) Match(req *http.Request) Match {
	if req.Method == http.MethodGet && len(req.URL.Path) > 4 && req.URL.Path[len(req.URL.Path)-4:] == ".tgz" {
		return Match{
			Matched: true, Pass: true, Operation: policy.Download,
			Coordinate: policy.Coordinate{Ecosystem: "npm", Name: "left-pad", Version: "1.3.0"},
		}
	}
	return Match{}
}

// uploadMatcher matches a PUT as an npm upload and reads+restores the body,
// exercising the peek-and-restore path end to end.
type uploadMatcher struct{}

func (uploadMatcher) Match(req *http.Request) Match {
	if req.Method != http.MethodPut {
		return Match{}
	}
	original := req.Body
	peek, _ := io.ReadAll(io.LimitReader(original, 1<<20))
	req.Body = bodyWithPrefix(peek, original)
	return Match{
		Matched: true, Pass: true, Operation: policy.Upload,
		Coordinate: policy.Coordinate{Ecosystem: "npm", Name: "left-pad", Version: "9.9.9"},
	}
}

func newClient(t *testing.T, p *Proxy) *http.Client {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(p.CACertificate())
	proxyURL, _ := url.Parse("http://" + p.Addr())
	return &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
		// The proxy mints a MITM leaf for the 127.0.0.1 authority whose SAN is
		// a DNS name, not an IP, so IP-literal verification against the CA pool
		// cannot succeed. Trust the CA but skip hostname verification for the
		// client -> proxy leg; upstream verification is still enforced via
		// WithUpstreamRootCAs.
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}, //nolint:gosec // test client trusts proxy's MITM leaf certs
	}}
}

func startProxy(t *testing.T, upstream *httptest.Server, checker policy.Checker) *Proxy {
	t.Helper()
	return startProxyWithMatcher(t, upstream, stubMatcher{}, checker)
}

func startProxyWithMatcher(t *testing.T, upstream *httptest.Server, matcher Matcher, checker policy.Checker) *Proxy {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(upstream.Certificate())
	p, err := New(matcher, checker, "group/proj", WithUpstreamRootCAs(pool))
	require.NoError(t, err)
	require.NoError(t, p.Start())
	t.Cleanup(p.Stop)
	return p
}

func TestProxyAllowInjectsAllowedHeaderAndProxies(t *testing.T) {
	t.Parallel()
	var gotHeader string
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Gitlab-Dependency-Firewall")
		_, _ = io.WriteString(w, "TARBALL")
	}))
	defer upstream.Close()

	p := startProxy(t, upstream, stubChecker{})
	client := newClient(t, p)

	resp, err := client.Get(upstream.URL + "/left-pad/-/left-pad-1.3.0.tgz")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, "TARBALL", string(body))
	assert.Equal(t, "allowed", gotHeader)
}

func TestProxyPassThroughInjectsAllowed(t *testing.T) {
	t.Parallel()
	var gotHeader string
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Gitlab-Dependency-Firewall")
		_, _ = io.WriteString(w, "META")
	}))
	defer upstream.Close()

	p := startProxy(t, upstream, stubChecker{})
	client := newClient(t, p)

	resp, err := client.Get(upstream.URL + "/left-pad")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "allowed", gotHeader)
}

func TestProxyBlockReturns403AndSkipsUpstream(t *testing.T) {
	t.Parallel()
	upstreamHit := false
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit = true
	}))
	defer upstream.Close()

	p := startProxy(t, upstream, stubChecker{result: policy.Result{Verdict: verdict.Blocked, Reason: "nope"}})
	client := newClient(t, p)

	resp, err := client.Get(upstream.URL + "/left-pad/-/left-pad-1.3.0.tgz")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, "blocked", resp.Header.Get("X-Gitlab-Dependency-Firewall"))
	assert.False(t, upstreamHit, "upstream must not be contacted on block")

	verdicts := p.Verdicts()
	require.Len(t, verdicts, 1)
	assert.Equal(t, verdict.Blocked, verdicts[0].Verdict)
}

func TestProxyFailsClosedOnCheckerError(t *testing.T) {
	t.Parallel()
	upstreamHit := false
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit = true
	}))
	defer upstream.Close()

	p := startProxy(t, upstream, stubChecker{err: errors.New("api down")})
	client := newClient(t, p)

	resp, err := client.Get(upstream.URL + "/left-pad/-/left-pad-1.3.0.tgz")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "checker error must fail closed")
	assert.Equal(t, "blocked", resp.Header.Get("X-Gitlab-Dependency-Firewall"))
	assert.False(t, upstreamHit, "upstream must not be contacted when the checker errors")

	verdicts := p.Verdicts()
	require.Len(t, verdicts, 1)
	assert.Equal(t, verdict.Blocked, verdicts[0].Verdict)
	assert.Contains(t, verdicts[0].Reason, "policy check failed")
}

func TestProxyWarningRecordsAndPassesThrough(t *testing.T) {
	t.Parallel()
	var gotHeader string
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Gitlab-Dependency-Firewall")
		_, _ = io.WriteString(w, "TARBALL")
	}))
	defer upstream.Close()

	p := startProxy(t, upstream, stubChecker{result: policy.Result{Verdict: verdict.Warning, Reason: "risky"}})
	client := newClient(t, p)

	resp, err := client.Get(upstream.URL + "/left-pad/-/left-pad-1.3.0.tgz")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, "TARBALL", string(body), "a warning still proxies the artifact")
	assert.Equal(t, "allowed", gotHeader)

	verdicts := p.Verdicts()
	require.Len(t, verdicts, 1)
	assert.Equal(t, verdict.Warning, verdicts[0].Verdict)
	assert.Zero(t, verdicts[0].Status, "a warning's status is unknown before the upstream round trip and must be left unset")
}

// TestProxyBlockedUploadWithLargeBodyReadsResponse proves the client still
// reads the synthesized 403 when it is streaming a sizable request body: the
// proxy drains the unread body after writing the block so the client's write
// completes and its response read does not hit a connection reset.
func TestProxyBlockedUploadWithLargeBodyReadsResponse(t *testing.T) {
	t.Parallel()
	upstreamHit := false
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHit = true
	}))
	defer upstream.Close()

	p := startProxyWithMatcher(t, upstream, blockMatcher{}, stubChecker{err: errors.New("must not be called")})
	client := newClient(t, p)

	body := strings.Repeat("a", 512<<10) // 512 KiB, within blockDrainLimit
	req, err := http.NewRequest(http.MethodPut, upstream.URL+"/left-pad", strings.NewReader(body))
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "client must be able to read the block body")
	assert.NotEmpty(t, respBody)
	assert.False(t, upstreamHit, "upstream must not be contacted on a fail-closed block")
}

func TestProxyUploadBodyReachesUpstreamIntact(t *testing.T) {
	t.Parallel()
	const payload = `{"name":"left-pad","versions":{"9.9.9":{}}}`
	var gotBody, gotHeader string
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Gitlab-Dependency-Firewall")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusCreated)
	}))
	defer upstream.Close()

	p := startProxyWithMatcher(t, upstream, uploadMatcher{}, stubChecker{})
	client := newClient(t, p)

	req, err := http.NewRequest(http.MethodPut, upstream.URL+"/left-pad", strings.NewReader(payload))
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.JSONEq(t, payload, gotBody, "the peeked upload body must reach upstream intact")
	assert.Equal(t, "allowed", gotHeader)
}

// TestProxyForwardsGzippedBody verifies the proxy transparently forwards a
// gzip-encoded response body to the client. Restored baseline coverage
// (previously TestProxyForwardsGzippedBody) adapted to the tunnel model.
func TestProxyForwardsGzippedBody(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		_, _ = gz.Write([]byte(`{"warning":"license review required"}`))
		assert.NoError(t, gz.Close())
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf.Bytes())
	}))
	defer upstream.Close()

	p := startProxy(t, upstream, stubChecker{})
	client := newClient(t, p)

	resp, err := client.Get(upstream.URL + "/left-pad")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "license review required")
}

// TestProxyPreservesUndecompressedContentEncoding guards against the proxy
// stripping Content-Encoding for a payload the client transport did not
// transparently decode. Restored baseline coverage.
func TestProxyPreservesUndecompressedContentEncoding(t *testing.T) {
	t.Parallel()
	payload := []byte("opaque-non-gzip-bytes")
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "br") // brotli — Transport does not auto-decompress
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer upstream.Close()

	p := startProxy(t, upstream, stubChecker{})
	client := newClient(t, p)
	tr, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	tr.DisableCompression = true

	resp, err := client.Get(upstream.URL + "/left-pad")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, "br", resp.Header.Get("Content-Encoding"),
		"proxy must not strip Content-Encoding when it did not decompress the body")
	assert.Equal(t, payload, body, "body must reach the client unchanged (still encoded)")
}

// TestProxyStreamsTarballByteIdentical verifies a large binary download is
// forwarded byte-identical through the tunnel. Restored baseline coverage.
func TestProxyStreamsTarballByteIdentical(t *testing.T) {
	t.Parallel()
	payload := bytes.Repeat([]byte("x"), 3<<20) // 3 MiB tarball
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer upstream.Close()

	p := startProxy(t, upstream, stubChecker{})
	client := newClient(t, p)

	resp, err := client.Get(upstream.URL + "/left-pad/-/left-pad-1.3.0.tgz")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, payload, body, "tarball must be forwarded byte-identical")
}

// TestProxyRejectsUntrustedUpstreamByDefault ensures the proxy verifies the
// upstream registry's certificate. Without WithUpstreamRootCAs, an
// httptest.Server's self-signed cert must not be accepted: this is the
// regression guard against InsecureSkipVerify sneaking back into the
// upstream leg. Restored baseline coverage.
func TestProxyRejectsUntrustedUpstreamByDefault(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	p, err := New(stubMatcher{}, stubChecker{}, "group/proj")
	require.NoError(t, err)
	require.NoError(t, p.Start())
	t.Cleanup(p.Stop)

	client := newClient(t, p)
	resp, err := client.Get(upstream.URL + "/left-pad/-/left-pad-1.3.0.tgz")
	if err == nil {
		resp.Body.Close()
		t.Fatalf("expected request to fail because upstream cert is untrusted, got status %d", resp.StatusCode)
	}
}

// blockMatcher simulates a matcher that recognized an in-scope upload it
// could not inspect (e.g. an over-limit body) and must fail closed.
type blockMatcher struct{}

func (blockMatcher) Match(*http.Request) Match {
	return Match{
		Matched:    true,
		Operation:  policy.Upload,
		Coordinate: policy.Coordinate{Ecosystem: "npm"},
		Reason:     "upload body too large to inspect for dependency firewall policy",
	}
}

// TestProxyBlockMatchFailsClosedWithoutChecker verifies a matched request
// that leaves Pass false is rejected with 403 without contacting upstream or
// the policy checker.
func TestProxyBlockMatchFailsClosedWithoutChecker(t *testing.T) {
	t.Parallel()
	upstreamHit := false
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHit = true
	}))
	defer upstream.Close()

	// A checker that would panic if consulted, proving a non-Pass match
	// short-circuits it.
	p := startProxyWithMatcher(t, upstream, blockMatcher{}, stubChecker{err: errors.New("must not be called")})
	client := newClient(t, p)

	req, err := http.NewRequest(http.MethodPut, upstream.URL+"/left-pad", strings.NewReader("payload"))
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.False(t, upstreamHit, "upstream must not be contacted on a fail-closed block")

	verdicts := p.Verdicts()
	require.Len(t, verdicts, 1)
	assert.Equal(t, verdict.Blocked, verdicts[0].Verdict)
}

// deadlineChecker records whether Check received a context with a deadline.
type deadlineChecker struct{ sawDeadline chan bool }

func (d deadlineChecker) Check(ctx context.Context, _ policy.Request) (policy.Result, error) {
	_, ok := ctx.Deadline()
	select {
	case d.sawDeadline <- ok:
	default:
	}
	return policy.Result{}, nil
}

// TestProxyPolicyCheckHasDeadline verifies the proxy bounds each policy check
// with a context deadline, so a hung backend can't block the tunnel forever.
func TestProxyPolicyCheckHasDeadline(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "TARBALL")
	}))
	defer upstream.Close()

	checker := deadlineChecker{sawDeadline: make(chan bool, 1)}
	p := startProxy(t, upstream, checker)
	client := newClient(t, p)

	resp, err := client.Get(upstream.URL + "/left-pad/-/left-pad-1.3.0.tgz")
	require.NoError(t, err)
	resp.Body.Close()

	select {
	case ok := <-checker.sawDeadline:
		assert.True(t, ok, "proxy must pass a context with a deadline to the policy checker")
	default:
		t.Fatal("policy checker was not invoked")
	}
}
