//go:build !integration

package artifactregistry

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	primaryFingerprint   = "0123456789ABCDEF0123456789ABCDEF01234567"
	secondaryFingerprint = "89ABCDEF0123456789ABCDEF0123456789ABCDEF"
)

func registryServer(t *testing.T, handler http.HandlerFunc) *url.URL {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	return u
}

func TestSigningKeyFingerprints(t *testing.T) {
	for _, tc := range []struct {
		name      string
		secondary string
		want      []string
	}{
		{name: "one selected key", secondary: "null", want: []string{primaryFingerprint}},
		{name: "a rotation", secondary: `"` + secondaryFingerprint + `"`, want: []string{primaryFingerprint, secondaryFingerprint}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := registryServer(t, func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/api/v1/acme/repositories/apt-internal", r.URL.Path)
				assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
				_, _ = w.Write([]byte(`{"name":"apt-internal","format":"debian","settings":{"signing_key_fingerprint":"` + primaryFingerprint + `","secondary_signing_key_fingerprint":` + tc.secondary + `}}`))
			})

			got, err := NewRegistryClient(http.DefaultClient, "tok").SigningKeyFingerprints(t.Context(), base, "acme", "apt-internal")
			require.NoError(t, err)
			assert.Equal(t, tc.want, got.List())
		})
	}
}

func TestSigningKeyFingerprints_KeepsTheBasePath(t *testing.T) {
	base := registryServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/prefix/api/v1/acme/repositories/apt-internal", r.URL.Path)
		_, _ = w.Write([]byte(`{"settings":{"signing_key_fingerprint":"` + primaryFingerprint + `"}}`))
	})

	_, err := NewRegistryClient(http.DefaultClient, "tok").SigningKeyFingerprints(t.Context(), base.JoinPath("prefix"), "acme", "apt-internal")
	require.NoError(t, err)
}

func TestSigningKeyFingerprints_NoFingerprint(t *testing.T) {
	for _, body := range []string{`{}`, `{"settings":{}}`, `{"settings":{"signing_key_fingerprint":""}}`} {
		t.Run(body, func(t *testing.T) {
			base := registryServer(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			})

			_, err := NewRegistryClient(http.DefaultClient, "tok").SigningKeyFingerprints(t.Context(), base, "acme", "apt-internal")
			assert.ErrorIs(t, err, ErrNoSigningKeyFingerprint)
		})
	}
}

func TestSigningKeyFingerprints_Forbidden(t *testing.T) {
	base := registryServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})

	_, err := NewRegistryClient(http.DefaultClient, "tok").SigningKeyFingerprints(t.Context(), base, "acme", "apt-internal")
	var statusErr *StatusError
	require.ErrorAs(t, err, &statusErr)
	assert.Equal(t, http.StatusForbidden, statusErr.StatusCode)
}

func TestPublicKeys(t *testing.T) {
	const armored = "-----BEGIN PGP PUBLIC KEY BLOCK-----\n...\n-----END PGP PUBLIC KEY BLOCK-----\n"
	base := registryServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/acme/debian/apt-internal/key.asc", r.URL.Path)
		user, password, ok := r.BasicAuth()
		assert.True(t, ok, "client routes take HTTP Basic")
		assert.Equal(t, "token", user)
		assert.Equal(t, "tok", password)
		_, _ = w.Write([]byte(armored))
	})

	got, err := NewRegistryClient(http.DefaultClient, "tok").PublicKeys(t.Context(), base.JoinPath("acme", "debian", "apt-internal"))
	require.NoError(t, err)
	assert.Equal(t, armored, string(got))
}

func TestPublicKeys_NotFound(t *testing.T) {
	base := registryServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	_, err := NewRegistryClient(http.DefaultClient, "tok").PublicKeys(t.Context(), base.JoinPath("acme", "debian", "apt-internal"))
	var statusErr *StatusError
	require.ErrorAs(t, err, &statusErr)
	assert.Equal(t, http.StatusNotFound, statusErr.StatusCode)
	assert.NotContains(t, err.Error(), "tok", "the error must not carry the token")
}

func TestPublicKeys_RefusesAnOversizedBody(t *testing.T) {
	base := registryServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("a", maxRegistryResponseBytes+1)))
	})

	_, err := NewRegistryClient(http.DefaultClient, "tok").PublicKeys(t.Context(), base)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "larger than")
}

func TestSigningKeyFingerprints_StatusErrorCarriesTheErrorEnvelope(t *testing.T) {
	base := registryServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"namespace_blocked","message":"the namespace is blocked","request_id":"r1"}}`))
	})

	_, err := NewRegistryClient(http.DefaultClient, "tok").SigningKeyFingerprints(t.Context(), base, "acme", "apt-internal")
	var statusErr *StatusError
	require.ErrorAs(t, err, &statusErr)
	assert.Equal(t, "namespace_blocked", statusErr.Code)
	assert.Equal(t, "the namespace is blocked", statusErr.Message)
	assert.Contains(t, err.Error(), "403 Forbidden (namespace_blocked): the namespace is blocked")
}

func TestPublicKeys_StatusErrorWithoutAnEnvelope(t *testing.T) {
	base := registryServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>bad gateway</html>"))
	})

	_, err := NewRegistryClient(http.DefaultClient, "tok").PublicKeys(t.Context(), base)
	var statusErr *StatusError
	require.ErrorAs(t, err, &statusErr)
	assert.Empty(t, statusErr.Code)
	assert.True(t, strings.HasSuffix(err.Error(), "502 Bad Gateway"), err.Error())
}

func TestPublicKeys_CrossHostRedirectDropsTheToken(t *testing.T) {
	other := registryServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Authorization"), "the token must not follow a redirect to another host")
		_, _ = w.Write([]byte("keys"))
	})
	// localhost and 127.0.0.1 reach the same listener, but net/http treats
	// them as different hosts.
	other.Host = strings.Replace(other.Host, "127.0.0.1", "localhost", 1)
	base := registryServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.JoinPath("key.asc").String(), http.StatusFound)
	})

	_, err := NewRegistryClient(http.DefaultClient, "tok").PublicKeys(t.Context(), base)
	require.NoError(t, err)
}
