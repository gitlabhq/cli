//go:build !integration

package set

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

type configStub map[string]string

func (c configStub) Local() (*config.LocalConfig, error) {
	return nil, nil
}

func (c configStub) WriteAll() error {
	c["_written"] = "true"
	return nil
}

func (c configStub) Reload() (config.Config, error) {
	return c, nil
}

func genKey(host, key string) string {
	if host != "" {
		return host + ":" + key
	}
	return key
}

func (c configStub) Get(host, key string) (string, error) {
	val, _, err := c.GetWithSource(host, key, true)
	return val, err
}

func (c configStub) GetWithSource(host, key string, searchENVVars bool) (string, string, error) {
	if v, found := c[genKey(host, key)]; found {
		return v, "(memory)", nil
	}
	return "", "", errors.New("not found")
}

func (c configStub) Set(host, key, value string) error {
	c[genKey(host, key)] = value
	return nil
}

func (c configStub) Aliases() (*config.AliasConfig, error) {
	return nil, nil
}

func (c configStub) Hosts() ([]string, error) {
	return nil, nil
}

func (c configStub) Write() error {
	c["_written"] = "true"
	return nil
}

func TestConfigSet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		cli       string
		expectKey string
	}{
		{
			name:      "set key",
			cli:       "editor vim -g",
			expectKey: "editor",
		},
		{
			name:      "set key scoped by host",
			cli:       "editor vim --host gitlab.com -g",
			expectKey: "gitlab.com:editor",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := configStub{}
			exec := cmdtest.SetupCmdForTest(t, NewCmdSet, true, cmdtest.WithConfig(cfg))

			out, err := exec(tt.cli)
			require.NoError(t, err)

			assert.Empty(t, out.String())
			assert.Empty(t, out.Stderr())
			assert.Equal(t, "vim", cfg[tt.expectKey])
			assert.Equal(t, "true", cfg["_written"])
		})
	}
}

func TestConfigSet_RejectsUnknownKey(t *testing.T) {
	t.Parallel()

	cfg := configStub{}
	exec := cmdtest.SetupCmdForTest(t, NewCmdSet, true, cmdtest.WithConfig(cfg))

	_, err := exec(`oauth_scopes "openid profile" -g`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"oauth_scopes" is not a recognized glab config key`)

	_, present := cfg["oauth_scopes"]
	assert.False(t, present, "unknown key should not be stored")
	_, written := cfg["_written"]
	assert.False(t, written, "config should not be written when key is rejected")
}
