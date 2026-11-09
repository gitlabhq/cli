//go:build !integration

package proxy

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
)

const plainJarPath = "/repository/public/org/slf4j/slf4j-api/2.0.9/slf4j-api-2.0.9.jar"

// startPlainProxy starts a proxy that a plain-HTTP client reaches with
// absolute-form requests, the way Maven uses its http <proxy> entry.
func startPlainProxy(t *testing.T, checker policy.Checker, opts ...Option) (*Proxy, *http.Client) {
	t.Helper()
	p, err := New(MavenMatcher{}, checker, "group/proj", opts...)
	require.NoError(t, err)
	require.NoError(t, p.Start())
	t.Cleanup(p.Stop)
	proxyURL, err := url.Parse("http://svc:proxy-secret@" + p.Addr())
	require.NoError(t, err)
	return p, &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
}

// recordingUpstream serves a jar and records what reached it.
type recordingUpstream struct {
	hits    atomic.Int32
	mu      sync.Mutex
	headers http.Header
	body    string
}

func (u *recordingUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u.hits.Add(1)
	body, _ := io.ReadAll(r.Body)
	u.mu.Lock()
	u.headers = r.Header.Clone()
	u.body = string(body)
	u.mu.Unlock()
	_, _ = io.WriteString(w, "jar-bytes")
}

// sendPartialUpload sends an absolute-form jar upload over a raw connection
// that declares a large body but sends only its first few bytes, then stalls.
func sendPartialUpload(t *testing.T, p *Proxy, upstream *httptest.Server) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", p.Addr())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	host := upstream.Listener.Addr().String()
	_, err = fmt.Fprintf(conn, "PUT http://%s%s HTTP/1.1\r\nHost: %s\r\nContent-Length: %d\r\n\r\npartial", host, plainJarPath, host, 1<<20)
	require.NoError(t, err)
	return conn
}

// h2cUpgradeRequest builds a GET carrying the h2c upgrade offer that libcurl
// adds to every plain-HTTP request when it may use HTTP/2.
func h2cUpgradeRequest(t *testing.T, target string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Connection", "Upgrade, HTTP2-Settings")
	req.Header.Set("Upgrade", "h2c")
	req.Header.Set("HTTP2-Settings", "AAMAAABkAAQAoAAAAAIAAAAA")
	return req
}

func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// lockedBuffer lets the standard logger write from server goroutines while
// the test reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureStdLog redirects the standard logger, which both dbg and an
// unconfigured ReverseProxy write to, for the rest of the test.
func captureStdLog(t *testing.T) *lockedBuffer {
	t.Helper()
	buf := &lockedBuffer{}
	out, flags := log.Writer(), log.Flags()
	log.SetOutput(buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(out)
		log.SetFlags(flags)
	})
	return buf
}

func TestProxyPlainHTTP(t *testing.T) {
	t.Run("when a rule names the real package behind an http mirror", func(t *testing.T) {
		up := &recordingUpstream{}
		upstream := httptest.NewServer(up)
		t.Cleanup(upstream.Close)
		p, client := startPlainProxy(t, &denyChecker{name: "org.slf4j:slf4j-api"})

		resp, err := client.Get(upstream.URL + plainJarPath)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
		assert.Equal(t, "blocked", resp.Header.Get(firewallHeader))
		assert.True(t, resp.Close, "the 403 closes the connection, so a client mid-upload stops sending")
		assert.Zero(t, up.hits.Load(), "a blocked request must not reach the upstream")
		verdicts := p.Verdicts()
		require.Len(t, verdicts, 1)
		assert.Equal(t, "org.slf4j:slf4j-api", verdicts[0].Package)
		assert.Equal(t, verdict.Blocked, verdicts[0].Verdict)
	})

	t.Run("when the package is allowed", func(t *testing.T) {
		up := &recordingUpstream{}
		upstream := httptest.NewServer(up)
		t.Cleanup(upstream.Close)
		p, client := startPlainProxy(t, verdictChecker{})

		resp, err := client.Get(upstream.URL + plainJarPath)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "jar-bytes", string(body))
		assert.Equal(t, int32(1), up.hits.Load())
		up.mu.Lock()
		defer up.mu.Unlock()
		assert.Equal(t, "allowed", up.headers.Get(firewallHeader))
		assert.Empty(t, up.headers.Get("Proxy-Authorization"), "the client's proxy credentials must not reach the upstream")
		assert.Empty(t, p.Verdicts())
	})

	t.Run("when a warn rule names the real package", func(t *testing.T) {
		upstream := httptest.NewServer(&recordingUpstream{})
		t.Cleanup(upstream.Close)
		p, client := startPlainProxy(t, verdictChecker{"org.slf4j:slf4j-api": {Verdict: verdict.Warning, Reason: "warned"}})

		resp, err := client.Get(upstream.URL + plainJarPath)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		verdicts := p.Verdicts()
		require.Len(t, verdicts, 1)
		assert.Equal(t, verdict.Warning, verdicts[0].Verdict)
		assert.Equal(t, "org.slf4j:slf4j-api", verdicts[0].Package)
	})

	t.Run("when the policy check fails", func(t *testing.T) {
		up := &recordingUpstream{}
		upstream := httptest.NewServer(up)
		t.Cleanup(upstream.Close)
		p, client := startPlainProxy(t, errChecker{on: "org.slf4j:slf4j-api"})

		resp, err := client.Get(upstream.URL + plainJarPath)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusForbidden, resp.StatusCode, "a failed check must fail closed")
		assert.Equal(t, "blocked", resp.Header.Get(firewallHeader))
		assert.Zero(t, up.hits.Load())
		verdicts := p.Verdicts()
		require.Len(t, verdicts, 1)
		assert.Equal(t, verdict.Blocked, verdicts[0].Verdict)
		assert.Contains(t, verdicts[0].Reason, "policy check failed")
	})

	t.Run("when the request is not for a Maven artifact", func(t *testing.T) {
		up := &recordingUpstream{}
		upstream := httptest.NewServer(up)
		t.Cleanup(upstream.Close)
		p, client := startPlainProxy(t, stubChecker{result: policy.Result{Verdict: verdict.Blocked, Reason: "blocks everything"}})

		resp, err := client.Get(upstream.URL + "/repository/public/org/slf4j/slf4j-api/maven-metadata.xml")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, int32(1), up.hits.Load())
		up.mu.Lock()
		defer up.mu.Unlock()
		assert.Equal(t, "allowed", up.headers.Get(firewallHeader))
		assert.Empty(t, p.Verdicts())
	})

	t.Run("when an upload is allowed", func(t *testing.T) {
		up := &recordingUpstream{}
		upstream := httptest.NewServer(up)
		t.Cleanup(upstream.Close)
		_, client := startPlainProxy(t, verdictChecker{})

		req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, upstream.URL+plainJarPath, strings.NewReader("uploaded-jar"))
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		up.mu.Lock()
		defer up.mu.Unlock()
		assert.Equal(t, "uploaded-jar", up.body, "the upload must reach the upstream intact")
	})

	t.Run("when an upload is blocked", func(t *testing.T) {
		up := &recordingUpstream{}
		upstream := httptest.NewServer(up)
		t.Cleanup(upstream.Close)
		p, client := startPlainProxy(t, &denyChecker{name: "org.slf4j:slf4j-api"})

		req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, upstream.URL+plainJarPath, strings.NewReader("uploaded-jar"))
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
		assert.Zero(t, up.hits.Load(), "a blocked upload must not reach the upstream")
		verdicts := p.Verdicts()
		require.Len(t, verdicts, 1)
		assert.Equal(t, verdict.Blocked, verdicts[0].Verdict)
	})

	t.Run("when a blocked upload is still sending its body", func(t *testing.T) {
		up := &recordingUpstream{}
		upstream := httptest.NewServer(up)
		t.Cleanup(upstream.Close)
		p, _ := startPlainProxy(t, &denyChecker{name: "org.slf4j:slf4j-api"})
		conn := sendPartialUpload(t, p, upstream)

		require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		require.NoError(t, err, "the block must reach a client that is still uploading")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
		assert.Zero(t, up.hits.Load())
		assert.Empty(t, resp.TransferEncoding)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err, "the whole 403 must arrive while the upload is still being drained")
		assert.Equal(t, int64(len(body)), resp.ContentLength)
	})

	t.Run("when a blocked upload stalls after the 403", func(t *testing.T) {
		upstream := httptest.NewServer(&recordingUpstream{})
		t.Cleanup(upstream.Close)
		p, _ := startPlainProxy(t, &denyChecker{name: "org.slf4j:slf4j-api"})
		conn := sendPartialUpload(t, p, upstream)

		require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
		br := bufio.NewReader(conn)
		resp, err := http.ReadResponse(br, nil)
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)

		require.NoError(t, conn.SetReadDeadline(time.Now().Add(plainBlockDrainTimeout+2*time.Second)))
		_, err = io.ReadAll(br)
		assert.NotErrorIs(t, err, os.ErrDeadlineExceeded, "the proxy must stop draining a stalled upload well before Stop's timeout")
	})

	t.Run("when a blocked upload is written in full before the response is read", func(t *testing.T) {
		up := &recordingUpstream{}
		upstream := httptest.NewServer(up)
		t.Cleanup(upstream.Close)
		p, _ := startPlainProxy(t, &denyChecker{name: "org.slf4j:slf4j-api"})
		addr, err := net.ResolveTCPAddr("tcp", p.Addr())
		require.NoError(t, err)
		conn, err := net.DialTCP("tcp", nil, addr)
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		// Left to auto-tune, the send buffer can absorb the whole body unread
		// and hide the reset.
		require.NoError(t, conn.SetWriteBuffer(64<<10))

		host := upstream.Listener.Addr().String()
		body := bytes.Repeat([]byte("x"), 1<<20)
		written := make(chan error, 1)
		go func() {
			_, err := fmt.Fprintf(conn, "PUT http://%s%s HTTP/1.1\r\nHost: %s\r\nContent-Length: %d\r\n\r\n", host, plainJarPath, host, len(body))
			if err == nil {
				_, err = conn.Write(body)
			}
			written <- err
		}()
		select {
		case err := <-written:
			require.NoError(t, err, "the client must be able to finish sending its body")
		case <-time.After(5 * time.Second):
			t.Fatal("timed out sending the upload")
		}

		require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		require.NoError(t, err, "the block must reach a client that reads only after sending its whole body")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
		assert.Zero(t, up.hits.Load())
	})

	t.Run("when the client sends forwarding headers", func(t *testing.T) {
		up := &recordingUpstream{}
		upstream := httptest.NewServer(up)
		t.Cleanup(upstream.Close)
		_, client := startPlainProxy(t, verdictChecker{})

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, upstream.URL+plainJarPath, http.NoBody)
		require.NoError(t, err)
		req.Header.Set("X-Forwarded-For", "203.0.113.7")
		req.Header.Set("Forwarded", "for=203.0.113.7")
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		up.mu.Lock()
		defer up.mu.Unlock()
		assert.Empty(t, up.headers.Values("X-Forwarded-For"), "neither the client's nor the proxy's address may reach the upstream")
		assert.Empty(t, up.headers.Values("Forwarded"))
	})

	t.Run("when the request offers an h2c upgrade", func(t *testing.T) {
		up := &recordingUpstream{}
		upstream := httptest.NewServer(up)
		t.Cleanup(upstream.Close)
		p, client := startPlainProxy(t, verdictChecker{})

		resp, err := client.Do(h2cUpgradeRequest(t, upstream.URL+plainJarPath))
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode, "libcurl offers h2c on every request, so the offer must not be refused")
		assert.Equal(t, int32(1), up.hits.Load())
		up.mu.Lock()
		defer up.mu.Unlock()
		assert.Empty(t, up.headers.Values("Upgrade"), "the upstream must not be asked to switch protocols")
		assert.Empty(t, p.Verdicts())
	})

	t.Run("when a blocked request offers an h2c upgrade", func(t *testing.T) {
		up := &recordingUpstream{}
		upstream := httptest.NewServer(up)
		t.Cleanup(upstream.Close)
		p, client := startPlainProxy(t, &denyChecker{name: "org.slf4j:slf4j-api"})

		resp, err := client.Do(h2cUpgradeRequest(t, upstream.URL+plainJarPath))
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
		assert.Zero(t, up.hits.Load())
		verdicts := p.Verdicts()
		require.Len(t, verdicts, 1)
		assert.Equal(t, verdict.Blocked, verdicts[0].Verdict)
	})

	t.Run("when the upstream switches protocols", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		t.Cleanup(func() { _ = ln.Close() })
		afterSwitch := make(chan string, 1)
		upstreamConn := make(chan net.Conn, 1)
		go func() {
			conn, err := ln.Accept()
			if err != nil {
				afterSwitch <- ""
				return
			}
			upstreamConn <- conn
			br := bufio.NewReader(conn)
			if _, err := http.ReadRequest(br); err == nil {
				_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
			}
			rest, _ := io.ReadAll(br)
			afterSwitch <- string(rest)
		}()
		p, _ := startPlainProxy(t, verdictChecker{})
		conn, err := net.Dial("tcp", p.Addr())
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })

		host := ln.Addr().String()
		_, err = fmt.Fprintf(conn, "GET http://%s%s HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n", host, plainJarPath, host)
		require.NoError(t, err)
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
		br := bufio.NewReader(conn)
		resp, err := http.ReadResponse(br, nil)
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusBadGateway, resp.StatusCode)

		_, err = io.WriteString(conn, "GET /after-switch HTTP/1.1\r\nHost: probe\r\n\r\n")
		require.NoError(t, err)
		if resp, err := http.ReadResponse(br, nil); assert.NoError(t, err, "the proxy itself must answer the next request") {
			resp.Body.Close()
			assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
		}

		_ = conn.Close()
		_ = ln.Close()
		select {
		case c := <-upstreamConn:
			_ = c.Close()
		default:
		}
		assert.NotContains(t, <-afterSwitch, "/after-switch", "bytes after a 101 must not be spliced to the upstream")
	})

	t.Run("when the upstream switches protocols without an upgrade offer", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		t.Cleanup(func() { _ = ln.Close() })
		closed := make(chan error, 1)
		go func() {
			conn, err := ln.Accept()
			if err != nil {
				closed <- err
				return
			}
			defer conn.Close()
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			br := bufio.NewReader(conn)
			if _, err := http.ReadRequest(br); err != nil {
				closed <- err
				return
			}
			_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
			_, err = br.ReadByte()
			closed <- err
		}()
		warnings := &warningRecorder{}
		_, client := startPlainProxy(t, verdictChecker{}, WithUpstreamFailureWarning(warnings.Logf))

		resp, err := client.Get("http://" + ln.Addr().String() + plainJarPath)
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusBadGateway, resp.StatusCode)

		select {
		case err := <-closed:
			require.ErrorIs(t, err, io.EOF, "the proxy must close the upstream connection it refused to switch")
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for the upstream connection to close")
		}
		assert.Equal(t, []string{
			fmt.Sprintf("warning: dependency firewall request to registry %s failed: %v\n", ln.Addr(), errUnpromptedSwitch),
		}, warnings.Lines())
	})

	t.Run("when an outbound proxy is configured", func(t *testing.T) {
		up := &recordingUpstream{}
		upstream := httptest.NewServer(up)
		t.Cleanup(upstream.Close)
		outbound := &recordingUpstream{}
		outboundServer := httptest.NewServer(outbound)
		t.Cleanup(outboundServer.Close)
		outboundURL, err := url.Parse(outboundServer.URL)
		require.NoError(t, err)
		_, client := startPlainProxy(t, verdictChecker{}, WithUpstreamProxy(http.ProxyURL(outboundURL)))

		resp, err := client.Get(upstream.URL + plainJarPath)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, int32(1), outbound.hits.Load(), "the request must go through the outbound proxy")
		assert.Zero(t, up.hits.Load(), "the request must not bypass the outbound proxy")
	})

	t.Run("when the upstream is unreachable", func(t *testing.T) {
		upstream := httptest.NewServer(&recordingUpstream{})
		dead := upstream.URL
		upstream.Close()
		var warnings []string
		var mu sync.Mutex
		_, client := startPlainProxy(t, verdictChecker{}, WithUpstreamFailureWarning(func(format string, args ...any) {
			mu.Lock()
			defer mu.Unlock()
			warnings = append(warnings, fmt.Sprintf(format, args...))
		}))

		for range 2 {
			resp, err := client.Get(dead + plainJarPath)
			require.NoError(t, err)
			resp.Body.Close()
			assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
		}

		mu.Lock()
		defer mu.Unlock()
		assert.Len(t, warnings, 1, "each registry is reported once")
	})

	t.Run("when the client aborts its upload", func(t *testing.T) {
		arrived := make(chan struct{}, 1)
		upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			arrived <- struct{}{}
			_, _ = io.Copy(io.Discard, r.Body)
		}))
		t.Cleanup(upstream.Close)
		warnings := &warningRecorder{}
		p, client := startPlainProxy(t, verdictChecker{}, WithUpstreamFailureWarning(warnings.Logf))

		body, bodyWriter := io.Pipe()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, upstream.URL+plainJarPath, body)
		require.NoError(t, err)
		req.ContentLength = 1 << 20
		done := make(chan error, 1)
		go func() {
			resp, err := client.Do(req)
			if err == nil {
				resp.Body.Close()
			}
			done <- err
		}()
		_, err = bodyWriter.Write([]byte("partial upload"))
		require.NoError(t, err)
		waitFor(t, arrived, "the upload to reach the upstream")
		bodyWriter.CloseWithError(errors.New("package manager aborted the upload"))
		require.Error(t, <-done)
		p.Stop()

		assert.Empty(t, warnings.Lines(), "a request the package manager aborted is not a registry failure")
	})

	t.Run("when the upstream drops the connection mid-response", func(t *testing.T) {
		t.Setenv("GLAB_DEBUG", "true")
		logs := captureStdLog(t)
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			conn, buf, err := http.NewResponseController(w).Hijack()
			if err != nil {
				return
			}
			defer conn.Close()
			_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\npartial")
			_ = buf.Flush()
		}))
		t.Cleanup(upstream.Close)
		p, client := startPlainProxy(t, verdictChecker{})

		if resp, err := client.Get(upstream.URL + plainJarPath); err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		p.Stop()

		out := logs.String()
		assert.Contains(t, out, "dependency firewall proxy: httputil: ReverseProxy read error during body copy")
		assert.NotRegexp(t, `(?m)^httputil:`, out, "ReverseProxy must not write to glab's stderr")
	})

	t.Run("when the request is not absolute-form", func(t *testing.T) {
		p, _ := startPlainProxy(t, verdictChecker{})

		resp, err := http.Get("http://" + p.Addr() + plainJarPath)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	})

	t.Run("when Stop runs while the upstream hangs", func(t *testing.T) {
		reached := make(chan struct{})
		release := make(chan struct{})
		upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			close(reached)
			<-release
		}))
		t.Cleanup(upstream.Close)
		p, client := startPlainProxy(t, verdictChecker{})
		// Registered after startPlainProxy so it runs before its p.Stop, which
		// would otherwise wait on this handler if the code under test regressed.
		t.Cleanup(func() { close(release) })

		go func() {
			if resp, err := client.Get(upstream.URL + plainJarPath); err == nil {
				resp.Body.Close()
			}
		}()
		waitFor(t, reached, "the request to reach the upstream")

		stopped := make(chan struct{})
		go func() {
			p.Stop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			t.Fatal("Stop must not wait on a hung plain-HTTP upstream")
		}
	})

	t.Run("when Stop runs while a client stalls mid-upload", func(t *testing.T) {
		reached := make(chan struct{}, 1)
		upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			reached <- struct{}{}
			_, _ = io.Copy(io.Discard, r.Body)
		}))
		t.Cleanup(upstream.Close)
		p, _ := startPlainProxy(t, verdictChecker{})
		conn := sendPartialUpload(t, p, upstream)
		waitFor(t, reached, "the upload to reach the upstream")

		stopped := make(chan struct{})
		go func() {
			p.Stop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-time.After(tunnelDrainTimeout + 2*time.Second):
			t.Fatal("Stop must not wait on a client that stalls mid-upload")
		}
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
		_, err := io.ReadAll(conn)
		assert.NotErrorIs(t, err, os.ErrDeadlineExceeded, "Stop must close a stalled client's connection")
	})
}

// ctxWaitChecker holds each check until its context ends, and reports when
// the first check starts and ends.
type ctxWaitChecker struct {
	started, ended     chan struct{}
	startOnce, endOnce sync.Once
}

func (c *ctxWaitChecker) Check(ctx context.Context, _ policy.Request) (policy.Result, error) {
	c.startOnce.Do(func() { close(c.started) })
	<-ctx.Done()
	c.endOnce.Do(func() { close(c.ended) })
	return policy.Result{}, ctx.Err()
}

func TestProxyPlainHTTPClientGoesAway(t *testing.T) {
	upstream := httptest.NewServer(&recordingUpstream{})
	t.Cleanup(upstream.Close)
	checker := &ctxWaitChecker{started: make(chan struct{}), ended: make(chan struct{})}
	_, client := startPlainProxy(t, checker)
	ctx, cancel := context.WithCancel(t.Context())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream.URL+plainJarPath, http.NoBody)
	require.NoError(t, err)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if resp, err := client.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}()

	waitFor(t, checker.started, "the policy check to start")
	cancel()

	waitFor(t, checker.ended, "the policy check to end when the client goes away")
	waitFor(t, done, "the client to return")
}

// TestProxyFailsClosedOnUnclearedMatch pins admit's guard for a request the
// matcher recognized but couldn't clear. The checker allows everything, so
// only the guard can block.
func TestProxyFailsClosedOnUnclearedMatch(t *testing.T) {
	// The file names the artifact but not the directory's version.
	const path = "/repository/public/org/example/foo/1/foo-10.jar"

	t.Run("when the request is plain HTTP", func(t *testing.T) {
		up := &recordingUpstream{}
		upstream := httptest.NewServer(up)
		t.Cleanup(upstream.Close)
		p, client := startPlainProxy(t, verdictChecker{})

		resp, err := client.Get(upstream.URL + path)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
		assert.Zero(t, up.hits.Load())
		verdicts := p.Verdicts()
		require.Len(t, verdicts, 1)
		assert.Equal(t, verdict.Blocked, verdicts[0].Verdict)
	})

	t.Run("when the request is tunnelled", func(t *testing.T) {
		up := &recordingUpstream{}
		upstream := httptest.NewTLSServer(up)
		t.Cleanup(upstream.Close)
		p := startProxyWithMatcher(t, upstream, MavenMatcher{}, verdictChecker{})

		resp, err := newClient(t, p).Get(upstream.URL + path)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
		assert.Zero(t, up.hits.Load())
		verdicts := p.Verdicts()
		require.Len(t, verdicts, 1)
		assert.Equal(t, verdict.Blocked, verdicts[0].Verdict)
	})
}

func TestProxyRefusesOtherAbsoluteSchemes(t *testing.T) {
	for _, scheme := range []string{"https", "ftp"} {
		t.Run("when the request names an "+scheme+" URL", func(t *testing.T) {
			up := &recordingUpstream{}
			upstream := httptest.NewServer(up)
			t.Cleanup(upstream.Close)
			p, _ := startPlainProxy(t, verdictChecker{})
			conn, err := net.Dial("tcp", p.Addr())
			require.NoError(t, err)
			t.Cleanup(func() { _ = conn.Close() })
			host := upstream.Listener.Addr().String()

			_, err = fmt.Fprintf(conn, "GET %s://%s%s HTTP/1.1\r\nHost: %s\r\n\r\n", scheme, host, plainJarPath, host)
			require.NoError(t, err)
			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode, "only http:// is served as a plain request")
			assert.Zero(t, up.hits.Load())
		})
	}
}

// TestProxyPlainHTTPOutboundProxyFromEnvironment reads the environment, as
// production does. Loopback is never proxied, so the registry is a name, and
// no assertion depends on whether that name resolves.
func TestProxyPlainHTTPOutboundProxyFromEnvironment(t *testing.T) {
	const registry = "http://registry.example"
	outbound := func(t *testing.T) (*recordingUpstream, string) {
		t.Helper()
		rec := &recordingUpstream{}
		srv := httptest.NewServer(rec)
		t.Cleanup(srv.Close)
		return rec, srv.URL
	}
	setProxyEnv := func(t *testing.T, httpsProxy, httpProxy, noProxy string) {
		t.Helper()
		for _, kv := range [][2]string{{"HTTPS_PROXY", httpsProxy}, {"HTTP_PROXY", httpProxy}, {"NO_PROXY", noProxy}} {
			t.Setenv(kv[0], kv[1])
			t.Setenv(strings.ToLower(kv[0]), kv[1])
		}
	}
	get := func(t *testing.T) int {
		t.Helper()
		_, client := startPlainProxy(t, verdictChecker{})
		resp, err := client.Get(registry + plainJarPath)
		require.NoError(t, err)
		defer resp.Body.Close()
		return resp.StatusCode
	}

	t.Run("when only HTTPS_PROXY is set", func(t *testing.T) {
		via, viaURL := outbound(t)
		setProxyEnv(t, viaURL, "", "")

		assert.Equal(t, http.StatusOK, get(t))
		assert.Equal(t, int32(1), via.hits.Load(), "HTTPS_PROXY is the outbound proxy glab documents")
	})

	t.Run("when HTTP_PROXY is set too", func(t *testing.T) {
		viaHTTPS, httpsURL := outbound(t)
		viaHTTP, httpURL := outbound(t)
		setProxyEnv(t, httpsURL, httpURL, "")

		assert.Equal(t, http.StatusOK, get(t))
		assert.Equal(t, int32(1), viaHTTP.hits.Load(), "HTTP_PROXY takes plain-HTTP requests when it's set")
		assert.Zero(t, viaHTTPS.hits.Load())
	})

	t.Run("when NO_PROXY names the registry", func(t *testing.T) {
		via, viaURL := outbound(t)
		setProxyEnv(t, viaURL, "", "registry.example")
		_, client := startPlainProxy(t, verdictChecker{})
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, registry+plainJarPath, http.NoBody)
		require.NoError(t, err)
		if resp, err := client.Do(req); err == nil {
			resp.Body.Close()
		}

		assert.Zero(t, via.hits.Load(), "a registry in NO_PROXY is reached directly, not through the outbound proxy")
	})
}
