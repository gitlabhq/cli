//go:build !integration

package api

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
	"software.sslmate.com/src/go-pkcs12"
)

const clientCertCN = "glab-mtls-client"

type clientIdentity struct {
	ca   *x509.Certificate
	leaf *x509.Certificate
	key  crypto.Signer
}

func newClientIdentity(t *testing.T, key crypto.Signer) clientIdentity {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "glab test client CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caKey.Public(), caKey)
	require.NoError(t, err)
	ca, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)

	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: clientCertCN},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, ca, key.Public(), caKey)
	require.NoError(t, err)
	leaf, err := x509.ParseCertificate(leafDER)
	require.NoError(t, err)

	return clientIdentity{ca: ca, leaf: leaf, key: key}
}

func newECKey(t *testing.T) crypto.Signer {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	return key
}

func newRSAKey(t *testing.T) crypto.Signer {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return key
}

// newMTLSServer starts a TLS server that requires a client certificate signed
// by ca and reports the common name of each certificate it accepts.
func newMTLSServer(t *testing.T, ca *x509.Certificate) (*httptest.Server, <-chan string) {
	t.Helper()

	names := make(chan string, 1)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		names <- r.TLS.PeerCertificates[0].Subject.CommonName
		w.WriteHeader(http.StatusOK)
	}))
	clientCAs := x509.NewCertPool()
	clientCAs.AddCert(ca)
	srv.TLS = &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clientCAs}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, names
}

func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

func pkcs8DER(t *testing.T, key crypto.Signer) []byte {
	t.Helper()

	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	return der
}

func pkcs12Bundle(t *testing.T, id clientIdentity, password string) []byte {
	t.Helper()

	data, err := pkcs12.Modern.Encode(id.key, id.leaf, []*x509.Certificate{id.ca}, password)
	require.NoError(t, err)
	return data
}

func TestNewClientFromConfig_ClientCertificateFormats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		key      func(*testing.T) crypto.Signer
		settings func(*testing.T, clientIdentity) map[string]string
	}{
		{
			name: "PEM certificate and PKCS#8 key",
			key:  newECKey,
			settings: func(t *testing.T, id clientIdentity) map[string]string {
				t.Helper()
				return map[string]string{
					"client_cert": writeFile(t, "client.crt", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: id.leaf.Raw})),
					"client_key":  writeFile(t, "client.key", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8DER(t, id.key)})),
				}
			},
		},
		{
			name: "DER certificate and PKCS#8 key",
			key:  newECKey,
			settings: func(t *testing.T, id clientIdentity) map[string]string {
				t.Helper()
				return map[string]string{
					"client_cert": writeFile(t, "client.der", id.leaf.Raw),
					"client_key":  writeFile(t, "client.key.der", pkcs8DER(t, id.key)),
				}
			},
		},
		{
			name: "DER certificate and PKCS#1 key",
			key:  newRSAKey,
			settings: func(t *testing.T, id clientIdentity) map[string]string {
				t.Helper()
				rsaKey, ok := id.key.(*rsa.PrivateKey)
				require.True(t, ok)
				return map[string]string{
					"client_cert": writeFile(t, "client.der", id.leaf.Raw),
					"client_key":  writeFile(t, "client.key.der", x509.MarshalPKCS1PrivateKey(rsaKey)),
				}
			},
		},
		{
			name: "DER certificate and EC key",
			key:  newECKey,
			settings: func(t *testing.T, id clientIdentity) map[string]string {
				t.Helper()
				ecKey, ok := id.key.(*ecdsa.PrivateKey)
				require.True(t, ok)
				keyDER, err := x509.MarshalECPrivateKey(ecKey)
				require.NoError(t, err)
				return map[string]string{
					"client_cert": writeFile(t, "client.der", id.leaf.Raw),
					"client_key":  writeFile(t, "client.key.der", keyDER),
				}
			},
		},
		{
			name: "PKCS#12 bundle",
			key:  newRSAKey,
			settings: func(t *testing.T, id clientIdentity) map[string]string {
				t.Helper()
				return map[string]string{
					"client_cert":          writeFile(t, "client.p12", pkcs12Bundle(t, id, "changeit")),
					"client_cert_password": "changeit",
				}
			},
		},
		{
			name: "PKCS#12 bundle without a password",
			key:  newECKey,
			settings: func(t *testing.T, id clientIdentity) map[string]string {
				t.Helper()
				return map[string]string{
					"client_cert": writeFile(t, "client.p12", pkcs12Bundle(t, id, "")),
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			id := newClientIdentity(t, tc.key(t))
			srv, names := newMTLSServer(t, id.ca)
			settings := tc.settings(t, id)
			settings["skip_tls_verify"] = "true"
			cfg := newServerConfig(t, srv, settings)

			client, err := NewClientFromConfig(serverHost(srv), cfg, false, "glab-test")
			require.NoError(t, err)
			mustGet(t, client, srv.URL)

			assert.Equal(t, clientCertCN, <-names)
		})
	}
}

func TestNewClientFromConfig_ClientCertificateErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		settings func(*testing.T, clientIdentity) map[string]string
		wantErr  string
	}{
		{
			name: "wrong PKCS#12 password",
			settings: func(t *testing.T, id clientIdentity) map[string]string {
				t.Helper()
				return map[string]string{
					"client_cert":          writeFile(t, "client.p12", pkcs12Bundle(t, id, "changeit")),
					"client_cert_password": "wrong",
				}
			},
			wantErr: "incorrect password for the PKCS#12 client certificate",
		},
		{
			name: "unknown certificate format with a key",
			settings: func(t *testing.T, id clientIdentity) map[string]string {
				t.Helper()
				return map[string]string{
					"client_cert": writeFile(t, "client.crt", []byte("not a certificate")),
					"client_key":  writeFile(t, "client.key.der", pkcs8DER(t, id.key)),
				}
			},
			wantErr: "is not a PEM or DER certificate",
		},
		{
			name: "PKCS#12 bundle without a private key",
			settings: func(t *testing.T, id clientIdentity) map[string]string {
				t.Helper()
				data, err := pkcs12.Passwordless.EncodeTrustStore([]*x509.Certificate{id.leaf}, "")
				require.NoError(t, err)
				return map[string]string{"client_cert": writeFile(t, "truststore.p12", data)}
			},
			wantErr: "loading PKCS#12 client certificate",
		},
		{
			name: "client_cert_password with a PEM certificate",
			settings: func(t *testing.T, id clientIdentity) map[string]string {
				t.Helper()
				return map[string]string{
					"client_cert":          writeFile(t, "client.crt", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: id.leaf.Raw})),
					"client_cert_password": "changeit",
				}
			},
			wantErr: "is not a PKCS#12 bundle",
		},
		{
			name: "client_cert_password with a missing file",
			settings: func(t *testing.T, _ clientIdentity) map[string]string {
				t.Helper()
				return map[string]string{
					"client_cert":          filepath.Join(t.TempDir(), "missing.p12"),
					"client_cert_password": "changeit",
				}
			},
			wantErr: "cannot be read",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			id := newClientIdentity(t, newECKey(t))
			srv, _ := newMTLSServer(t, id.ca)
			settings := tc.settings(t, id)
			settings["skip_tls_verify"] = "true"
			cfg := newServerConfig(t, srv, settings)

			_, err := NewClientFromConfig(serverHost(srv), cfg, false, "glab-test")

			require.ErrorContains(t, err, tc.wantErr)
			assert.ErrorContains(t, err, settings["client_cert"])
		})
	}
}

func TestNewClientFromConfig_ClientCertificateWithoutKeyNotPKCS12IsNotSent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		certFile func(*testing.T, clientIdentity) string
	}{
		{
			name: "PEM certificate",
			certFile: func(t *testing.T, id clientIdentity) string {
				t.Helper()
				return writeFile(t, "client.crt", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: id.leaf.Raw}))
			},
		},
		{
			name: "DER certificate",
			certFile: func(t *testing.T, id clientIdentity) string {
				t.Helper()
				return writeFile(t, "client.der", id.leaf.Raw)
			},
		},
		{
			name: "unknown format",
			certFile: func(t *testing.T, _ clientIdentity) string {
				t.Helper()
				return writeFile(t, "client.crt", []byte("not a certificate"))
			},
		},
		{
			name: "missing file",
			certFile: func(t *testing.T, _ clientIdentity) string {
				t.Helper()
				return filepath.Join(t.TempDir(), "missing.crt")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			id := newClientIdentity(t, newECKey(t))
			peerCerts := make(chan int, 1)
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				peerCerts <- len(r.TLS.PeerCertificates)
				w.WriteHeader(http.StatusOK)
			}))
			srv.TLS = &tls.Config{ClientAuth: tls.RequestClientCert}
			srv.StartTLS()
			t.Cleanup(srv.Close)
			cfg := newServerConfig(t, srv, map[string]string{
				"skip_tls_verify": "true",
				"client_cert":     tc.certFile(t, id),
			})

			client, err := NewClientFromConfig(serverHost(srv), cfg, false, "glab-test")
			require.NoError(t, err)
			mustGet(t, client, srv.URL)

			assert.Zero(t, <-peerCerts)
		})
	}
}

func TestNewClientFromConfig_ClientCertificateWithoutKeyIgnoresUnreadablePassword(t *testing.T) {
	t.Setenv("GLAB_CLIENT_CERT_PASSWORD", "")

	id := newClientIdentity(t, newECKey(t))
	peerCerts := make(chan int, 1)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peerCerts <- len(r.TLS.PeerCertificates)
		w.WriteHeader(http.StatusOK)
	}))
	srv.TLS = &tls.Config{ClientAuth: tls.RequestClientCert}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	cfg := newServerConfig(t, srv, map[string]string{
		"skip_tls_verify": "true",
		"use_keyring":     "true",
		"client_cert":     writeFile(t, "client.crt", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: id.leaf.Raw})),
	})
	keyring.MockInitWithError(errors.New("keyring locked"))
	t.Cleanup(keyring.MockInit)

	client, err := NewClientFromConfig(serverHost(srv), cfg, false, "glab-test", WithoutAuthentication())
	require.NoError(t, err)
	mustGet(t, client, srv.URL)

	assert.Zero(t, <-peerCerts)
}

func TestNewClientFromConfig_PKCS12PasswordReadErrorIsWrapped(t *testing.T) {
	t.Setenv("GLAB_CLIENT_CERT_PASSWORD", "")

	id := newClientIdentity(t, newECKey(t))
	srv, _ := newMTLSServer(t, id.ca)
	cfg := newServerConfig(t, srv, map[string]string{
		"skip_tls_verify": "true",
		"use_keyring":     "true",
		"client_cert":     writeFile(t, "client.p12", pkcs12Bundle(t, id, "changeit")),
	})
	errLocked := errors.New("keyring locked")
	keyring.MockInitWithError(errLocked)
	t.Cleanup(keyring.MockInit)

	_, err := NewClientFromConfig(serverHost(srv), cfg, false, "glab-test", WithoutAuthentication())

	require.ErrorIs(t, err, errLocked)
	assert.ErrorContains(t, err, fmt.Sprintf("failed to read client_cert_password for %q", serverHost(srv)))
}
