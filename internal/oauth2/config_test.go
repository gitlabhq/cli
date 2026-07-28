//go:build !integration

package oauth2

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"

	"gitlab.com/gitlab-org/cli/internal/config"
)

type stubConfig struct {
	hosts map[string]map[string]string
}

func (s stubConfig) Get(host string, key string) (string, error) {
	return s.hosts[host][key], nil
}

func (s stubConfig) GetWithSource(string, string, bool) (string, string, error) { return "", "", nil }

func (s stubConfig) Set(host string, key string, value string) error {
	if _, ok := s.hosts[host]; !ok {
		s.hosts[host] = make(map[string]string)
	}
	s.hosts[host][key] = value

	return nil
}

func (s stubConfig) Hosts() ([]string, error)              { return nil, nil }
func (s stubConfig) Aliases() (*config.AliasConfig, error) { return nil, nil }
func (s stubConfig) Local() (*config.LocalConfig, error)   { return nil, nil }
func (s stubConfig) Write() error                          { return nil }
func (s stubConfig) WriteAll() error                       { return nil }

func TestConfig_unmarshal(t *testing.T) {
	tests := []struct {
		name           string
		expiryDate     string
		expectedFormat string
	}{
		{
			name:           "RFC3339",
			expiryDate:     "2023-03-13T15:47:00Z",
			expectedFormat: time.RFC3339,
		},
		{
			name:           "RFC822 with named timezone (legacy)",
			expiryDate:     "13 Mar 23 15:47 GMT",
			expectedFormat: time.RFC822,
		},
		{
			name:           "RFC822Z with numeric offset (legacy, bug #7810)",
			expiryDate:     "03 Apr 25 21:50 +0530",
			expectedFormat: time.RFC822Z,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := stubConfig{
				hosts: map[string]map[string]string{
					"gitlab.com": {
						"is_oauth2":            "true",
						"oauth2_refresh_token": "refresh_token",
						"token":                "access_token",
						"oauth2_expiry_date":   tt.expiryDate,
					},
				},
			}

			token, err := unmarshal("gitlab.com", cfg)
			require.NoError(t, err)

			assert.Equal(t, "refresh_token", token.RefreshToken)
			assert.Equal(t, "access_token", token.AccessToken)

			expectedDate, err := time.Parse(tt.expectedFormat, tt.expiryDate)
			require.NoError(t, err)

			assert.Equal(t, expectedDate, token.Expiry)
		})
	}
}

func TestConfig_marshal(t *testing.T) {
	cfg := stubConfig{
		hosts: map[string]map[string]string{},
	}

	token := &oauth2.Token{
		RefreshToken: "refresh_token",
		AccessToken:  "access_token",
		ExpiresIn:    60,
		Expiry:       time.Now().Add(60 * time.Second),
	}

	err := marshal("gitlab.com", cfg, token)
	require.NoError(t, err)

	require.Equal(t, map[string]map[string]string{
		"gitlab.com": {
			"is_oauth2":            "true",
			"oauth2_refresh_token": "refresh_token",
			"token":                "access_token",
			"oauth2_expiry_date":   token.Expiry.Format(time.RFC3339),
		},
	}, cfg.hosts)
}

func TestConfig_marshalPreservesKeyringRefreshTokenWhenReauthOmitsIt(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)

	cfg := config.NewBlankConfig()
	require.NoError(t, cfg.Set("gitlab.com", "use_keyring", "true"))
	require.NoError(t, cfg.Set("gitlab.com", "oauth2_refresh_token", "existing-refresh-token"))

	token := &oauth2.Token{
		AccessToken: "new-access-token",
		Expiry:      time.Now().Add(time.Hour),
	}

	require.NoError(t, marshal("gitlab.com", cfg, token))

	storedRefreshToken, err := keyring.Get("glab:gitlab.com:oauth2_refresh_token", "")
	require.NoError(t, err)
	assert.Equal(t, "existing-refresh-token", storedRefreshToken)

	persistedToken, err := unmarshal("gitlab.com", cfg)
	require.NoError(t, err)
	assert.Equal(t, "existing-refresh-token", persistedToken.RefreshToken)
	assert.Equal(t, "new-access-token", persistedToken.AccessToken)
}
