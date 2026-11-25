package pm

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"maps"
	"os"

	"gitlab.com/gitlab-org/cli/internal/dbg"
)

// upstreamCAVars is the union of every manager's ExistingBundleVars. Registry
// connections trust the files they name whichever manager runs, so a CA the
// user set for one ecosystem also covers the proxy in front of another.
var upstreamCAVars = []string{
	"SSL_CERT_FILE",
	"REQUESTS_CA_BUNDLE",
	"PIP_CERT",
	"TWINE_CERT",
	"NODE_EXTRA_CA_CERTS",
	"npm_config_cafile",
	"NPM_CONFIG_CAFILE",
	"YARN_CAFILE",
	"YARN_HTTPS_CA_FILE_PATH",
	"YARN_CA_FILE_PATH",
	"BUNDLE_SSL_CA_CERT",
	"CARGO_HTTP_CAINFO",
}

// UpstreamRootCAs is upstreamRootCAs for the job-level proxy, which has no
// single manager but must trust the same registry CAs as a per-command run.
// That includes the truststores mvn's and gradle's JVMs would load, which the
// job-level proxy replaces for them. dir is the project directory, where
// .mvn/jvm.config and .mvn/maven.config are read, and a truststore that can't
// be used is reported through logf. The job-level proxy needs only the pool,
// so the PEM return is dropped here.
func UpstreamRootCAs(env []string, dir, caCertPath string, logf func(string, ...any)) (*x509.CertPool, error) {
	anchors, err := jobJVMTrustAnchors(env, dir)
	logWarnings(logf, err)
	pool, _, err := upstreamRootCAs(env, caCertPath, anchors)
	return pool, err
}

func jobJVMTrustAnchors(env []string, dir string) ([]*x509.Certificate, error) {
	maven := mavenTrustProps(env, dir, nil, mavenArgProperties(env, dir, nil))
	certs, err := jvmTrustAnchors(env, dir, maven)
	if gradle := gradleTrustProps(env); !maps.Equal(gradle, maven) {
		more, moreErr := jvmTrustAnchors(env, dir, gradle)
		certs, err = append(certs, more...), errors.Join(err, moreErr)
	}
	return certs, err
}

func WarnJobTrustStoreOverrides(env []string, dir string, warnf func(string, ...any)) {
	logWarnings(warnf, errors.Join(
		jvmTrustStoreOverride(env, "mvn", "MAVEN_OPTS", "MAVEN_DEBUG_OPTS", "_JAVA_OPTIONS"),
		argTrustStoreOverride(mavenArgProperties(env, dir, nil), "-D%s in .mvn/maven.config or MAVEN_ARGS replaces glab's truststore on Maven 3, so mvn cannot trust the dependency firewall proxy there; set it in MAVEN_OPTS instead"),
		jvmTrustStoreOverride(env, "gradle", "GRADLE_OPTS", "_JAVA_OPTIONS"),
	))
}

// upstreamRootCAs returns the system roots extended with anchors and with
// every certificate in caCertPath and in the files named by upstreamCAVars in
// env, and those extra certificates PEM-encoded. It returns a nil pool and PEM
// when there are no anchors and no file yields a certificate, so the transport
// default applies. Unusable files are logged only under GLAB_DEBUG:
// writeCABundle already warns about the running manager's variables, and the
// API client fails on a bad ca_cert.
func upstreamRootCAs(env []string, caCertPath string, anchors []*x509.Certificate) (*x509.CertPool, []byte, error) {
	paths := make([]string, 0, len(upstreamCAVars)+1)
	paths = append(paths, caCertPath)
	for _, v := range upstreamCAVars {
		paths = append(paths, caVarValues(env, v)...)
	}

	certs := anchors
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, dup := seen[path]; dup {
			continue
		}
		seen[path] = struct{}{}
		raw, err := os.ReadFile(path)
		if err != nil {
			dbg.Debugf("dependency firewall: skipping registry CA file %q: %v", path, err)
			continue
		}
		parsed := parseCertsPEM(raw)
		if len(parsed) == 0 {
			dbg.Debugf("dependency firewall: skipping registry CA file %q: no PEM certificates", path)
			continue
		}
		certs = append(certs, parsed...)
	}
	if len(certs) == 0 {
		return nil, nil, nil
	}

	pool, err := x509.SystemCertPool()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load system certificate pool: %w", err)
	}
	var caPEM []byte
	for _, c := range certs {
		pool.AddCert(c)
		caPEM = append(caPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})...)
	}
	return pool, caPEM, nil
}
