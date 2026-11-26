//go:build !integration

package session

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"software.sslmate.com/src/go-pkcs12"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// Not parallel: it clears GITLAB_CI.
func TestServeAppliesUnreachableFallbackWhenGitLabReturns502(t *testing.T) {
	t.Setenv("GITLAB_CI", "")
	expiresAt := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	gl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/dependency_firewall/enablement") {
			_, _ = fmt.Fprintf(w, `{"unreachable_fallback":"warn","expires_at":%q}`, expiresAt)
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(gl.Close)
	client, err := gitlab.NewClient("token", gitlab.WithBaseURL(gl.URL+"/api/v4"), gitlab.WithCustomRetryMax(0))
	require.NoError(t, err)

	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "tarball")
	}))
	t.Cleanup(upstream.Close)
	upstreamCA := filepath.Join(t.TempDir(), "upstream-ca.pem")
	require.NoError(t, os.WriteFile(upstreamCA, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upstream.Certificate().Raw}), 0o600))

	baseDir, stateDir := t.TempDir(), t.TempDir()
	// The proxy and Serve log from separate goroutines, and the test buffer isn't safe for that.
	ios, _, _, _ := cmdtest.TestIOStreams(iostreams.WithStderr(io.Discard, false))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ServeConfig{
			Client:     client,
			ProjectID:  "group/proj",
			BaseDir:    baseDir,
			StateDir:   stateDir,
			CACertPath: upstreamCA,
			IO:         ios,
		})
	}()
	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(stateDir, AddrFile))
		return err == nil
	}, 10*time.Second, 20*time.Millisecond, "the job-level proxy never reported ready")

	addr, err := os.ReadFile(filepath.Join(stateDir, AddrFile))
	require.NoError(t, err)
	// The proxy's leaf for an IP authority carries a DNS SAN only, so this
	// client skips hostname verification on its own hop to the proxy.
	httpClient := &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(&url.URL{Scheme: "http", Host: string(addr)}),
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}, //nolint:gosec // client -> inspection proxy hop only
	}}
	resp, err := httpClient.Get(upstream.URL + "/left-pad/-/left-pad-1.3.0.tgz")
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	httpClient.CloseIdleConnections()

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("Serve did not stop after its context was cancelled")
	}

	assert.Equal(t, http.StatusOK, resp.StatusCode, "a warn fallback lets the package through")
}

func TestServeTrustsTheJVMTrustStoreMavenAndGradleWouldLoad(t *testing.T) {
	registryCert, registryCA := selfSignedServer(t)
	registry := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	registry.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{registryCert}}
	registry.StartTLS()
	t.Cleanup(registry.Close)
	_, otherCA := selfSignedServer(t)

	dir := t.TempDir()
	store := writeTrustStore(t, filepath.Join(dir, "registry.p12"), "s3cret", registryCA)
	other := writeTrustStore(t, filepath.Join(dir, "other.p12"), "s3cret", otherCA)
	jks := filepath.Join(dir, "store.jks")
	require.NoError(t, os.WriteFile(jks, []byte{0xfe, 0xed, 0xfe, 0xed, 0, 0, 0, 2}, 0o600))
	javaHome := filepath.Join(dir, "jdk")
	writeTrustStore(t, filepath.Join(javaHome, "lib", "security", "cacerts"), "changeit", registryCA)
	opts := func(path string) string {
		return "-Djavax.net.ssl.trustStore=" + path + " -Djavax.net.ssl.trustStorePassword=s3cret"
	}
	mvnConfig := func(file, content string) func(*testing.T, string) {
		return func(t *testing.T, project string) {
			t.Helper()
			writeTrustStore(t, filepath.Join(project, "registry.p12"), "s3cret", registryCA)
			require.NoError(t, os.MkdirAll(filepath.Join(project, ".mvn"), 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(project, ".mvn", file), []byte(content), 0o600))
		}
	}

	cases := []struct {
		name    string
		env     map[string]string
		project func(t *testing.T, dir string)
		wantOK  bool
		wantLog string
	}{
		{name: "MAVEN_OPTS", env: map[string]string{"MAVEN_OPTS": opts(store)}, wantOK: true},
		{name: "GRADLE_OPTS", env: map[string]string{"GRADLE_OPTS": opts(store)}, wantOK: true},
		{name: "Maven and Gradle name different stores", env: map[string]string{"MAVEN_OPTS": opts(other), "GRADLE_OPTS": opts(store)}, wantOK: true},
		{name: "the JDK default cacerts", env: map[string]string{"JAVA_HOME": javaHome}, wantOK: true},
		{name: ".mvn/jvm.config in the project directory", project: mvnConfig("jvm.config", "-Djavax.net.ssl.trustStore=registry.p12 -Djavax.net.ssl.trustStorePassword=s3cret\n"), wantOK: true},
		{name: ".mvn/maven.config in the project directory", project: mvnConfig("maven.config", "-Djavax.net.ssl.trustStore=registry.p12\n-Djavax.net.ssl.trustStorePassword=s3cret\n"), wantOK: true},
		{name: "no JVM truststore"},
		{name: "a store glab cannot read", env: map[string]string{"MAVEN_OPTS": opts(jks)}, wantLog: "warning: registry connections do not trust JVM truststore"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, v := range []string{
				"GITLAB_CI", "HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy",
				"JAVA_HOME", "MAVEN_OPTS", "MAVEN_DEBUG_OPTS", "MAVEN_ARGS", "MAVEN_BASEDIR",
				"JAVA_TOOL_OPTIONS", "JDK_JAVA_OPTIONS", "_JAVA_OPTIONS", "GRADLE_OPTS", "JAVA_OPTS",
			} {
				t.Setenv(v, "")
			}
			t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
			for k, v := range c.env {
				t.Setenv(k, v)
			}

			project := t.TempDir()
			if c.project != nil {
				c.project(t, project)
			}
			p := startServe(t, project)
			resp, err := p.client.Get(registry.URL + "/ping")
			if err == nil {
				_ = resp.Body.Close()
			}
			log := p.stop()

			if c.wantOK {
				require.NoError(t, err)
				assert.Equal(t, http.StatusOK, resp.StatusCode)
			} else {
				require.Error(t, err, "an untrusted registry must not be fetched")
				assert.Contains(t, log, "x509: certificate signed by unknown authority")
			}
			if c.wantLog != "" {
				assert.Contains(t, log, c.wantLog)
			}
		})
	}
}

type servedProxy struct {
	client *http.Client
	stop   func() string
}

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

func startServe(t *testing.T, baseDir string) servedProxy {
	t.Helper()
	stateDir := t.TempDir()
	stderr := &lockedBuffer{}
	ios, _, _, _ := cmdtest.TestIOStreams(iostreams.WithStderr(stderr, false))
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ServeConfig{ProjectID: "group/proj", BaseDir: baseDir, StateDir: stateDir, IO: ios})
	}()

	deadline := time.Now().Add(10 * time.Second)
	var addr []byte
	for len(addr) == 0 {
		addr, _ = os.ReadFile(filepath.Join(stateDir, AddrFile))
		select {
		case err := <-done:
			t.Fatalf("Serve returned before it was ready: %v\n%s", err, stderr.String())
		case <-time.After(20 * time.Millisecond):
		}
		require.True(t, time.Now().Before(deadline), "Serve did not become ready")
	}
	proxyURL, err := url.Parse("http://" + strings.TrimSpace(string(addr)))
	require.NoError(t, err)
	transport := &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}, //nolint:gosec // client -> inspection proxy hop only; glab verifies the registry
	}

	return servedProxy{
		client: &http.Client{Transport: transport, Timeout: 10 * time.Second},
		stop: func() string {
			transport.CloseIdleConnections()
			cancel()
			require.NoError(t, <-done)
			return stderr.String()
		},
	}
}

func selfSignedServer(t *testing.T) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "test registry"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: cert}, cert
}

func writeTrustStore(t *testing.T, path, password string, certs ...*x509.Certificate) string {
	t.Helper()
	der, err := pkcs12.Modern.EncodeTrustStore(certs, password)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, der, 0o600))
	return path
}
