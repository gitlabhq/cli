package pm

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
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
	"BUNDLE_SSL_CA_CERT",
	"CARGO_HTTP_CAINFO",
}

// upstreamRootCAs returns the system roots extended with every certificate in
// caCertPath and in the files named by upstreamCAVars in env, and those extra
// certificates PEM-encoded. It returns a nil pool and PEM when no file yields
// a certificate, so the transport default applies.
// Unusable files are logged only under GLAB_DEBUG: writeCABundle already warns
// about the running manager's variables, and the API client fails on a bad
// ca_cert.
func upstreamRootCAs(env []string, caCertPath string) (*x509.CertPool, []byte, error) {
	paths := make([]string, 0, len(upstreamCAVars)+1)
	paths = append(paths, caCertPath)
	for _, v := range upstreamCAVars {
		paths = append(paths, lookupEnv(env, v))
	}

	var certs []*x509.Certificate
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
