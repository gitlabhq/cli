//go:build !integration

package docker

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

	"gitlab.com/gitlab-org/cli/internal/config"
)

const (
	refreshTestHost     = "gitlab.example.com"
	refreshTestRegistry = "registry.gitlab.example.com"
)

// rewriteTransport sends every request to base instead of its real host, so the
// OAuth token endpoint derived from the config hostname hits an httptest server.
type rewriteTransport struct {
	base *url.URL
}

func (t rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.URL.Scheme = t.base.Scheme
	r.URL.Host = t.base.Host
	return http.DefaultTransport.RoundTrip(r)
}

// fileModeOAuthConfig writes a file-backed (use_keyring unset) OAuth config for
// refreshTestHost and returns it re-read from disk, so the config has a backing
// directory and Reload() returns a distinct object — as in real usage. The
// directory is returned so the test can inspect what was persisted.
func fileModeOAuthConfig(t *testing.T, accessToken, refreshToken string, expiry time.Time) (config.Config, string) {
	t.Helper()

	keyring.MockInit()
	t.Cleanup(keyring.MockInit)

	dir := t.TempDir()
	seed := config.NewBlankConfigInDir(dir)
	require.NoError(t, seed.Set(refreshTestHost, "is_oauth2", "true"))
	require.NoError(t, seed.Set(refreshTestHost, "client_id", "abc"))
	require.NoError(t, seed.Set(refreshTestHost, "user", "user1"))
	require.NoError(t, seed.Set(refreshTestHost, "container_registry_domains", refreshTestRegistry))
	require.NoError(t, seed.Set(refreshTestHost, "token", accessToken))
	require.NoError(t, seed.Set(refreshTestHost, "oauth2_refresh_token", refreshToken))
	require.NoError(t, seed.Set(refreshTestHost, "oauth2_expiry_date", expiry.Format(time.RFC3339)))
	require.NoError(t, seed.Write())

	cfg, err := config.ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)
	return cfg, dir
}

// The credential helper forces a refresh via the token source, then reads the
// access token back out of its own config. The refreshed token must be the one
// handed to Docker; returning the pre-refresh token hands Docker an already
// expired credential.
func TestHelper_GetReturnsRefreshedTokenForFileBackedHost(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "")

	cfg, dir := fileModeOAuthConfig(t, "access-old", "refresh-old", time.Now().Add(-time.Hour))

	var refreshed bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshed = true
		// assert, not require: this runs in the server's handler goroutine.
		assert.NoError(t, r.ParseForm())
		assert.Equal(t, "refresh_token", r.Form.Get("grant_type"))
		assert.Equal(t, "refresh-old", r.Form.Get("refresh_token"))

		w.Header().Set("Content-Type", "application/json")
		assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-new",
			"refresh_token": "refresh-new",
			"token_type":    "bearer",
			"expires_in":    7200,
		}))
	}))
	defer srv.Close()

	base, err := url.Parse(srv.URL)
	require.NoError(t, err)

	helper := Helper{
		cfg:    cfg,
		client: &http.Client{Transport: rewriteTransport{base: base}},
	}

	gotUser, gotPassword, err := helper.Get(refreshTestRegistry)
	require.NoError(t, err)
	require.True(t, refreshed, "expected the expired token to be refreshed")

	assert.Equal(t, "user1", gotUser)
	assert.Equal(t, "access-new", gotPassword,
		"Docker must receive the refreshed access token, not the expired one")

	// Sanity check: the refresh does reach disk. Only the in-memory copy that
	// helper.Get() reads back from lags behind.
	onDisk, err := config.ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)
	diskToken, _, err := onDisk.GetWithSource(refreshTestHost, "token", false)
	require.NoError(t, err)
	assert.Equal(t, "access-new", diskToken, "disk should hold the refreshed token")
}

// When the token is still valid no refresh happens, and if the config cannot be
// re-read (for example it lives in an XDG_CONFIG_DIRS entry with no user-level
// file, so no refresh ever created one) the helper must fall back to the
// in-memory copy rather than hard-failing. Modeled with a dir-backed config that
// was never written to disk, so Reload() cannot find config.yml.
func TestHelper_GetFallsBackToInMemoryConfigWhenReloadCannotFindFile(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "")
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)

	cfg := config.NewBlankConfigInDir(t.TempDir())
	require.NoError(t, cfg.Set(refreshTestHost, "is_oauth2", "true"))
	require.NoError(t, cfg.Set(refreshTestHost, "client_id", "abc"))
	require.NoError(t, cfg.Set(refreshTestHost, "user", "user1"))
	require.NoError(t, cfg.Set(refreshTestHost, "container_registry_domains", refreshTestRegistry))
	require.NoError(t, cfg.Set(refreshTestHost, "token", "valid-token"))
	require.NoError(t, cfg.Set(refreshTestHost, "oauth2_refresh_token", "refresh"))
	require.NoError(t, cfg.Set(refreshTestHost, "oauth2_expiry_date", time.Now().Add(time.Hour).Format(time.RFC3339)))

	// The token is valid, so no token-endpoint request should be made.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("token endpoint should not be called; the token is valid")
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	helper := Helper{cfg: cfg, client: srv.Client()}

	gotUser, gotPassword, err := helper.Get(refreshTestRegistry)
	require.NoError(t, err)
	assert.Equal(t, "user1", gotUser)
	assert.Equal(t, "valid-token", gotPassword)
}
