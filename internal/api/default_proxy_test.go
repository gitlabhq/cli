//go:build !integration

package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/config"
)

func TestProxyFromConfig(t *testing.T) {
	cfg := config.NewBlankConfig()
	require.NoError(t, cfg.Set("gitlab.example.com", "proxy", "https://proxy.local:8443"))

	proxy, err := ProxyFromConfig(cfg, "gitlab.example.com")
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://gitlab.example.com/api/v4", http.NoBody)
	require.NoError(t, err)

	proxyURL, err := proxy(req)
	require.NoError(t, err)
	require.NotNil(t, proxyURL)
	assert.Equal(t, "https://proxy.local:8443", proxyURL.String())
}

func TestProxyFromConfigFallbacks(t *testing.T) {
	cfg := config.NewBlankConfig()

	t.Run("empty host", func(t *testing.T) {
		proxy, err := ProxyFromConfig(cfg, "")
		require.NoError(t, err)
		require.NotNil(t, proxy)
	})

	t.Run("missing host", func(t *testing.T) {
		proxy, err := ProxyFromConfig(cfg, "missing.example.com")
		require.NoError(t, err)
		require.NotNil(t, proxy)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://missing.example.com", http.NoBody)
		require.NoError(t, err)
		got, err := proxy(req)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("invalid configured proxy", func(t *testing.T) {
		require.NoError(t, cfg.Set("bad.example.com", "proxy", "://bad-url"))
		proxy, err := ProxyFromConfig(cfg, "bad.example.com")
		require.Error(t, err)
		assert.Nil(t, proxy)
	})
}
