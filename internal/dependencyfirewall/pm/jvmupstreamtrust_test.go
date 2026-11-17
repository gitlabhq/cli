//go:build !integration

package pm

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"software.sslmate.com/src/go-pkcs12"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func writeTrustStoreAt(t *testing.T, path string, enc *pkcs12.Encoder, password string, certs ...*x509.Certificate) string {
	t.Helper()
	der, err := enc.EncodeTrustStore(certs, password)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, der, 0o600))
	return path
}

func writeTrustStore(t *testing.T, password string, certs ...*x509.Certificate) string {
	t.Helper()
	return writeTrustStoreAt(t, filepath.Join(t.TempDir(), "truststore.p12"), pkcs12.Modern, password, certs...)
}

func trustStoreOpts(path, password string) string {
	return "-Djavax.net.ssl.trustStore=" + path + " -Djavax.net.ssl.trustStorePassword=" + password
}

func TestRunTrustsUserJVMTrustStoreUpstream(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on POSIX temp-dir isolation for the CA bundle")
	}
	// Not httptest's shared certificate: on Linux the process caches the system
	// roots, and an earlier test that sets SSL_CERT_FILE to that certificate
	// would make the npm registry trusted.
	ca := newTestAuthority(t)
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	upstream.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{ca.serverCert(t, net.IPv4(127, 0, 0, 1))}}
	upstream.StartTLS()
	t.Cleanup(upstream.Close)
	opts := trustStoreOpts(writeTrustStore(t, "s3cret", ca.cert), "s3cret")

	cases := []struct {
		name    string
		manager PackageManager
		envVar  string
		wantOK  bool
	}{
		{name: "maven MAVEN_OPTS", manager: Maven(), envVar: "MAVEN_OPTS", wantOK: true},
		{name: "maven JAVA_TOOL_OPTIONS", manager: Maven(), envVar: "JAVA_TOOL_OPTIONS", wantOK: true},
		{name: "gradle GRADLE_OPTS", manager: Gradle(), envVar: "GRADLE_OPTS", wantOK: true},
		{name: "gradle JAVA_OPTS", manager: Gradle(), envVar: "JAVA_OPTS", wantOK: true},
		{name: "npm ignores JVM options", manager: NPM(), envVar: "JAVA_TOOL_OPTIONS", wantOK: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("GITLAB_CI", "")
			t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
			for _, v := range []string{"JAVA_HOME", "MAVEN_OPTS", "MAVEN_DEBUG_OPTS", "MAVEN_BASEDIR", "JAVA_TOOL_OPTIONS", "JDK_JAVA_OPTIONS", "_JAVA_OPTIONS", "GRADLE_OPTS", "JAVA_OPTS"} {
				t.Setenv(v, "")
			}
			t.Setenv(c.envVar, opts)

			ios, _, _, stderr := cmdtest.TestIOStreams()
			fe := &upstreamFetchExecutor{url: upstream.URL + "/ping"}
			err := Run(t.Context(), c.manager, RunOptions{
				IO:        ios,
				Executor:  fe,
				BaseDir:   t.TempDir(),
				ProjectID: "group/proj",
			})
			if c.wantOK {
				assert.Empty(t, stderr.String())
				require.NoError(t, err)
				require.NotNil(t, fe.resp)
				assert.Equal(t, http.StatusOK, fe.resp.StatusCode)
				return
			}
			require.Error(t, err)
			if fe.resp != nil {
				assert.NotEqual(t, http.StatusOK, fe.resp.StatusCode, "an untrusted registry must not be fetched")
			}
		})
	}
}

func TestRunWarnsAboutAConfiguredJVMTrustStoreItCannotUse(t *testing.T) {
	jks := []byte{0xfe, 0xed, 0xfe, 0xed, 0, 0, 0, 2}
	javaHome := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(javaHome, "lib", "security"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(javaHome, "lib", "security", "cacerts"), jks, 0o600))

	cases := []struct {
		name        string
		envVar      string
		value       string
		wantWarning bool
	}{
		{name: "JKS store in MAVEN_OPTS", envVar: "MAVEN_OPTS", value: "-Djavax.net.ssl.trustStore=" + writeTestFile(t, "ts.jks", jks), wantWarning: true},
		{name: "JKS cacerts of the JDK in JAVA_HOME", envVar: "JAVA_HOME", value: javaHome},
		{name: "Windows system store in MAVEN_OPTS", envVar: "MAVEN_OPTS", value: "-Djavax.net.ssl.trustStore=" + writeTestFile(t, "root.bin", []byte("x")) + " -Djavax.net.ssl.trustStoreType=Windows-ROOT"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("GITLAB_CI", "")
			t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
			for _, v := range []string{"JAVA_HOME", "MAVEN_OPTS", "MAVEN_DEBUG_OPTS", "MAVEN_BASEDIR", "JAVA_TOOL_OPTIONS", "JDK_JAVA_OPTIONS", "_JAVA_OPTIONS"} {
				t.Setenv(v, "")
			}
			t.Setenv(c.envVar, c.value)

			ios, _, _, stderr := cmdtest.TestIOStreams()
			err := Run(t.Context(), Maven(), RunOptions{
				IO:        ios,
				Executor:  &fakeExecutor{},
				BaseDir:   t.TempDir(),
				ProjectID: "group/proj",
			})
			require.NoError(t, err)
			if !c.wantWarning {
				assert.Empty(t, stderr.String())
				return
			}
			assert.Contains(t, stderr.String(), "warning: registry connections do not trust JVM truststore")
			assert.Contains(t, stderr.String(), "JKS and JCEKS keystores are not supported")
		})
	}
}

func TestJVMTrustStoreDiagnosticsKeepThePasswordOut(t *testing.T) {
	t.Setenv("GLAB_DEBUG", "true")
	var logged bytes.Buffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	project := t.TempDir()
	writeMavenJVMConfig(t, project, `-Djavax.net.ssl.trustStore="`+project+`/certs #1/ts.p12"`+"\n-Djavax.net.ssl.trustStorePassword=s3cretpw\n")
	cases := []struct {
		name   string
		env    []string
		dir    string
		secret string
	}{
		{name: "quoted path with a # in jvm.config", dir: project, secret: "s3cretpw"},
		{name: "unbalanced quote in MAVEN_OPTS", env: []string{`MAVEN_OPTS=-Djavax.net.ssl.trustStore="/x/missing.p12 -Djavax.net.ssl.trustStorePassword=hunter2`}, secret: "hunter2"},
		{name: "quotes balanced across two options", env: []string{`MAVEN_OPTS=-Djavax.net.ssl.trustStore=/Users/o'neil/ts.p12 -Djavax.net.ssl.trustStorePassword=it's`}, secret: "trustStorePassword=its"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			logged.Reset()
			dir := c.dir
			if dir == "" {
				dir = t.TempDir()
			}
			_, err := (&mavenManager{}).upstreamTrustAnchors(c.env, dir, nil)
			out := logged.String()
			if err != nil {
				out += err.Error()
			}
			assert.NotEmpty(t, logged.String())
			assert.NotContains(t, out, c.secret)
		})
	}
}

func TestJVMTrustStoreOverride(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		env     []string
		wantErr string
	}{
		{name: "no _JAVA_OPTIONS"},
		{name: "_JAVA_OPTIONS without truststore options", env: []string{"_JAVA_OPTIONS=-Xmx1g -Dfoo=bar"}},
		{name: "truststore only in MAVEN_OPTS", env: []string{"MAVEN_OPTS=-Djavax.net.ssl.trustStore=/ts.p12"}},
		{name: "trustStore", env: []string{"_JAVA_OPTIONS=-Xmx1g -Djavax.net.ssl.trustStore=/ts.p12"}, wantErr: "-Djavax.net.ssl.trustStore in _JAVA_OPTIONS"},
		{name: "trustStorePassword", env: []string{"_JAVA_OPTIONS=-Djavax.net.ssl.trustStorePassword=x"}, wantErr: "-Djavax.net.ssl.trustStorePassword in _JAVA_OPTIONS"},
		{name: "JKS trustStoreType", env: []string{"_JAVA_OPTIONS=-Djavax.net.ssl.trustStoreType=Jks"}},
		{name: "PKCS12 trustStoreType", env: []string{"_JAVA_OPTIONS=-Djavax.net.ssl.trustStoreType=Pkcs12"}},
		{name: "empty trustStoreType", env: []string{"_JAVA_OPTIONS=-Djavax.net.ssl.trustStoreType="}, wantErr: "-Djavax.net.ssl.trustStoreType in _JAVA_OPTIONS"},
		{name: "Windows-ROOT trustStoreType", env: []string{"_JAVA_OPTIONS=-Djavax.net.ssl.trustStoreType=Windows-ROOT"}, wantErr: "-Djavax.net.ssl.trustStoreType in _JAVA_OPTIONS"},
		{name: "option without -D", env: []string{"_JAVA_OPTIONS=javax.net.ssl.trustStore=/ts.p12"}},
		{name: "trustStore in MAVEN_DEBUG_OPTS", env: []string{"MAVEN_DEBUG_OPTS=-Djavax.net.ssl.trustStore=/ts.p12"}, wantErr: "-Djavax.net.ssl.trustStore in MAVEN_DEBUG_OPTS"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			err := jvmTrustStoreOverride(c.env, "mvn", "MAVEN_OPTS", "MAVEN_DEBUG_OPTS", "_JAVA_OPTIONS")
			if c.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, c.wantErr)
			assert.ErrorContains(t, err, "mvn cannot trust the dependency firewall proxy; set it in MAVEN_OPTS instead")
		})
	}
}

func TestRunWarnsOncePerJVMTrustStoreProblem(t *testing.T) {
	t.Setenv("GITLAB_CI", "")
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
	for _, v := range []string{"JAVA_HOME", "MAVEN_OPTS", "MAVEN_DEBUG_OPTS", "MAVEN_BASEDIR", "JAVA_TOOL_OPTIONS", "JDK_JAVA_OPTIONS", "JAVA_OPTS"} {
		t.Setenv(v, "")
	}
	t.Setenv("GRADLE_OPTS", "-Djavax.net.ssl.trustStore="+writeTestFile(t, "ts.jks", []byte{0xfe, 0xed, 0xfe, 0xed, 0, 0, 0, 2}))
	t.Setenv("_JAVA_OPTIONS", "-Djavax.net.ssl.trustStorePassword=x")

	ios, _, _, stderr := cmdtest.TestIOStreams()
	err := Run(t.Context(), Gradle(), RunOptions{
		IO:        ios,
		Executor:  &fakeExecutor{},
		BaseDir:   t.TempDir(),
		ProjectID: "group/proj",
	})
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
	require.Len(t, lines, 2)
	assert.Contains(t, lines[0], "warning: registry connections do not trust JVM truststore")
	assert.Contains(t, lines[1], "warning: -Djavax.net.ssl.trustStorePassword in _JAVA_OPTIONS replaces glab's truststore, so gradle cannot trust the dependency firewall proxy; set it in GRADLE_OPTS instead")
}

func TestMavenJVMConfigLogsAReadError(t *testing.T) {
	t.Setenv("GLAB_DEBUG", "true")
	var logged bytes.Buffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	project := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(project, ".mvn", "jvm.config"), 0o700))
	assert.Empty(t, mavenJVMConfig(nil, project, nil))
	assert.Contains(t, logged.String(), "not reading .mvn/jvm.config")
}

func TestJVMUpstreamTrustAnchors(t *testing.T) {
	t.Parallel()
	a, b := newTestAuthority(t).cert, newTestAuthority(t).cert

	type run struct {
		env  []string
		dir  string
		args []string
	}
	cases := []struct {
		name    string
		manager upstreamTrustManager
		setup   func(t *testing.T) run
		want    *x509.Certificate
		wantErr string
	}{
		{
			name:    "quoted path and password with spaces",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				path := writeTrustStoreAt(t, filepath.Join(t.TempDir(), "trust store", "ts.p12"), pkcs12.Modern, "pa ss", a)
				return run{env: []string{`MAVEN_OPTS=-Xmx1g "-Djavax.net.ssl.trustStore=` + path + `" -Djavax.net.ssl.trustStorePassword='pa ss' -Djavax.net.ssl.trustStoreType=PKCS12`}}
			},
			want: a,
		},
		{
			name:    "MAVEN_OPTS overrides JAVA_TOOL_OPTIONS",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				return run{env: []string{
					"MAVEN_OPTS=" + trustStoreOpts(writeTrustStore(t, "x", a), "x"),
					"JAVA_TOOL_OPTIONS=" + trustStoreOpts(writeTrustStore(t, "x", b), "x"),
				}}
			},
			want: a,
		},
		{
			name:    "GRADLE_OPTS overrides JAVA_OPTS",
			manager: &gradleManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				return run{env: []string{
					"JAVA_OPTS=" + trustStoreOpts(writeTrustStore(t, "x", b), "x"),
					"GRADLE_OPTS=" + trustStoreOpts(writeTrustStore(t, "x", a), "x"),
				}}
			},
			want: a,
		},
		{
			name:    "gradle ignores MAVEN_OPTS",
			manager: &gradleManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				return run{env: []string{"MAVEN_OPTS=" + trustStoreOpts(writeTrustStore(t, "x", a), "x")}}
			},
		},
		{
			name:    "relative path resolves against the working directory",
			manager: &gradleManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				dir := t.TempDir()
				writeTrustStoreAt(t, filepath.Join(dir, "certs", "ts.p12"), pkcs12.Modern, "x", a)
				return run{env: []string{"GRADLE_OPTS=" + trustStoreOpts(filepath.Join("certs", "ts.p12"), "x")}, dir: dir}
			},
			want: a,
		},
		{
			name:    "jvm.config in an ancestor directory",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				root := t.TempDir()
				writeTrustStoreAt(t, filepath.Join(root, ".mvn", "ts.p12"), pkcs12.Modern, "x", a)
				writeMavenJVMConfig(t, root, "-Djavax.net.ssl.trustStore=${MAVEN_PROJECTBASEDIR}/.mvn/ts.p12\n-Djavax.net.ssl.trustStorePassword=x\n")
				module := filepath.Join(root, "module")
				require.NoError(t, os.Mkdir(module, 0o700))
				return run{dir: module}
			},
			want: a,
		},
		{
			name:    "MAVEN_OPTS overrides jvm.config",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				root := t.TempDir()
				writeMavenJVMConfig(t, root, trustStoreOpts(writeTrustStore(t, "x", b), "x"))
				return run{env: []string{"MAVEN_OPTS=" + trustStoreOpts(writeTrustStore(t, "x", a), "x")}, dir: root}
			},
			want: a,
		},
		{
			name:    "jvm.config overrides JAVA_TOOL_OPTIONS",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				root := t.TempDir()
				writeMavenJVMConfig(t, root, trustStoreOpts(writeTrustStore(t, "x", a), "x"))
				return run{env: []string{"JAVA_TOOL_OPTIONS=" + trustStoreOpts(writeTrustStore(t, "x", b), "x")}, dir: root}
			},
			want: a,
		},
		{
			name:    "JDK_JAVA_OPTIONS overrides JAVA_TOOL_OPTIONS and loses to jvm.config",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				root := t.TempDir()
				writeMavenJVMConfig(t, root, trustStoreOpts(writeTrustStore(t, "x", a), "x"))
				return run{env: []string{
					"JAVA_TOOL_OPTIONS=" + trustStoreOpts(writeTrustStore(t, "x", b), "x"),
					"JDK_JAVA_OPTIONS=" + trustStoreOpts(writeTrustStore(t, "x", b), "x"),
				}, dir: root}
			},
			want: a,
		},
		{
			name:    "JDK_JAVA_OPTIONS overrides JAVA_TOOL_OPTIONS for maven",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				return run{env: []string{
					"JAVA_TOOL_OPTIONS=" + trustStoreOpts(writeTrustStore(t, "x", b), "x"),
					"JDK_JAVA_OPTIONS=" + trustStoreOpts(writeTrustStore(t, "x", a), "x"),
				}}
			},
			want: a,
		},
		{
			name:    "JDK_JAVA_OPTIONS overrides JAVA_TOOL_OPTIONS for gradle",
			manager: &gradleManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				return run{env: []string{
					"JAVA_TOOL_OPTIONS=" + trustStoreOpts(writeTrustStore(t, "x", b), "x"),
					"JDK_JAVA_OPTIONS=" + trustStoreOpts(writeTrustStore(t, "x", a), "x"),
				}}
			},
			want: a,
		},
		{
			name:    "MAVEN_DEBUG_OPTS overrides MAVEN_OPTS",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				return run{env: []string{
					"MAVEN_OPTS=" + trustStoreOpts(writeTrustStore(t, "x", b), "x"),
					"MAVEN_DEBUG_OPTS=" + trustStoreOpts(writeTrustStore(t, "x", a), "x"),
				}}
			},
			want:    a,
			wantErr: "MAVEN_DEBUG_OPTS replaces glab's truststore",
		},
		{
			name:    "_JAVA_OPTIONS overrides MAVEN_OPTS",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				return run{env: []string{
					"MAVEN_OPTS=" + trustStoreOpts(writeTrustStore(t, "x", b), "x"),
					"_JAVA_OPTIONS=" + trustStoreOpts(writeTrustStore(t, "x", a), "x"),
				}}
			},
			want:    a,
			wantErr: "_JAVA_OPTIONS replaces glab's truststore",
		},
		{
			name:    "_JAVA_OPTIONS overrides GRADLE_OPTS",
			manager: &gradleManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				return run{env: []string{
					"GRADLE_OPTS=" + trustStoreOpts(writeTrustStore(t, "x", b), "x"),
					"_JAVA_OPTIONS=" + trustStoreOpts(writeTrustStore(t, "x", a), "x"),
				}}
			},
			want:    a,
			wantErr: "_JAVA_OPTIONS replaces glab's truststore",
		},
		{
			name:    "JAVA_OPTS overrides JDK_JAVA_OPTIONS for gradle",
			manager: &gradleManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				return run{env: []string{
					"JDK_JAVA_OPTIONS=" + trustStoreOpts(writeTrustStore(t, "x", b), "x"),
					"JAVA_OPTS=" + trustStoreOpts(writeTrustStore(t, "x", a), "x"),
				}}
			},
			want: a,
		},
		{
			name:    "_JAVA_OPTIONS overrides MAVEN_DEBUG_OPTS",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				return run{env: []string{
					"MAVEN_DEBUG_OPTS=" + trustStoreOpts(writeTrustStore(t, "x", b), "x"),
					"_JAVA_OPTIONS=" + trustStoreOpts(writeTrustStore(t, "x", a), "x"),
				}}
			},
			want:    a,
			wantErr: "replaces glab's truststore",
		},
		{
			name:    "MAVEN_BASEDIR selects the jvm.config",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				project, other := t.TempDir(), t.TempDir()
				writeMavenJVMConfig(t, project, trustStoreOpts(writeTrustStore(t, "x", b), "x"))
				writeMavenJVMConfig(t, other, trustStoreOpts(writeTrustStore(t, "x", a), "x"))
				return run{env: []string{"MAVEN_BASEDIR=" + other}, dir: project}
			},
			want: a,
		},
		{
			name:    "jvm.config comments are not read as options",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				root := t.TempDir()
				writeMavenJVMConfig(t, root, trustStoreOpts(writeTrustStore(t, "x", a), "x")+" # -Djavax.net.ssl.trustStorePassword=wrong\n"+
					"# -Djavax.net.ssl.trustStore="+filepath.Join(root, "missing.p12")+"\n")
				return run{dir: root}
			},
			want: a,
		},
		{
			name:    "jvm.config comments are stripped before MAVEN_PROJECTBASEDIR is substituted",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				root := filepath.Join(t.TempDir(), "proj #1")
				writeTrustStoreAt(t, filepath.Join(root, ".mvn", "ts.p12"), pkcs12.Modern, "x", a)
				writeMavenJVMConfig(t, root, `-Djavax.net.ssl.trustStore="${MAVEN_PROJECTBASEDIR}/.mvn/ts.p12" -Djavax.net.ssl.trustStorePassword=x`+"\n")
				return run{dir: root}
			},
			want: a,
		},
		{
			name:    "-f selects the project whose jvm.config applies",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				project := t.TempDir()
				writeMavenJVMConfig(t, project, trustStoreOpts(writeTrustStore(t, "x", a), "x"))
				pom := filepath.Join(project, "pom.xml")
				require.NoError(t, os.WriteFile(pom, nil, 0o600))
				return run{dir: t.TempDir(), args: []string{"-q", "-f", pom, "verify"}}
			},
			want: a,
		},
		{
			name:    "truststore in the mvn arguments",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				return run{args: []string{"-D", "javax.net.ssl.trustStore=" + writeTrustStore(t, "x", a), "--define=javax.net.ssl.trustStorePassword=x", "verify"}}
			},
			want:    a,
			wantErr: "-Djavax.net.ssl.trustStore in the mvn arguments replaces glab's truststore on Maven 3, so mvn cannot trust the dependency firewall proxy there; set it in MAVEN_OPTS instead",
		},
		{
			name:    "truststore in MAVEN_ARGS",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				return run{env: []string{"MAVEN_ARGS=" + trustStoreOpts(writeTrustStore(t, "x", a), "x")}}
			},
			want:    a,
			wantErr: "in the mvn arguments replaces glab's truststore on Maven 3",
		},
		{
			name:    "truststore in maven.config",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				root := t.TempDir()
				writeMavenConfigFile(t, root, "-Djavax.net.ssl.trustStore="+writeTrustStore(t, "x", a)+"\n-Djavax.net.ssl.trustStorePassword=x\n")
				return run{dir: root}
			},
			want:    a,
			wantErr: "in the mvn arguments replaces glab's truststore on Maven 3",
		},
		{
			name:    "MAVEN_ARGS overrides maven.config",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				root := t.TempDir()
				writeMavenConfigFile(t, root, "-Djavax.net.ssl.trustStore="+writeTrustStore(t, "x", b)+"\n-Djavax.net.ssl.trustStorePassword=x\n")
				return run{env: []string{"MAVEN_ARGS=-Djavax.net.ssl.trustStore=" + writeTrustStore(t, "x", a)}, dir: root}
			},
			want:    a,
			wantErr: "in the mvn arguments replaces glab's truststore on Maven 3",
		},
		{
			name:    "the command line overrides MAVEN_ARGS",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				return run{
					env:  []string{"MAVEN_ARGS=" + trustStoreOpts(writeTrustStore(t, "x", b), "x")},
					args: []string{"-Djavax.net.ssl.trustStore=" + writeTrustStore(t, "x", a), "verify"},
				}
			},
			want:    a,
			wantErr: "in the mvn arguments replaces glab's truststore on Maven 3",
		},
		{
			name:    "JVM options override the mvn arguments, as on Maven 4",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				return run{
					env:  []string{"MAVEN_OPTS=" + trustStoreOpts(writeTrustStore(t, "x", a), "x")},
					args: []string{"-Djavax.net.ssl.trustStore=" + writeTrustStore(t, "x", b), "verify"},
				}
			},
			want:    a,
			wantErr: "in the mvn arguments replaces glab's truststore on Maven 3",
		},
		{
			name:    "the mvn arguments set a property the JVM options leave unset",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				return run{
					env:  []string{"MAVEN_OPTS=-Djavax.net.ssl.trustStore=" + writeTrustStore(t, "x", a)},
					args: []string{"-Djavax.net.ssl.trustStorePassword=x", "verify"},
				}
			},
			want:    a,
			wantErr: "-Djavax.net.ssl.trustStorePassword in the mvn arguments",
		},
		{
			name:    "PKCS12 trustStoreType in the mvn arguments keeps glab's truststore",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				return run{
					env:  []string{"MAVEN_OPTS=" + trustStoreOpts(writeTrustStore(t, "x", a), "x")},
					args: []string{"-Djavax.net.ssl.trustStoreType=PKCS12", "verify"},
				}
			},
			want: a,
		},
		{
			name:    "truststore on the gradle command line is reported",
			manager: &gradleManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				return run{
					env:  []string{"GRADLE_OPTS=" + trustStoreOpts(writeTrustStore(t, "x", a), "x")},
					args: []string{"build", "-D", "javax.net.ssl.trustStore=" + writeTrustStore(t, "x", b)},
				}
			},
			want:    a,
			wantErr: "-Djavax.net.ssl.trustStore in the gradle arguments replaces glab's truststore, so gradle cannot trust the dependency firewall proxy; set it in GRADLE_OPTS instead",
		},
		{
			name:    "truststore password set with --system-prop on the gradle command line is reported",
			manager: &gradleManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				return run{
					env:  []string{"GRADLE_OPTS=" + trustStoreOpts(writeTrustStore(t, "x", a), "x")},
					args: []string{"--system-prop=javax.net.ssl.trustStorePassword=y", "build"},
				}
			},
			want:    a,
			wantErr: "-Djavax.net.ssl.trustStorePassword in the gradle arguments",
		},
		{
			name:    "PKCS12 trustStoreType on the gradle command line keeps glab's truststore",
			manager: &gradleManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				return run{
					env:  []string{"GRADLE_OPTS=" + trustStoreOpts(writeTrustStore(t, "x", a), "x")},
					args: []string{"build", "--system-prop", "javax.net.ssl.trustStoreType=pkcs12"},
				}
			},
			want: a,
		},
		{
			name:    "JDK default cacerts protected with changeit",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				javaHome := t.TempDir()
				writeTrustStoreAt(t, filepath.Join(javaHome, "lib", "security", "cacerts"), pkcs12.Modern, "changeit", a)
				return run{env: []string{"JAVA_HOME=" + javaHome}}
			},
			want: a,
		},
		{
			name:    "JDK default cacerts without a MAC",
			manager: &gradleManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				javaHome := t.TempDir()
				writeTrustStoreAt(t, filepath.Join(javaHome, "lib", "security", "cacerts"), pkcs12.Passwordless, "", a)
				return run{env: []string{"JAVA_HOME=" + javaHome}}
			},
			want: a,
		},
		{
			name:    "jssecacerts takes precedence over cacerts",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				javaHome := t.TempDir()
				writeTrustStoreAt(t, filepath.Join(javaHome, "lib", "security", "jssecacerts"), pkcs12.Modern, "changeit", a)
				writeTrustStoreAt(t, filepath.Join(javaHome, "lib", "security", "cacerts"), pkcs12.Modern, "changeit", b)
				return run{env: []string{"JAVA_HOME=" + javaHome}}
			},
			want: a,
		},
		{
			name:    "missing configured store falls back to cacerts, not jssecacerts, as JSSE does",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				javaHome := t.TempDir()
				writeTrustStoreAt(t, filepath.Join(javaHome, "lib", "security", "jssecacerts"), pkcs12.Modern, "changeit", a)
				writeTrustStoreAt(t, filepath.Join(javaHome, "lib", "security", "cacerts"), pkcs12.Modern, "changeit", b)
				return run{env: []string{
					"JAVA_HOME=" + javaHome,
					"MAVEN_OPTS=-Djavax.net.ssl.trustStore=" + filepath.Join(t.TempDir(), "missing.p12"),
				}}
			},
			want: b,
		},
		{
			name:    "empty trustStore option falls back to cacerts, not jssecacerts, as JSSE does",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				javaHome := t.TempDir()
				writeTrustStoreAt(t, filepath.Join(javaHome, "lib", "security", "jssecacerts"), pkcs12.Modern, "changeit", a)
				writeTrustStoreAt(t, filepath.Join(javaHome, "lib", "security", "cacerts"), pkcs12.Modern, "changeit", b)
				return run{env: []string{"JAVA_HOME=" + javaHome, "MAVEN_OPTS=-Djavax.net.ssl.trustStore="}}
			},
			want: b,
		},
		{
			name:    "configured store that is not a regular file falls back to cacerts",
			manager: &gradleManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				javaHome := t.TempDir()
				writeTrustStoreAt(t, filepath.Join(javaHome, "lib", "security", "cacerts"), pkcs12.Modern, "changeit", b)
				return run{env: []string{"JAVA_HOME=" + javaHome, "GRADLE_OPTS=-Djavax.net.ssl.trustStore=" + os.DevNull}}
			},
			want: b,
		},
		{
			name:    "Windows system store adds nothing",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				return run{env: []string{"MAVEN_OPTS=-Djavax.net.ssl.trustStore=" + writeTestFile(t, "root.bin", []byte("x")) + " -Djavax.net.ssl.trustStoreType=Windows-ROOT"}}
			},
		},
		{
			name:    "macOS keychain store adds nothing",
			manager: &gradleManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				return run{env: []string{"GRADLE_OPTS=-Djavax.net.ssl.trustStore=" + writeTestFile(t, "keychain.bin", []byte("x")) + " -Djavax.net.ssl.trustStoreType=KeychainStore"}}
			},
		},
		{
			name:    "store that also holds a private key gets a hint",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				ca := newTestAuthority(t)
				der, err := pkcs12.Modern.Encode(ca.key, ca.cert, nil, "x")
				require.NoError(t, err)
				return run{env: []string{"MAVEN_OPTS=" + trustStoreOpts(writeTestFile(t, "combined.p12", der), "x")}}
			},
			wantErr: "glab reads PKCS#12 truststores that hold only trusted certificates",
		},
		{
			name:    "trustStore=NONE trusts no store and does not fall back to cacerts",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				javaHome := t.TempDir()
				writeTrustStoreAt(t, filepath.Join(javaHome, "lib", "security", "cacerts"), pkcs12.Modern, "changeit", b)
				return run{env: []string{"JAVA_HOME=" + javaHome, "MAVEN_OPTS=-Djavax.net.ssl.trustStore=NONE"}}
			},
		},
		{
			name:    "JKS store is skipped with a warning",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				path := writeTestFile(t, "ts.jks", []byte{0xfe, 0xed, 0xfe, 0xed, 0, 0, 0, 2})
				return run{env: []string{"MAVEN_OPTS=" + trustStoreOpts(path, "changeit") + " -Djavax.net.ssl.trustStoreType=JKS"}}
			},
			wantErr: "JKS and JCEKS keystores are not supported",
		},
		{
			name:    "JKS cacerts of the JDK is skipped without an error",
			manager: &gradleManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				javaHome := t.TempDir()
				require.NoError(t, os.MkdirAll(filepath.Join(javaHome, "lib", "security"), 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(javaHome, "lib", "security", "cacerts"), []byte{0xfe, 0xed, 0xfe, 0xed, 0, 0, 0, 2}, 0o600))
				return run{env: []string{"JAVA_HOME=" + javaHome}}
			},
		},
		{
			name:    "wrong password is skipped with a warning",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				return run{env: []string{"MAVEN_OPTS=" + trustStoreOpts(writeTrustStore(t, "right", a), "wrong")}}
			},
			wantErr: "decryption password incorrect",
		},
		{
			name:    "store with a MAC and no configured password asks for the password",
			manager: &mavenManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				return run{env: []string{"MAVEN_OPTS=-Djavax.net.ssl.trustStore=" + writeTrustStore(t, "x", a)}}
			},
			wantErr: "set -Djavax.net.ssl.trustStorePassword",
		},
		{
			name:    "unsupported store type is skipped with a warning",
			manager: &gradleManager{},
			setup: func(t *testing.T) run {
				t.Helper()
				return run{env: []string{"GRADLE_OPTS=" + trustStoreOpts(writeTrustStore(t, "x", a), "x") + " -Djavax.net.ssl.trustStoreType=PKCS11"}}
			},
			wantErr: "type PKCS11 is not supported",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := c.setup(t)
			if r.dir == "" {
				r.dir = t.TempDir()
			}
			got, err := c.manager.upstreamTrustAnchors(r.env, r.dir, r.args)
			if c.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, c.wantErr)
			}
			if c.want == nil {
				assert.Empty(t, got)
				return
			}
			require.Len(t, got, 1)
			assert.True(t, got[0].Equal(c.want), "got %s, want %s", got[0].Subject, c.want.Subject)
		})
	}
}

func TestDecodeJVMTrustStoreReportsTheFirstError(t *testing.T) {
	t.Parallel()
	der, err := pkcs12.Modern.EncodeTrustStore([]*x509.Certificate{newTestAuthority(t).cert}, "y")
	require.NoError(t, err)
	var pfx struct {
		Version  int
		AuthSafe asn1.RawValue
		MacData  asn1.RawValue `asn1:"optional"`
	}
	_, err = asn1.Unmarshal(der, &pfx)
	require.NoError(t, err)
	noMAC, err := asn1.Marshal(struct {
		Version  int
		AuthSafe asn1.RawValue
	}{pfx.Version, pfx.AuthSafe})
	require.NoError(t, err)

	_, err = decodeJVMTrustStore(noMAC, "", "y")
	require.ErrorContains(t, err, "no MAC in data")
}

func writeMavenJVMConfig(t *testing.T, root, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".mvn"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".mvn", "jvm.config"), []byte(content), 0o600))
}

func writeMavenConfigFile(t *testing.T, root, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".mvn"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".mvn", "maven.config"), []byte(content), 0o600))
}

func TestStripJVMConfigComments(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want []string
	}{
		{in: "# -Da=1\n-Db=2\n", want: []string{"-Db=2"}},
		{in: "  # -Da=1\n", want: nil},
		{in: "-Da=1 # -Db=2\n-Dc=3", want: []string{"-Da=1", "-Dc=3"}},
		{in: "-Da=1\t#x\r\n-Db=2", want: []string{"-Da=1", "-Db=2"}},
		{in: "# c\r-Da=1\r-Db=2\r", want: []string{"-Da=1", "-Db=2"}},
		{in: "-Dp=pa#ss", want: []string{"-Dp=pa#ss"}},
		{in: "-Dp=\"/a #1/b\"\n-Dq=s", want: []string{"-Dp=\"/a", "-Dq=s"}},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, splitJVMArgs(stripJVMConfigComments(c.in)), "input %q", c.in)
	}
}

func TestSplitJVMArgs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want []string
	}{
		{in: "", want: nil},
		{in: "  -Da=1 \t-Db=2\n-Dc=3 ", want: []string{"-Da=1", "-Db=2", "-Dc=3"}},
		{in: `"-Dp=/a b/c" -Xmx1g`, want: []string{"-Dp=/a b/c", "-Xmx1g"}},
		{in: `-Dp="/a b/c" -Dq='x "y" z'`, want: []string{"-Dp=/a b/c", `-Dq=x "y" z`}},
		{in: `-Dp= -Dq=""`, want: []string{"-Dp=", "-Dq="}},
		{in: `-Dp=C:\Users\me\ts.p12`, want: []string{`-Dp=C:\Users\me\ts.p12`}},
		{in: `-Dp="/x -Dq=pw`, want: []string{`-Dp="/x`, "-Dq=pw"}},
		{in: "-Dp=\"/a b\n-Dq=1", want: []string{`-Dp="/a`, "b", "-Dq=1"}},
		{in: "-Dp=\"/x\n-Dq=pw\"", want: []string{`-Dp="/x`, `-Dq=pw"`}},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, splitJVMArgs(c.in), "input %q", c.in)
	}
}
