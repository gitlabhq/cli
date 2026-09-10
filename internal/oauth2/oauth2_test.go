//go:build !integration

package oauth2

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/config"
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

	t.Run("self-managed with no client_id configured resolves to empty, not an error", func(t *testing.T) {
		// oauthClientID only looks up what's already configured; deciding what
		// to do about an empty result (prompt vs. fail) is resolveClientID's
		// job, tested separately in prompt_test.go.
		cfg := stubConfig{
			hosts: map[string]map[string]string{
				"salsa.debian.org": {},
			},
		}
		clientID, err := oauthClientID(cfg, "salsa.debian.org")
		require.NoError(t, err)
		assert.Empty(t, clientID)
	})
}

// A client_id supplied via GITLAB_CLIENT_ID must resolve like a configured
// value and never reach the interactive prompt. Uses the real config package
// rather than stubConfig because the env short-circuit lives in
// fileConfig.Get, not in this package.
func TestClientID_GITLAB_CLIENT_ID_ShortCircuitsInteractivePrompt(t *testing.T) {
	t.Setenv("GITLAB_CLIENT_ID", "from-env-123")
	cfg := config.NewBlankConfig()

	clientID, err := oauthClientID(cfg, "salsa.debian.org")
	require.NoError(t, err)
	assert.Equal(t, "from-env-123", clientID)

	// A non-interactive io would surface the non-interactive error instead
	// of the env value if resolveClientID ever reached the prompt.
	clientID, persist, err := resolveClientID(t.Context(), cfg, newNonInteractiveIOStreams(), "salsa.debian.org")
	require.NoError(t, err)
	assert.Equal(t, "from-env-123", clientID)
	assert.NoError(t, persist(), "an already-resolved value's persist func must be a no-op")
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
