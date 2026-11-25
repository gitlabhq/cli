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

	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

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
