//go:build !integration

package artifactregistry

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/testing/rpmregistrytest"
)

func TestParseRPMRepository(t *testing.T) {
	t.Parallel()

	repo, err := ParseRPMRepository("https://AR.example.com:08443/acme/rpm/rpm-internal/")

	require.NoError(t, err)
	assert.Equal(t, "https://ar.example.com:8443/acme/rpm/rpm-internal", repo.BaseURL().String())
	assert.Equal(t, "ar.example.com:8443", repo.Host())
	assert.Equal(t, "acme", repo.Handle())
	assert.Equal(t, "rpm-internal", repo.Name())
}

func TestParseRPMRepository_OneSpellingPerRepository(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"https://ar.example.com/acme/rpm/repo",
		"https://AR.example.com/acme/rpm/repo/",
		"https://ar.example.com:443/acme/rpm/repo",
		"https://ar.example.com:/acme/rpm/repo",
		"https://ar.example.com:0443/acme/rpm/repo",
	} {
		repo, err := ParseRPMRepository(raw)

		require.NoError(t, err, raw)
		assert.Equal(t, "https://ar.example.com/acme/rpm/repo", repo.BaseURL().String(), raw)
	}
}

func TestParseRPMRepository_Refusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"http", "http://ar.example.com/acme/rpm/repo", "scheme must be https"},
		{"no scheme", "ar.example.com/acme/rpm/repo", "scheme must be https"},
		{"user info", "https://u:p@ar.example.com/acme/rpm/repo", "no user info"},
		{"query", "https://ar.example.com/acme/rpm/repo?x=1", "no user info, query"},
		{"fragment", "https://ar.example.com/acme/rpm/repo#x", "no user info, query or fragment"},
		{"empty query", "https://ar.example.com/acme/rpm/repo?", "no user info, query or fragment"},
		{"no host", "https:///acme/rpm/repo", "no user info, query or fragment"},
		{"IPv6 host", "https://[::1]:8443/acme/rpm/repo", "the host must contain only"},
		{"quote in host", "https://ar\"x.example.com/acme/rpm/repo", "the host must contain only"},
		{"trailing dot in host", "https://ar.example.com./acme/rpm/repo", "must not end with '.'"},
		{"port 0", "https://ar.example.com:0/acme/rpm/repo", "port must be between 1 and 65535"},
		{"port too large", "https://ar.example.com:65536/acme/rpm/repo", "port must be between 1 and 65535"},
		{"not rpm", "https://ar.example.com/acme/npm/repo", "path must be"},
		{"extra segment", "https://ar.example.com/acme/rpm/repo/x", "path must be"},
		{"no repository", "https://ar.example.com/acme/rpm", "path must be"},
		{"uppercase handle", "https://ar.example.com/Acme/rpm/repo", "registry handle must"},
		{"underscore handle", "https://ar.example.com/ac_me/rpm/repo", "registry handle must"},
		{"dollar in repository", "https://ar.example.com/acme/rpm/re$po", "repository name must"},
		{"encoded slash in repository", "https://ar.example.com/acme/rpm/re%2Fpo", "repository name must"},
		{"repository ends with a dot", "https://ar.example.com/acme/rpm/repo.", "repository name must"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			repo, err := ParseRPMRepository(tc.raw)

			require.ErrorContains(t, err, tc.want)
			assert.Nil(t, repo)
		})
	}
}

// repoAt is the fake repository's address on srv.
func repoAt(t *testing.T, srv *httptest.Server) *RPMRepository {
	t.Helper()
	repo, err := ParseRPMRepository(srv.URL + "/" + rpmregistrytest.Handle + "/rpm/" + rpmregistrytest.Name)
	require.NoError(t, err)
	return repo
}

// startRegistry serves reg and returns the repository it serves.
func startRegistry(t *testing.T, reg *rpmregistrytest.Registry) (*httptest.Server, *RPMRepository) {
	t.Helper()
	reg.Token = "the-token"
	srv, raw := reg.Start(t)
	repo, err := ParseRPMRepository(raw)
	require.NoError(t, err)
	return srv, repo
}

func TestFetchRPMKeys_OneKey(t *testing.T) {
	t.Parallel()
	reg := &rpmregistrytest.Registry{Settings: []map[string]any{rpmregistrytest.Selects(rpmregistrytest.KeyAFingerprint, nil)}, KeyFile: rpmregistrytest.KeyA()}
	srv, repo := startRegistry(t, reg)

	keys, err := FetchRPMKeys(t.Context(), srv.Client(), repo, "the-token")

	require.NoError(t, err)
	assert.Equal(t, []string{rpmregistrytest.KeyAFingerprint}, keys.Fingerprints)
	reread, err := CheckPublicKeys(keys.Armored, []string{rpmregistrytest.KeyAFingerprint})
	require.NoError(t, err)
	assert.Equal(t, keys.Armored, reread.Armored)
}

func TestFetchRPMKeys_Rotation(t *testing.T) {
	t.Parallel()
	reg := &rpmregistrytest.Registry{
		Settings: []map[string]any{rpmregistrytest.Selects(rpmregistrytest.KeyBFingerprint, rpmregistrytest.KeyAFingerprint)},
		KeyFile:  append(rpmregistrytest.KeyB(), rpmregistrytest.KeyA()...),
	}
	srv, repo := startRegistry(t, reg)

	keys, err := FetchRPMKeys(t.Context(), srv.Client(), repo, "the-token")

	require.NoError(t, err)
	assert.Equal(t, []string{rpmregistrytest.KeyBFingerprint, rpmregistrytest.KeyAFingerprint}, keys.Fingerprints)
}

func TestFetchRPMKeys_MismatchIsRetriedOnce(t *testing.T) {
	t.Parallel()
	reg := &rpmregistrytest.Registry{
		Settings: []map[string]any{rpmregistrytest.Selects(rpmregistrytest.KeyBFingerprint, nil), rpmregistrytest.Selects(rpmregistrytest.KeyAFingerprint, nil)},
		KeyFile:  rpmregistrytest.KeyA(),
	}
	srv, repo := startRegistry(t, reg)

	keys, err := FetchRPMKeys(t.Context(), srv.Client(), repo, "the-token")

	require.NoError(t, err)
	assert.Equal(t, []string{rpmregistrytest.KeyAFingerprint}, keys.Fingerprints)
	assert.Equal(t, int32(2), reg.DetailRequests.Load())
}

func TestFetchRPMKeys_PersistentMismatchIsRefused(t *testing.T) {
	t.Parallel()
	reg := &rpmregistrytest.Registry{
		Settings: []map[string]any{rpmregistrytest.Selects(rpmregistrytest.KeyAFingerprint, nil)},
		KeyFile:  append(rpmregistrytest.KeyA(), rpmregistrytest.KeyB()...),
	}
	srv, repo := startRegistry(t, reg)

	keys, err := FetchRPMKeys(t.Context(), srv.Client(), repo, "the-token")

	mismatch, ok := errors.AsType[*KeyMismatchError](err)
	require.True(t, ok, "got %v", err)
	assert.Equal(t, []string{rpmregistrytest.KeyAFingerprint, rpmregistrytest.KeyBFingerprint}, mismatch.Served)
	assert.Equal(t, []string{rpmregistrytest.KeyAFingerprint}, mismatch.Expected)
	assert.Nil(t, keys)
	assert.Equal(t, int32(2), reg.DetailRequests.Load())
	assert.Equal(t, int32(2), reg.KeyRequests.Load())
}

func TestFetchRPMKeys_BadSettingsAreRefusedBeforeTheKeyFetch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		settings map[string]any
		want     string
	}{
		{"no settings", nil, "no signing key fingerprint"},
		{"lowercase fingerprint", rpmregistrytest.Selects(strings.ToLower(rpmregistrytest.KeyAFingerprint), nil), "not an uppercase 40-character hexadecimal fingerprint"},
		{"empty fingerprint", rpmregistrytest.Selects("", nil), "no signing key fingerprint"},
		{"bad secondary", rpmregistrytest.Selects(rpmregistrytest.KeyAFingerprint, "XYZ"), `selects "XYZ", which is not`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reg := &rpmregistrytest.Registry{Settings: []map[string]any{tc.settings}, KeyFile: rpmregistrytest.KeyA()}
			srv, repo := startRegistry(t, reg)

			keys, err := FetchRPMKeys(t.Context(), srv.Client(), repo, "the-token")

			require.ErrorContains(t, err, tc.want)
			assert.Nil(t, keys)
			assert.Equal(t, int32(1), reg.DetailRequests.Load(), "only a mismatch is retried")
			assert.Zero(t, reg.KeyRequests.Load())
		})
	}
}

func TestFetchRPMKeys_NotAHostedRPMRepository(t *testing.T) {
	t.Parallel()

	tests := []struct {
		format, kind string
		want         string
	}{
		{"npm", "hosted", "is a hosted npm repository, not a hosted rpm one"},
		{"rpm", "remote", "is a remote rpm repository, not a hosted rpm one"},
	}
	for _, tc := range tests {
		t.Run(tc.kind+" "+tc.format, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				_ = json.NewEncoder(w).Encode(map[string]any{"format": tc.format, "kind": tc.kind, "settings": rpmregistrytest.Selects(rpmregistrytest.KeyAFingerprint, nil)})
			}))
			t.Cleanup(srv.Close)
			_, err := FetchRPMKeys(t.Context(), srv.Client(), repoAt(t, srv), "the-token")

			require.ErrorContains(t, err, tc.want)
			assert.Equal(t, int32(1), requests.Load(), "the key file is not fetched and the error is not retried")
		})
	}
}

func TestFetchRPMKeys_OversizeDetailIsRefused(t *testing.T) {
	t.Parallel()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat(" ", maxRegistryResponseBytes+1)))
	}))
	t.Cleanup(srv.Close)
	_, err := FetchRPMKeys(t.Context(), srv.Client(), repoAt(t, srv), "the-token")

	assert.ErrorContains(t, err, "response is larger than 1048576 bytes")
}

func TestFetchRPMKeys_OversizeKeyFileIsRefused(t *testing.T) {
	t.Parallel()
	reg := &rpmregistrytest.Registry{
		Settings: []map[string]any{rpmregistrytest.Selects(rpmregistrytest.KeyAFingerprint, nil)},
		KeyFile:  []byte(strings.Repeat("A", maxRegistryResponseBytes+1)),
	}
	srv, repo := startRegistry(t, reg)

	_, err := FetchRPMKeys(t.Context(), srv.Client(), repo, "the-token")

	require.ErrorContains(t, err, "response is larger than 1048576 bytes")
	assert.Equal(t, int32(1), reg.DetailRequests.Load(), "only a mismatch is retried")
	assert.Equal(t, int32(1), reg.KeyRequests.Load())
}

func TestFetchRPMKeys_SubkeyIsRefused(t *testing.T) {
	t.Parallel()
	reg := &rpmregistrytest.Registry{Settings: []map[string]any{rpmregistrytest.Selects(rpmregistrytest.KeyAFingerprint, nil)}, KeyFile: rpmregistrytest.KeySub()}
	srv, repo := startRegistry(t, reg)

	keys, err := FetchRPMKeys(t.Context(), srv.Client(), repo, "the-token")

	require.ErrorIs(t, err, errMalformedKey)
	require.ErrorContains(t, err, "has a subkey")
	assert.Nil(t, keys)
	assert.Equal(t, int32(1), reg.DetailRequests.Load(), "only a mismatch is retried")
	assert.Equal(t, int32(1), reg.KeyRequests.Load())
}

func TestFetchRPMKeys_DoesNotFollowRedirects(t *testing.T) {
	t.Parallel()
	var elsewhere atomic.Int32
	other := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { elsewhere.Add(1) }))
	t.Cleanup(other.Close)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	_, err := FetchRPMKeys(t.Context(), srv.Client(), repoAt(t, srv), "the-token")

	require.ErrorContains(t, err, "302 Found")
	assert.Zero(t, elsewhere.Load())
}

func TestFetchRPMKeys_ErrorStatusDoesNotLeakTheToken(t *testing.T) {
	t.Parallel()
	reg := &rpmregistrytest.Registry{Settings: []map[string]any{rpmregistrytest.Selects(rpmregistrytest.KeyAFingerprint, nil)}}
	srv, repo := startRegistry(t, reg)

	_, err := FetchRPMKeys(t.Context(), srv.Client(), repo, "wrong-token")

	require.ErrorContains(t, err, "401 Unauthorized")
	assert.NotContains(t, err.Error(), "wrong-token")
}
