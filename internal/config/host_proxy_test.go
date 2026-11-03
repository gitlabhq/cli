package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveHostProxy(t *testing.T) {
	root := NewBlankRoot()
	cfg := NewConfig(root)

	// Ensure host exists
	require.NoError(t, cfg.Set("gitlab.com", "proxy", "https://proxy.local:3128"))

	value, err := ResolveHostProxy(cfg, "gitlab.com")
	require.NoError(t, err)
	require.NotNil(t, value)
	assert.Equal(t, "https://proxy.local:3128", value.String())

	// Unknown host should return nil
	value, err = ResolveHostProxy(cfg, "unknown.example.com")
	require.NoError(t, err)
	assert.Nil(t, value)

	// Empty host should not fail
	value, err = ResolveHostProxy(cfg, "")
	require.NoError(t, err)
	assert.Nil(t, value)

	// Invalid URL should return an error
	require.NoError(t, cfg.Set("bad.example.com", "proxy", "://bad-url"))
	value, err = ResolveHostProxy(cfg, "bad.example.com")
	require.Error(t, err)
	assert.Nil(t, value)
}

func TestResolveHostProxyEnvAlias(t *testing.T) {
	t.Setenv("GLAB_PROXY", "http://env-proxy.local:3128")
	cfg := NewBlankConfig()
	require.NoError(t, cfg.Set("gitlab.com", "proxy", "https://proxy.local:3128"))

	value, err := ResolveHostProxy(cfg, "gitlab.com")
	require.NoError(t, err)
	require.NotNil(t, value)
	assert.Equal(t, "http://env-proxy.local:3128", value.String())
}
