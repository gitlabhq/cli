package api

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"fmt"
	"os"

	"software.sslmate.com/src/go-pkcs12"

	"gitlab.com/gitlab-org/cli/internal/dbg"
)

var (
	oidPKCS7Data       = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	oidPKCS7SignedData = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
)

// loadClientCertificates loads a PEM or DER certificate and key pair, or, when
// keyFile is empty, a PKCS#12 bundle decrypted with password. A certFile without
// a keyFile that is not a PKCS#12 bundle yields no certificate and no error,
// because glab ignored a lone client_cert before PKCS#12 support and CI
// auto-login can set CI_SERVER_TLS_CERT_FILE without CI_SERVER_TLS_KEY_FILE.
// A PKCS#12 bundle that cannot be decoded is an error.
func loadClientCertificates(certFile, keyFile, password string) ([]tls.Certificate, error) {
	if keyFile == "" {
		return loadPKCS12ClientCertificate(certFile, password)
	}

	certData, err := os.ReadFile(certFile)
	if err != nil {
		return nil, fmt.Errorf("reading client certificate: %w", err)
	}
	keyData, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("reading client key: %w", err)
	}

	if !isPEM(certData) {
		if _, err := x509.ParseCertificate(certData); err != nil {
			return nil, fmt.Errorf("client certificate %q is not a PEM or DER certificate (for a PKCS#12 bundle, leave client_key unset): %w", certFile, err)
		}
		certData = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certData})
	}
	if !isPEM(keyData) {
		// tls.X509KeyPair tries PKCS#1, PKCS#8 and SEC 1 on any "PRIVATE KEY" block.
		keyData = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyData})
	}

	cert, err := tls.X509KeyPair(certData, keyData)
	if err != nil {
		return nil, fmt.Errorf("loading client certificate %q and key %q: %w", certFile, keyFile, err)
	}
	return []tls.Certificate{cert}, nil
}

// pkcs12Password calls password only when certFile, used without keyFile, is a
// PKCS#12 bundle that an empty password does not open, so an unreadable keyring
// cannot fail a client that never uses the password. When certFile is not a
// readable bundle, a client_cert_password that would be ignored is an error;
// failing to read it there is not.
func pkcs12Password(certFile, keyFile string, password func() (string, error)) (string, error) {
	if certFile == "" || keyFile != "" || password == nil {
		return "", nil
	}
	data, err := os.ReadFile(certFile)
	if err != nil || !isPKCS12(data) {
		if configured, readErr := password(); readErr == nil && configured != "" {
			return "", unusedPasswordError(certFile, err)
		}
		return "", nil
	}
	if _, _, _, err := pkcs12.DecodeChain(data, ""); !errors.Is(err, pkcs12.ErrIncorrectPassword) {
		return "", nil
	}
	return password()
}

func unusedPasswordError(certFile string, readErr error) error {
	if readErr != nil {
		return fmt.Errorf("client_cert_password is set, but the client certificate %q cannot be read: %w", certFile, readErr)
	}
	return fmt.Errorf("client_cert_password is set, but the client certificate %q is not a PKCS#12 bundle (for a PEM or DER certificate, set client_key)", certFile)
}

func loadPKCS12ClientCertificate(certFile, password string) ([]tls.Certificate, error) {
	data, err := os.ReadFile(certFile)
	if err != nil {
		dbg.Debugf("api: not using client certificate %q: client_key is unset and the file cannot be read: %v", certFile, err)
		return nil, nil
	}
	if !isPKCS12(data) {
		dbg.Debugf("api: not using client certificate %q: client_key is unset and the file is not a PKCS#12 bundle", certFile)
		return nil, nil
	}

	key, leaf, chain, err := pkcs12.DecodeChain(data, password)
	if errors.Is(err, pkcs12.ErrIncorrectPassword) {
		return nil, fmt.Errorf("incorrect password for the PKCS#12 client certificate %q: set client_cert_password", certFile)
	}
	if err != nil {
		return nil, fmt.Errorf("loading PKCS#12 client certificate %q: %w", certFile, err)
	}

	cert := tls.Certificate{
		Certificate: [][]byte{leaf.Raw},
		PrivateKey:  key,
		Leaf:        leaf,
	}
	for _, c := range chain {
		cert.Certificate = append(cert.Certificate, c.Raw)
	}
	return []tls.Certificate{cert}, nil
}

// isPKCS12 checks only the outer PFX structure from RFC 7292: a version, then
// PKCS#7 data or signed-data content. go-pkcs12 reports every later failure,
// such as an unsupported algorithm or a missing key, for a file that has it.
func isPKCS12(data []byte) bool {
	var pfx struct {
		Version  int
		AuthSafe struct{ ContentType asn1.ObjectIdentifier }
	}
	if _, err := asn1.Unmarshal(data, &pfx); err != nil {
		return false
	}
	return pfx.AuthSafe.ContentType.Equal(oidPKCS7Data) || pfx.AuthSafe.ContentType.Equal(oidPKCS7SignedData)
}

func isPEM(data []byte) bool {
	block, _ := pem.Decode(data)
	return block != nil
}
