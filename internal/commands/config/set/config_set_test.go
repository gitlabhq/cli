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
		{
			name:      "set key by alias",
			cli:       "visual vim -g",
			expectKey: "visual",
		},
		{
			name:      "set glab_pager",
			cli:       "glab_pager vim -g",
			expectKey: "glab_pager",
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

func TestConfigSet_TokenClearsStaleOAuth2Config(t *testing.T) {
	t.Parallel()

	cfg := configStub{"gitlab.com:is_oauth2": "true", "gitlab.com:oauth2_refresh_token": "stale-refresh", "gitlab.com:oauth2_expiry_date": "2026-01-01T00:00:00Z"}
	exec := cmdtest.SetupCmdForTest(t, NewCmdSet, true, cmdtest.WithConfig(cfg))

	out, err := exec("token glpat-nOtARealToken --host gitlab.com")
	require.NoError(t, err)

	assert.Equal(t, "glpat-nOtARealToken", cfg["gitlab.com:token"])
	assert.Empty(t, cfg["gitlab.com:is_oauth2"])
	assert.Empty(t, cfg["gitlab.com:oauth2_refresh_token"])
	assert.Empty(t, cfg["gitlab.com:oauth2_expiry_date"])
	assert.Empty(t, out.String(), "the notice belongs on stderr, not in command output")
	assert.Contains(t, out.Stderr(), "Cleared the OAuth configuration for gitlab.com")
}

func TestConfigSet_TokenLeavesNonOAuth2HostAlone(t *testing.T) {
	t.Parallel()

	cfg := configStub{"gitlab.com:is_oauth2": "false", "gitlab.com:job_token": "some-job-token"}
	exec := cmdtest.SetupCmdForTest(t, NewCmdSet, true, cmdtest.WithConfig(cfg))

	out, err := exec("token glpat-nOtARealToken --host gitlab.com")
	require.NoError(t, err)

	assert.Equal(t, "glpat-nOtARealToken", cfg["gitlab.com:token"])
	assert.Equal(t, "some-job-token", cfg["gitlab.com:job_token"], "clearing is scoped to the OAuth fields")
	assert.Empty(t, out.Stderr())
}

func TestConfigSet_NonTokenKeyLeavesOAuth2ConfigAlone(t *testing.T) {
	t.Parallel()

	cfg := configStub{"gitlab.com:is_oauth2": "true", "gitlab.com:oauth2_refresh_token": "live-refresh"}
	exec := cmdtest.SetupCmdForTest(t, NewCmdSet, true, cmdtest.WithConfig(cfg))

	out, err := exec("git_protocol ssh --host gitlab.com")
	require.NoError(t, err)

	assert.Equal(t, "true", cfg["gitlab.com:is_oauth2"])
	assert.Equal(t, "live-refresh", cfg["gitlab.com:oauth2_refresh_token"])
	assert.Empty(t, out.Stderr())
}

// failingSetConfig is a configStub whose Set always fails, so a test can
// assert on the error `config set` reports.
type failingSetConfig struct {
	configStub
	err error
}

func (c failingSetConfig) Set(host, key, value string) error {
	return c.err
}

func TestConfigSet_ErrorOmitsValue(t *testing.T) {
	t.Parallel()

	const secret = "glpat-nOtARealToken"
	cfg := failingSetConfig{configStub: configStub{}, err: errors.New("keyring is locked")}
	exec := cmdtest.SetupCmdForTest(t, NewCmdSet, true, cmdtest.WithConfig(cfg))

	_, err := exec("token " + secret + " --host gitlab.com")
	require.Error(t, err)

	assert.NotContains(t, err.Error(), secret, "credential must not be echoed into the error")
	assert.Contains(t, err.Error(), `failed to set "token"`)
	assert.Contains(t, err.Error(), "keyring is locked", "underlying cause must be preserved")
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

// State glab maintains for itself is read from the global file only, so
// `config set` must not put it in the repository's local file where nothing
// would read it again. configStub.Local returns nil, so a local write would not
// reach the config at all and the assertions below would not hold.
func TestConfigSet_GlobalOnlyKeyWritesGloballyWithoutTheFlag(t *testing.T) {
	t.Parallel()

	cfg := configStub{}
	exec := cmdtest.SetupCmdForTest(t, NewCmdSet, true, cmdtest.WithConfig(cfg))

	out, err := exec("duo_cli_binary_version 9.26.0")
	require.NoError(t, err)

	assert.Contains(t, out.Stderr(), "always stored in the global configuration",
		"the user must be told the write was routed to the global config")
	assert.Equal(t, "9.26.0", cfg["duo_cli_binary_version"],
		"the value must land in the global config, which is the one glab reads")
	assert.Equal(t, "true", cfg["_written"])
}

func TestConfigSet_ClientIDValidation(t *testing.T) {
	t.Parallel()

	validID := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	t.Run("accepts valid 64 hex characters", func(t *testing.T) {
		t.Parallel()

		cfg := configStub{}
		exec := cmdtest.SetupCmdForTest(t, NewCmdSet, true, cmdtest.WithConfig(cfg))

		out, err := exec("client_id " + validID + " --host salsa.debian.org -g")
		require.NoError(t, err)
		assert.Empty(t, out.String())
		assert.Equal(t, validID, cfg["salsa.debian.org:client_id"])
		assert.Equal(t, "true", cfg["_written"])
	})

	t.Run("allows clearing client_id with empty string", func(t *testing.T) {
		t.Parallel()

		cfg := configStub{"salsa.debian.org:client_id": validID}
		exec := cmdtest.SetupCmdForTest(t, NewCmdSet, true, cmdtest.WithConfig(cfg))

		out, err := exec(`client_id "" --host salsa.debian.org -g`)
		require.NoError(t, err)
		assert.Empty(t, out.String())
		assert.Empty(t, cfg["salsa.debian.org:client_id"])
		assert.Equal(t, "true", cfg["_written"])
	})

	t.Run("allows clearing client_id with whitespace string", func(t *testing.T) {
		t.Parallel()

		cfg := configStub{"salsa.debian.org:client_id": validID}
		exec := cmdtest.SetupCmdForTest(t, NewCmdSet, true, cmdtest.WithConfig(cfg))

		out, err := exec(`client_id "   " --host salsa.debian.org -g`)
		require.NoError(t, err)
		assert.Empty(t, out.String())
		assert.Empty(t, cfg["salsa.debian.org:client_id"])
		assert.Equal(t, "true", cfg["_written"])
	})

	t.Run("trims surrounding whitespace before storing", func(t *testing.T) {
		t.Parallel()

		cfg := configStub{}
		exec := cmdtest.SetupCmdForTest(t, NewCmdSet, true, cmdtest.WithConfig(cfg))

		out, err := exec("client_id '  " + validID + "  ' --host salsa.debian.org -g")
		require.NoError(t, err)
		assert.Empty(t, out.String())
		assert.Equal(t, validID, cfg["salsa.debian.org:client_id"])
		assert.Equal(t, "true", cfg["_written"])
	})

	t.Run("rejects personal access token", func(t *testing.T) {
		t.Parallel()

		cfg := configStub{}
		exec := cmdtest.SetupCmdForTest(t, NewCmdSet, true, cmdtest.WithConfig(cfg))

		_, err := exec("client_id glpat-1234567890abcdef1234567890abcdef1234567890abcdef1234567890 --host salsa.debian.org -g")
		require.Error(t, err)
		assert.Contains(t, err.Error(), `invalid value for "client_id": value is a personal access token (glpat-)`)
		assert.NotEqual(t, "true", cfg["_written"])
	})

	t.Run("rejects application secret", func(t *testing.T) {
		t.Parallel()

		cfg := configStub{}
		exec := cmdtest.SetupCmdForTest(t, NewCmdSet, true, cmdtest.WithConfig(cfg))

		_, err := exec("client_id gloas-1234567890abcdef1234567890abcdef1234567890abcdef1234567890 --host salsa.debian.org -g")
		require.Error(t, err)
		assert.Contains(t, err.Error(), `invalid value for "client_id": value is an OAuth application secret (gloas-)`)
		assert.NotEqual(t, "true", cfg["_written"])
	})

	t.Run("rejects short client_id", func(t *testing.T) {
		t.Parallel()

		cfg := configStub{}
		exec := cmdtest.SetupCmdForTest(t, NewCmdSet, true, cmdtest.WithConfig(cfg))

		_, err := exec("client_id 12345 --host salsa.debian.org -g")
		require.Error(t, err)
		assert.Contains(t, err.Error(), `invalid value for "client_id": OAuth application ID must be 64 hexadecimal characters`)
		assert.NotEqual(t, "true", cfg["_written"])
	})
}
