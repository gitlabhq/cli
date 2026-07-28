//go:build !integration

package oauth2

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/glinstance"
)

func TestClientID(t *testing.T) {
	testCasesTable := []struct {
		name             string
		hostname         string
		configClientID   string
		expectedClientID string
	}{
		{
			name:             "managed",
			hostname:         glinstance.DefaultHostname,
			configClientID:   "",
			expectedClientID: glinstance.DefaultClientID,
		},
		{
			name:             "self-managed-complete",
			hostname:         "salsa.debian.org",
			configClientID:   "321",
			expectedClientID: "321",
		},
	}

	for _, testCase := range testCasesTable {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := stubConfig{
				hosts: map[string]map[string]string{
					testCase.hostname: {
						"client_id": testCase.configClientID,
					},
				},
			}
			clientID, err := oauthClientID(cfg, testCase.hostname)
			require.NoError(t, err)
			assert.Equal(t, testCase.expectedClientID, clientID)
		})
	}

	t.Run("invalid self-managed config", func(t *testing.T) {
		cfg := stubConfig{
			hosts: map[string]map[string]string{
				"salsa.debian.org": {},
			},
		}
		clientID, err := oauthClientID(cfg, "salsa.debian.org")
		require.Error(t, err)
		assert.Empty(t, clientID)
	})
}

func TestOAuthBaseURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		subfolder   string
		expectedURL string
	}{
		{
			name:        "nested subfolder",
			subfolder:   "apps/gitlab",
			expectedURL: "https://gitlab.example.com/apps/gitlab",
		},
		{
			name:        "nested subfolder with surrounding slashes",
			subfolder:   "/apps/gitlab/",
			expectedURL: "https://gitlab.example.com/apps/gitlab",
		},
		{
			name:        "no subfolder",
			expectedURL: "https://gitlab.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := stubConfig{
				hosts: map[string]map[string]string{
					"gitlab.example.com": {
						"subfolder": tt.subfolder,
					},
				},
			}

			baseURL, err := oauthBaseURL(cfg, "gitlab.example.com")

			require.NoError(t, err)
			assert.Equal(t, tt.expectedURL, baseURL)
		})
	}
}

func TestOAuthBaseURLReturnsConfigError(t *testing.T) {
	t.Parallel()

	expectedErr := errors.New("read config")
	cfg := stubConfig{getErr: expectedErr}

	baseURL, err := oauthBaseURL(cfg, "gitlab.example.com")

	require.ErrorIs(t, err, expectedErr)
	assert.Empty(t, baseURL)
}
