package api

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/config"
)

// GitLab Duo Agent Platform workloads install a pinned glab and authenticate it
// without `glab auth login`. The configuration file and environment below
// mirror what gitlab-org/gitlab writes, so update them together:
//
//   - config.yml: GlabSetup#config_file_command in
//     ee/lib/gitlab/duo_workflow/glab_setup.rb
//   - environment: StartWorkflowService in
//     ee/app/services/ai/duo_workflows/start_workflow_service.rb
//
// A failure here means the next pin bump in gitlab-org/gitlab breaks API calls
// from those workloads (see https://gitlab.com/gitlab-org/cli/-/work_items/8482).
func TestNewClientFromConfig_DuoAgentPlatformContract(t *testing.T) {
	for _, kd := range config.KeySchema {
		for _, k := range kd.EnvVars {
			t.Setenv(k, "")
		}
	}
	t.Setenv("GLAB_ENABLE_CI_AUTOLOGIN", "")
	t.Setenv("GLAB_CONFIG_DIR", "")

	t.Setenv("GITLAB_TOKEN", "doorkeeper-access-token")
	t.Setenv("GLAB_IS_OAUTH2", "true")

	tests := []struct {
		name            string
		host            string
		protocol        string
		subfolder       string
		expectedBaseURL string
	}{
		{
			name:            "default HTTPS port",
			host:            "gitlab.example.com",
			protocol:        "https",
			expectedBaseURL: "https://gitlab.example.com/api/v4/",
		},
		{
			name:            "non-default port",
			host:            "gitlab.example.com:8443",
			protocol:        "https",
			expectedBaseURL: "https://gitlab.example.com:8443/api/v4/",
		},
		{
			name:            "relative URL root",
			host:            "gitlab.example.com",
			protocol:        "https",
			subfolder:       "gitlab",
			expectedBaseURL: "https://gitlab.example.com/gitlab/api/v4/",
		},
		{
			name:            "HTTP instance",
			host:            "gdk.test:3000",
			protocol:        "http",
			expectedBaseURL: "http://gdk.test:3000/api/v4/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.NewFromString(duoAgentPlatformConfig(tt.host, tt.protocol, tt.subfolder))

			client, err := NewClientFromConfig(tt.host, cfg, false, "test-agent")
			require.NoError(t, err)

			key, value, err := client.AuthSource().Header(t.Context())
			require.NoError(t, err)
			assert.Equal(t, gitlab.OAuthTokenHeaderName, key)
			assert.Equal(t, "Bearer doorkeeper-access-token", value)
			assert.Equal(t, tt.expectedBaseURL, client.BaseURL())
		})
	}
}

// Matches the Ruby `to_yaml` output of GlabSetup#config_file_command.
func duoAgentPlatformConfig(host, protocol, subfolder string) string {
	var b strings.Builder
	b.WriteString("---\nhosts:\n  " + host + ":\n")
	b.WriteString("    is_oauth2: 'true'\n")
	b.WriteString("    client_id: bypass\n")
	b.WriteString("    oauth2_refresh_token: ''\n")
	b.WriteString("    oauth2_expiry_date: 01 Jan 2050 00:00 UTC\n")
	b.WriteString("    api_host: " + host + "\n")
	b.WriteString("    api_protocol: " + protocol + "\n")
	b.WriteString("    user: duo-user\n")
	if subfolder != "" {
		b.WriteString("    subfolder: " + subfolder + "\n")
	}
	b.WriteString("check_update: 'false'\n")
	b.WriteString("git_protocol: " + protocol + "\n")
	return b.String()
}
