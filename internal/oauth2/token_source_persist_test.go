//go:build !integration

package oauth2

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"gitlab.com/gitlab-org/cli/internal/config"
)

// The refresh must not be attempted at all when the write would fail, because
// the network call spends the refresh token.
func TestConfigTokenSource_DoesNotConsumeRefreshTokenWhenItCannotPersist(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)

	// Parent is a regular file, so MkdirAll fails with ENOTDIR even as root.
	blocker := filepath.Join(t.TempDir(), "blocker")
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))

	cfg := config.NewFromStringInDir(`
---
hosts:
  gitlab.com:
    is_oauth2: "true"
    token: access-old
    oauth2_refresh_token: refresh-old
    oauth2_expiry_date: `+time.Now().Add(-time.Hour).Format(time.RFC3339)+`
`, filepath.Join(blocker, "glab-cli"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// assert, not require: this runs in the server's handler goroutine.
		assert.Fail(t, "the token endpoint must not be called when the rotated token cannot be persisted")
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	_, err := tokenSourceForServer(cfg, srv).Token()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not be saved")

	// Still unspent: the user recovers by fixing the directory, not re-logging in.
	refresh, err := cfg.Get(testHost, "oauth2_refresh_token")
	require.NoError(t, err)
	assert.Equal(t, "refresh-old", refresh)
}

// The probe must not stand in the way of a refresh that would have succeeded.
func TestConfigTokenSource_RefreshesNormallyWhenCredentialsCanBePersisted(t *testing.T) {
	dir := t.TempDir()
	cfg := newKeyringConfigInDir(t, dir, "access-old", "refresh-old", time.Now().Add(-time.Hour))

	var refreshed bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshed = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"access-new","refresh_token":"refresh-new","token_type":"bearer","expires_in":7200}`))
	}))
	defer srv.Close()

	token, err := tokenSourceForServer(cfg, srv).Token()
	require.NoError(t, err)
	require.True(t, refreshed, "expected the expired token to be refreshed")
	assert.Equal(t, "access-new", token.AccessToken)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotEqual(t, ".glab-write-probe", e.Name(), "probe file left behind")
	}
}
