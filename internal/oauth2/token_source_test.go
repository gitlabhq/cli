//go:build !integration

package oauth2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"

	"gitlab.com/gitlab-org/cli/internal/config"
)

const testHost = "gitlab.com"

// newKeyringConfig writes a keyring-backed OAuth config for testHost to a temp
// directory and returns a config re-read from that directory, so its backing
// dir is set and reload() works. The keyring is mocked per-test.
func newKeyringConfig(t *testing.T, accessToken, refreshToken string, expiry time.Time) config.Config {
	t.Helper()
	return newKeyringConfigInDir(t, t.TempDir(), accessToken, refreshToken, expiry)
}

// tokenSourceForServer builds a configTokenSource whose token endpoint points at
// srv, bypassing NewConfigTokenSource so the test controls the refresh response.
func tokenSourceForServer(cfg config.Config, srv *httptest.Server) *configTokenSource {
	return &configTokenSource{
		cfg:        cfg,
		httpClient: srv.Client(),
		hostname:   testHost,
		oauth2Config: &oauth2.Config{
			ClientID: "test-client",
			Endpoint: oauth2.Endpoint{TokenURL: srv.URL + "/oauth/token"},
			Scopes:   scopes,
		},
	}
}

// withoutAdoptBackoff removes the inter-attempt sleep in adopt() so a test that
// exhausts the retries does not pause for adoptAttempts*adoptBackoff.
func withoutAdoptBackoff(t *testing.T) {
	t.Helper()
	old := adoptBackoff
	adoptBackoff = 0
	t.Cleanup(func() { adoptBackoff = old })
}

func writeTokenResponse(t *testing.T, w http.ResponseWriter, accessToken, refreshToken string, expiresIn int) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	// assert, not require: this runs in the server's handler goroutine, where
	// require's FailNow (runtime.Goexit) would exit the wrong goroutine.
	assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"token_type":    "bearer",
		"expires_in":    expiresIn,
	}))
}

func writeInvalidGrant(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	// assert, not require: this runs in the server's handler goroutine.
	assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
		"error":             "invalid_grant",
		"error_description": "The provided authorization grant is invalid, expired, or revoked.",
	}))
}

func TestToken_SkipsRefreshWhenFreshestTokenIsValid(t *testing.T) {
	cfg := newKeyringConfig(t, "access-current", "refresh-current", time.Now().Add(time.Hour))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("token endpoint should not be called when the token is still valid; got %s", r.URL.Path)
		writeInvalidGrant(t, w)
	}))
	defer srv.Close()

	ts := tokenSourceForServer(cfg, srv)
	token, err := ts.Token()
	require.NoError(t, err)
	assert.Equal(t, "access-current", token.AccessToken)
}

func TestToken_RefreshRotatesAndPersists(t *testing.T) {
	cfg := newKeyringConfig(t, "access-old", "refresh-old", time.Now().Add(-time.Hour))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !assert.NoError(t, r.ParseForm()) {
			return
		}
		assert.Equal(t, "refresh_token", r.Form.Get("grant_type"))
		assert.Equal(t, "refresh-old", r.Form.Get("refresh_token"))
		writeTokenResponse(t, w, "access-new", "refresh-new", 7200)
	}))
	defer srv.Close()

	ts := tokenSourceForServer(cfg, srv)
	token, err := ts.Token()
	require.NoError(t, err)
	assert.Equal(t, "access-new", token.AccessToken)
	assert.Equal(t, "refresh-new", token.RefreshToken)

	// The rotated credentials are persisted to the keyring, not just returned.
	storedAccess, err := keyring.Get("glab:gitlab.com:token", "")
	require.NoError(t, err)
	assert.Equal(t, "access-new", storedAccess)
	storedRefresh, err := keyring.Get("glab:gitlab.com:oauth2_refresh_token", "")
	require.NoError(t, err)
	assert.Equal(t, "refresh-new", storedRefresh)
}

func TestToken_AdoptsConcurrentlyRotatedTokenOnInvalidGrant(t *testing.T) {
	dir := t.TempDir()
	cfg := newKeyringConfigInDir(t, dir, "access-old", "refresh-old", time.Now().Add(-time.Hour))

	// The handler models a concurrent process that won the refresh race: it
	// commits a fresh, valid token to the keyring and config file, then returns
	// invalid_grant to us because our refresh token is now consumed.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// assert, not require: this runs in the server's handler goroutine. Return
		// early if the config cannot be re-read so we do not deref a nil winner.
		winner, err := config.ParseConfig(filepath.Join(dir, "config.yml"))
		if !assert.NoError(t, err) {
			writeInvalidGrant(t, w)
			return
		}
		assert.NoError(t, winner.Set(testHost, "token", "access-winner"))
		assert.NoError(t, winner.Set(testHost, "oauth2_refresh_token", "refresh-winner"))
		assert.NoError(t, winner.Set(testHost, "oauth2_expiry_date", time.Now().Add(time.Hour).Format(time.RFC3339)))
		assert.NoError(t, winner.Write())

		writeInvalidGrant(t, w)
	}))
	defer srv.Close()

	ts := tokenSourceForServer(cfg, srv)
	token, err := ts.Token()
	require.NoError(t, err)
	assert.Equal(t, "access-winner", token.AccessToken, "should adopt the token the winning process rotated")
}

func TestToken_ReturnsErrorWhenInvalidGrantWithoutRecovery(t *testing.T) {
	withoutAdoptBackoff(t)
	cfg := newKeyringConfig(t, "access-old", "refresh-old", time.Now().Add(-time.Hour))

	// A genuinely revoked/expired session: invalid_grant with no valid token ever
	// appearing on disk. We must surface the error rather than loop forever.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeInvalidGrant(t, w)
	}))
	defer srv.Close()

	ts := tokenSourceForServer(cfg, srv)
	_, err := ts.Token()
	require.Error(t, err)
	assert.True(t, isInvalidGrant(err), "expected the original invalid_grant error to be surfaced")
}

func TestIsInvalidGrant(t *testing.T) {
	assert.True(t, isInvalidGrant(&oauth2.RetrieveError{ErrorCode: "invalid_grant"}))
	assert.False(t, isInvalidGrant(&oauth2.RetrieveError{ErrorCode: "invalid_client"}))
	assert.False(t, isInvalidGrant(&url.Error{Op: "Post", Err: assert.AnError}))
	assert.False(t, isInvalidGrant(nil))
}

// TestToken_ActsOnFreshestStateNotStartupCopy verifies that a token source
// constructed with a stale startup config acts on the freshest persisted state:
// when another process has rotated the credentials to a valid token, Token()
// returns that token and never contacts the endpoint. Covered for both storage
// modes because a keyring host reads its secrets live while a file-backed host
// relies on the config re-read.
func TestToken_ActsOnFreshestStateNotStartupCopy(t *testing.T) {
	for _, tc := range []struct {
		name       string
		useKeyring bool
	}{
		{"keyring-backed", true},
		{"file-backed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keyring.MockInit()
			t.Cleanup(keyring.MockInit)
			dir := t.TempDir()

			// Startup state persisted to disk: an already-expired token, then loaded
			// into a config that stands in for a long-lived process's in-memory copy.
			writeOAuthConfig(t, dir, tc.useKeyring, "access-startup", "refresh-startup", time.Now().Add(-time.Hour))
			startup, err := config.ParseConfig(filepath.Join(dir, "config.yml"))
			require.NoError(t, err)

			// Another process rotates the credentials to a fresh, valid token.
			writeOAuthConfig(t, dir, tc.useKeyring, "access-fresh", "refresh-fresh", time.Now().Add(time.Hour))

			// The freshest token is valid, so the endpoint must not be contacted.
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("token endpoint should not be called; the freshest token is valid")
				writeInvalidGrant(t, w)
			}))
			defer srv.Close()

			ts := tokenSourceForServer(startup, srv)
			token, err := ts.Token()
			require.NoError(t, err)
			assert.Equal(t, "access-fresh", token.AccessToken,
				"Token must act on the freshest persisted state, not the startup in-memory copy")
		})
	}
}

// TestToken_RefreshDoesNotOverwriteConcurrentConfigWrites verifies that a
// refresh persists through a freshly re-read config, so a write another process
// made after this process loaded its config (here, rotating a second host's
// token) is not rolled back when the refresh writes. The narrow window of a
// write landing *during* the network refresh is closed by the cross-process
// lock added in !3679 (mechanism A).
func TestToken_RefreshDoesNotOverwriteConcurrentConfigWrites(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)
	dir := t.TempDir()
	const otherHost = "other.example.com"

	// Our host starts with an expired token (file-backed so credentials live in
	// the document being serialized).
	writeOAuthConfig(t, dir, false, "access-old", "refresh-old", time.Now().Add(-time.Hour))
	startup, err := config.ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)

	// Another process adds/rotates a second host's token on disk after we loaded.
	other, err := config.ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)
	require.NoError(t, other.Set(otherHost, "token", "other-rotated"))
	require.NoError(t, other.Write())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeTokenResponse(t, w, "access-new", "refresh-new", 7200)
	}))
	defer srv.Close()

	ts := tokenSourceForServer(startup, srv)
	_, err = ts.Token()
	require.NoError(t, err)

	onDisk, err := config.ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)

	ourToken, _, err := onDisk.GetWithSource(testHost, "token", false)
	require.NoError(t, err)
	assert.Equal(t, "access-new", ourToken, "our refreshed token should be persisted")

	otherToken, _, err := onDisk.GetWithSource(otherHost, "token", false)
	require.NoError(t, err)
	assert.Equal(t, "other-rotated", otherToken,
		"a config write from another process must survive the refresh")
}

// TestNewConfigTokenSource_IgnoresEnvironmentToken pins the fix for the gap
// Timo flagged on !3704: api.WithoutTokenFromEnvironment set
// searchEnvForIdentity=false for the token and is_oauth2 lookups in
// api.NewClientFromConfig, but NewConfigTokenSource still read "token" through
// cfg.Get, which always searches GITLAB_TOKEN/GITLAB_ACCESS_TOKEN/OAUTH_TOKEN
// regardless of that option. A caller like the Docker credential helper,
// which runs as a subprocess inheriting the user's shell environment, could
// therefore mint a request using a stray environment token even though it
// asked not to.
func TestNewConfigTokenSource_IgnoresEnvironmentToken(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		searchEnvForIdentity bool
		want                 string
	}{
		{"searches env when asked to", true, "env-token"},
		{"ignores env when told not to", false, "config-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GITLAB_TOKEN", "env-token")

			expiry := time.Now().Add(time.Hour).Format(time.RFC3339)
			cfg := config.NewFromString(`
---
hosts:
  ` + testHost + `:
    is_oauth2: "true"
    client_id: abc
    token: config-token
    oauth2_refresh_token: refresh-token
    oauth2_expiry_date: ` + expiry + `
`)

			ts, err := NewConfigTokenSource(cfg, &http.Client{}, "https", testHost, tc.searchEnvForIdentity)
			require.NoError(t, err)

			token, err := ts.Token()
			require.NoError(t, err)
			assert.Equal(t, tc.want, token.AccessToken)
		})
	}
}

// newKeyringConfigInDir is newKeyringConfig with a caller-provided directory, so
// a test can also open a second config pointing at the same files.
func newKeyringConfigInDir(t *testing.T, dir, accessToken, refreshToken string, expiry time.Time) config.Config {
	t.Helper()

	keyring.MockInit()
	t.Cleanup(keyring.MockInit)

	seed := config.NewBlankConfigInDir(dir)
	require.NoError(t, seed.Set(testHost, "use_keyring", "true"))
	require.NoError(t, seed.Set(testHost, "is_oauth2", "true"))
	require.NoError(t, seed.Set(testHost, "oauth2_refresh_token", refreshToken))
	require.NoError(t, seed.Set(testHost, "token", accessToken))
	require.NoError(t, seed.Set(testHost, "oauth2_expiry_date", expiry.Format(time.RFC3339)))
	require.NoError(t, seed.Write())

	cfg, err := config.ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)
	return cfg
}

// writeOAuthConfig persists an OAuth config for testHost to dir in either
// keyring or plaintext-file storage mode. Callers re-read it with
// config.ParseConfig. The keyring must already be mocked by the caller.
func writeOAuthConfig(t *testing.T, dir string, useKeyring bool, accessToken, refreshToken string, expiry time.Time) {
	t.Helper()

	seed := config.NewBlankConfigInDir(dir)
	if useKeyring {
		require.NoError(t, seed.Set(testHost, "use_keyring", "true"))
	}
	require.NoError(t, seed.Set(testHost, "is_oauth2", "true"))
	require.NoError(t, seed.Set(testHost, "oauth2_refresh_token", refreshToken))
	require.NoError(t, seed.Set(testHost, "token", accessToken))
	require.NoError(t, seed.Set(testHost, "oauth2_expiry_date", expiry.Format(time.RFC3339)))
	require.NoError(t, seed.Write())
}
