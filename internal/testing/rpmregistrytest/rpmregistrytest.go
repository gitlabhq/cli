// Package rpmregistrytest provides a fake Artifact Registry RPM repository and
// OpenPGP key fixtures, for internal/api/artifactregistry and the commands
// built on it.
package rpmregistrytest

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// The fixtures are RSA-3072 v4 keys that gpg generated, and the fingerprints
// are the ones gpg reported for them, so a parser is checked against an
// independent implementation. KeySub carries an encryption subkey, which the
// registry never generates.
var (
	//go:embed testdata/key-a.asc
	keyA string
	//go:embed testdata/key-b.asc
	keyB string
	//go:embed testdata/key-sub.asc
	keySub string
)

const (
	KeyAFingerprint = "6E81E6039E2B3E46675AFD45680E0716A26FC847"
	KeyBFingerprint = "3B704C4C72407677E1FF15C7B8418E685627E3AB"
)

func KeyA() []byte   { return []byte(keyA) }
func KeyB() []byte   { return []byte(keyB) }
func KeySub() []byte { return []byte(keySub) }

// The repository the fake serves.
const (
	Handle = "acme"
	Name   = "rpm-internal"
)

// Registry is a fake registry serving one hosted RPM repository: its detail
// at /api/v1/acme/repositories/rpm-internal, which needs Bearer Token, and its
// repomd.xml.key, which needs Basic token:Token.
type Registry struct {
	Token string
	// Settings answers successive detail requests, the last one repeating.
	Settings []map[string]any
	KeyFile  []byte

	DetailRequests atomic.Int32
	KeyRequests    atomic.Int32
}

// Selects returns repository settings selecting the key with fingerprint
// primary and, during a rotation, secondary. Pass nil for no secondary.
func Selects(primary string, secondary any) map[string]any {
	return map[string]any{
		"signing_key_id":                    "00000000-0000-0000-0000-000000000001",
		"secondary_signing_key_id":          nil,
		"signing_key_fingerprint":           primary,
		"secondary_signing_key_fingerprint": secondary,
	}
}

// Start serves r over TLS until the test ends, and returns the server and the
// repository's URL on it.
func (r *Registry) Start(t *testing.T) (*httptest.Server, string) {
	t.Helper()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/api/v1/" + Handle + "/repositories/" + Name:
			if req.Header.Get("Authorization") != "Bearer "+r.Token {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			n := int(r.DetailRequests.Add(1)) - 1
			settings := r.Settings[min(n, len(r.Settings)-1)]
			_ = json.NewEncoder(w).Encode(map[string]any{"name": Name, "format": "rpm", "kind": "hosted", "settings": settings}) //nolint:forbidigo // a fake server response body, not stdout
		case "/" + Handle + "/rpm/" + Name + "/repodata/repomd.xml.key":
			user, password, ok := req.BasicAuth()
			if !ok || user != "token" || password != r.Token {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			r.KeyRequests.Add(1)
			_, _ = w.Write(r.KeyFile)
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(srv.Close)

	return srv, srv.URL + "/" + Handle + "/rpm/" + Name
}
