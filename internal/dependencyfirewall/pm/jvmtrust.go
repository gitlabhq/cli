package pm

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync"

	"software.sslmate.com/src/go-pkcs12"

	"gitlab.com/gitlab-org/cli/internal/dbg"
)

// jvmTrustStorePassword is the password for the generated PKCS#12 truststore.
// The store holds only public certificates (no private keys) and lives in a
// temp file removed after the run, so the password is not a secret; the JVM
// simply requires one.
const jvmTrustStorePassword = "df-truststore-not-secret"

// writeJVMTrustStoreAt builds a PKCS#12 truststore at path containing the
// given CA certificates (the proxy MITM CA, plus any user CAs prepended into
// the proxy bundle) and the host's system roots, so the JVM (Maven, Gradle)
// trusts the proxy while still validating public TLS dependencies. On the Run
// path the proxy bundle already holds the system roots, so a root in cas is
// not added twice. The caller ties the file's lifecycle to a known path it
// already cleans up. It returns the truststore password.
func writeJVMTrustStoreAt(path string, cas ...*x509.Certificate) (string, error) {
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	if err := encodeJVMTrustStore(f, cas...); err != nil {
		return "", errors.Join(err, f.Close())
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return jvmTrustStorePassword, nil
}

// encodeJVMTrustStore encodes cas plus the system roots into a PKCS#12
// truststore and writes it to w.
func encodeJVMTrustStore(w io.Writer, cas ...*x509.Certificate) error {
	sysRoots := systemRoots()
	roots := slices.Clone(cas)
	for _, r := range sysRoots {
		if !slices.ContainsFunc(cas, r.Equal) {
			roots = append(roots, r)
		}
	}

	// pkcs12.Modern.EncodeTrustStore marks the certs with the Java TrustStore
	// OID (Java 8+). The method form takes (certs, password); the package-level
	// pkcs12.EncodeTrustStore additionally takes a rand source.
	der, err := pkcs12.Modern.EncodeTrustStore(roots, jvmTrustStorePassword)
	if err != nil {
		return err
	}
	_, err = w.Write(der)
	return err
}

// jvmTrustArgs returns the JVM system-property flags that point at the
// truststore at path with the given password.
func jvmTrustArgs(path, password string) []string {
	return []string{
		"-Djavax.net.ssl.trustStore=" + path,
		"-Djavax.net.ssl.trustStorePassword=" + password,
		"-Djavax.net.ssl.trustStoreType=PKCS12",
	}
}

// JVMTrustOpts reads the PEM CA bundle at caPath, writes a PKCS#12 truststore
// at caPath+".p12" (which Run cleans up), and returns the JVM truststore flags
// as a single space-joined string, prepending any value already in MAVEN_OPTS.
// It returns "" when the CA bundle has no parsable certificates.
func JVMTrustOpts(caPath string) string {
	raw, err := os.ReadFile(caPath)
	if err != nil {
		dbg.Debugf("dependency firewall: failed to read CA bundle %s for JVM truststore: %v", caPath, err)
		return ""
	}
	cas := parseCertsPEM(raw)
	if len(cas) == 0 {
		dbg.Debugf("dependency firewall: CA bundle %s has no parsable certificates; JVM will not trust the proxy", caPath)
		return ""
	}
	tsPath := caPath + ".p12"
	password, err := writeJVMTrustStoreAt(tsPath, cas...)
	if err != nil {
		dbg.Debugf("dependency firewall: failed to write JVM truststore %s: %v", tsPath, err)
		return ""
	}
	opts := strings.Join(jvmTrustArgs(tsPath, password), " ")
	if existing := os.Getenv("MAVEN_OPTS"); existing != "" {
		opts = existing + " " + opts
	}
	return opts
}

// systemRoots returns the host's trusted root certificates, read once per
// process. Most CA settings glab points at its bundle, like the JVM's
// -Djavax.net.ssl.trustStore flag, replace (not augment) a tool's default
// trust, so a bundle or truststore built from the proxy CA alone would break
// every connection the proxy does not intercept. The Go standard library's
// x509.SystemCertPool does not expose the underlying certificates (only DER
// subjects), so the roots are sourced per platform: PEM CA-bundle files on
// Linux, the security(1) keychains on macOS, and the LocalMachine certificate
// stores on Windows. It returns nil when none can be read.
var systemRoots = sync.OnceValue(func() []*x509.Certificate {
	var roots []*x509.Certificate
	switch runtime.GOOS {
	case "darwin":
		roots = darwinSystemRoots()
	case "windows":
		roots = windowsSystemRoots()
	default:
		roots = unixSystemRoots()
	}
	if len(roots) == 0 {
		dbg.Debugf("dependency firewall: no system root certificates found for %s; tools that skip the proxy may fail to verify public hosts", runtime.GOOS)
	}
	return roots
})

// SystemCABundlePaths are the common locations of the OS CA bundle across
// Linux and BSD distributions, tried in order.
var SystemCABundlePaths = []string{
	"/etc/ssl/certs/ca-certificates.crt", // Debian, Ubuntu, Alpine
	"/etc/pki/tls/certs/ca-bundle.crt",   // Fedora, RHEL, CentOS
	"/etc/ssl/ca-bundle.pem",             // openSUSE
	"/etc/pki/tls/cacert.pem",
	"/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem",
	"/etc/ssl/cert.pem", // Alpine, some BSDs
}

// unixSystemRoots reads the first CA bundle in SystemCABundlePaths that holds
// certificates.
func unixSystemRoots() []*x509.Certificate {
	for _, p := range SystemCABundlePaths {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if certs := parseCertsPEM(raw); len(certs) > 0 {
			return certs
		}
	}
	return nil
}

// darwinSystemRoots exports the CA certificates in the macOS keychains via
// security(1), which prints the certificates as concatenated PEM.
func darwinSystemRoots() []*x509.Certificate {
	keychains := []string{
		"/System/Library/Keychains/SystemRootCertificates.keychain",
		"/Library/Keychains/System.keychain",
	}
	var certs []*x509.Certificate
	for _, kc := range keychains {
		out, err := exec.Command("/usr/bin/security", "find-certificate", "-a", "-p", kc).Output()
		if err != nil {
			dbg.Debugf("dependency firewall: security find-certificate failed for %s: %v", kc, err)
			continue
		}
		for _, c := range parseCertsPEM(out) {
			if c.IsCA {
				certs = append(certs, c)
			}
		}
	}
	return certs
}

// windowsSystemRoots exports the trusted roots from the Windows LocalMachine
// certificate stores via PowerShell, which prints them as concatenated PEM.
func windowsSystemRoots() []*x509.Certificate {
	const script = `$ErrorActionPreference='Stop';` +
		`foreach ($store in 'Root','CA') {` +
		`Get-ChildItem -Path "Cert:\LocalMachine\$store" | ForEach-Object {` +
		`"-----BEGIN CERTIFICATE-----";` +
		`[System.Convert]::ToBase64String($_.RawData, 'InsertLineBreaks');` +
		`"-----END CERTIFICATE-----" } }`
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		dbg.Debugf("dependency firewall: powershell certificate export failed: %v", err)
		return nil
	}
	return parseCertsPEM(out)
}

func parseCertsPEM(raw []byte) []*x509.Certificate {
	var certs []*x509.Certificate
	for {
		block, rest := pem.Decode(raw)
		if block == nil {
			break
		}
		raw = rest
		if block.Type != "CERTIFICATE" {
			continue
		}
		if c, err := x509.ParseCertificate(block.Bytes); err == nil {
			certs = append(certs, c)
		}
	}
	return certs
}
