package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/config"
)

// The OAuth flag can reach NewClientFromConfig from two places: the
// GLAB_IS_OAUTH2 environment variable, or is_oauth2 in the configuration file
// (where `glab auth login` persists it). The chosen auth scheme depends on
// which one supplied it, so pin every combination.
func TestNewClientFromConfig_OAuthFlagProvenance(t *testing.T) {
	for _, k := range []string{
		"GITLAB_TOKEN", "GITLAB_ACCESS_TOKEN", "OAUTH_TOKEN",
		"GLAB_IS_OAUTH2", "JOB_TOKEN", "CI_JOB_TOKEN", "GITLAB_CI",
	} {
		t.Setenv(k, "")
	}

	const storedOAuthHost = `hosts:
  example.com:
    is_oauth2: "true"
    api_host: example.com
    api_protocol: https
    user: someone
`

	const storedOAuthHostWithToken = `hosts:
  example.com:
    is_oauth2: "true"
    token: stored-oauth-token
    api_host: example.com
    api_protocol: https
    user: someone
`

	tests := []struct {
		name            string
		configYAML      string
		envVars         map[string]string
		expectedAuthKey string
		expectedAuthVal string
	}{
		{
			name:            "environment token alone is a PAT",
			envVars:         map[string]string{"GITLAB_TOKEN": "a-token"},
			expectedAuthKey: gitlab.AccessTokenHeaderName,
			expectedAuthVal: "a-token",
		},
		{
			name:            "environment token with environment OAuth flag is OAuth",
			envVars:         map[string]string{"GITLAB_TOKEN": "a-token", "GLAB_IS_OAUTH2": "true"},
			expectedAuthKey: gitlab.OAuthTokenHeaderName,
			expectedAuthVal: "Bearer a-token",
		},
		{
			// An environment token is assumed to be a PAT even when the host is
			// configured for OAuth, so that a glpat- in GITLAB_TOKEN is not sent
			// as a Bearer token. Changing this flips the header for anyone
			// injecting an OAuth token through the environment.
			name:            "environment token with stored OAuth flag is a PAT",
			configYAML:      storedOAuthHost,
			envVars:         map[string]string{"GITLAB_TOKEN": "a-token"},
			expectedAuthKey: gitlab.AccessTokenHeaderName,
			expectedAuthVal: "a-token",
		},
		{
			// The environment flag re-asserts OAuth, and is the supported escape
			// hatch for the row above.
			name:            "environment token with stored and environment OAuth flags is OAuth",
			configYAML:      storedOAuthHost,
			envVars:         map[string]string{"GITLAB_TOKEN": "a-token", "GLAB_IS_OAUTH2": "true"},
			expectedAuthKey: gitlab.OAuthTokenHeaderName,
			expectedAuthVal: "Bearer a-token",
		},
		{
			// The `glab auth login` path: both the flag and the token come from
			// the configuration file, so nothing is overridden.
			name:            "stored token with stored OAuth flag is OAuth",
			configYAML:      storedOAuthHostWithToken,
			expectedAuthKey: gitlab.OAuthTokenHeaderName,
			expectedAuthVal: "Bearer stored-oauth-token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			cfg := config.NewBlankConfig()
			if tt.configYAML != "" {
				cfg = config.NewFromString(tt.configYAML)
			}

			client, err := NewClientFromConfig("example.com", cfg, false, "test-agent")
			require.NoError(t, err)

			key, value, err := client.AuthSource().Header(t.Context())
			require.NoError(t, err)
			assert.Equal(t, tt.expectedAuthKey, key)
			assert.Equal(t, tt.expectedAuthVal, value)
		})
	}
}
