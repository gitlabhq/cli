package api

import (
	"errors"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/config"
)

func TestBuildInfoUserAgent(t *testing.T) {
	tests := []struct {
		name       string
		buildInfo  BuildInfo
		expectedUA string
	}{
		{
			name: "without coding agent",
			buildInfo: BuildInfo{
				Version:      "1.50.0",
				Platform:     "linux",
				Architecture: "amd64",
			},
			expectedUA: "glab/1.50.0 (linux, amd64)",
		},
		{
			name: "with coding agent",
			buildInfo: BuildInfo{
				Version:      "1.50.0",
				Platform:     "darwin",
				Architecture: "arm64",
				CodingAgent:  "claude-code",
			},
			expectedUA: "glab/1.50.0 (darwin, arm64) Coding-Agent/claude-code",
		},
		{
			name: "empty coding agent omits suffix",
			buildInfo: BuildInfo{
				Version:      "DEV",
				Platform:     "windows",
				Architecture: "amd64",
				CodingAgent:  "",
			},
			expectedUA: "glab/DEV (windows, amd64)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectedUA, tt.buildInfo.UserAgent())
		})
	}
}

func TestNewClientFromConfig(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "")
	t.Setenv("GITLAB_ACCESS_TOKEN", "")
	t.Setenv("OAUTH_TOKEN", "")
	t.Setenv("GLAB_IS_OAUTH2", "")
	t.Setenv("JOB_TOKEN", "")
	t.Setenv("CI_JOB_TOKEN", "")
	t.Setenv("GITLAB_CI", "")
	t.Setenv("GLAB_ENABLE_CI_AUTOLOGIN", "")
	t.Setenv("CI_SERVER_FQDN", "")
	t.Setenv("CI_SERVER_PROTOCOL", "")
	t.Setenv("GITLAB_HOST", "")
	t.Setenv("GITLAB_API_HOST", "")
	t.Setenv("API_PROTOCOL", "")

	tests := []struct {
		name            string
		envVars         map[string]string
		repoHost        string
		expectedAuthKey string
		expectedAuthVal string
		expectedBaseURL string
	}{
		{
			name: "OAuth2 access token",
			envVars: map[string]string{
				"GITLAB_TOKEN":   "oauth2-access-token",
				"GLAB_IS_OAUTH2": "true",
			},
			repoHost:        "example.com",
			expectedAuthKey: "Authorization",
			expectedAuthVal: "Bearer oauth2-access-token",
			expectedBaseURL: "https://example.com/api/v4/",
		},
		{
			name: "PAT auth",
			envVars: map[string]string{
				"GITLAB_TOKEN": "some-pat",
			},
			repoHost:        "example.com",
			expectedAuthKey: gitlab.AccessTokenHeaderName,
			expectedAuthVal: "some-pat",
			expectedBaseURL: "https://example.com/api/v4/",
		},
		{
			name: "job token from env without CI",
			envVars: map[string]string{
				"JOB_TOKEN": "my-job-token",
			},
			repoHost:        "example.com",
			expectedAuthKey: gitlab.JobTokenHeaderName,
			expectedAuthVal: "my-job-token",
			expectedBaseURL: "https://example.com/api/v4/",
		},
		{
			name: "custom protocol from env",
			envVars: map[string]string{
				"GITLAB_TOKEN": "my-pat",
				"API_PROTOCOL": "http",
			},
			repoHost:        "example.com",
			expectedAuthKey: gitlab.AccessTokenHeaderName,
			expectedAuthVal: "my-pat",
			expectedBaseURL: "http://example.com/api/v4/",
		},
		{
			name: "CI auto-login uses CI variables",
			envVars: map[string]string{
				"GLAB_ENABLE_CI_AUTOLOGIN": "true",
				"GITLAB_CI":                "true",
				"CI_JOB_TOKEN":             "ci-tok",
				"CI_SERVER_FQDN":           "ci.example.com",
			},
			repoHost:        "example.com",
			expectedAuthKey: gitlab.JobTokenHeaderName,
			expectedAuthVal: "ci-tok",
			expectedBaseURL: "https://ci.example.com/api/v4/",
		},
		{
			name: "CI auto-login with custom protocol",
			envVars: map[string]string{
				"GLAB_ENABLE_CI_AUTOLOGIN": "true",
				"GITLAB_CI":                "true",
				"CI_JOB_TOKEN":             "ci-tok",
				"CI_SERVER_FQDN":           "ci.example.com",
				"CI_SERVER_PROTOCOL":       "http",
			},
			repoHost:        "example.com",
			expectedAuthKey: gitlab.JobTokenHeaderName,
			expectedAuthVal: "ci-tok",
			expectedBaseURL: "http://ci.example.com/api/v4/",
		},
		{
			name: "CI auto-login PAT takes precedence over job token",
			envVars: map[string]string{
				"GLAB_ENABLE_CI_AUTOLOGIN": "true",
				"GITLAB_CI":                "true",
				"GITLAB_TOKEN":             "my-pat",
				"CI_JOB_TOKEN":             "ci-tok",
				"CI_SERVER_FQDN":           "ci.example.com",
			},
			repoHost:        "example.com",
			expectedAuthKey: gitlab.AccessTokenHeaderName,
			expectedAuthVal: "my-pat",
			expectedBaseURL: "https://ci.example.com/api/v4/",
		},
		{
			name: "CI auto-login disabled falls back to PAT and passed-in host",
			envVars: map[string]string{
				"GITLAB_CI":                "true",
				"GLAB_ENABLE_CI_AUTOLOGIN": "false",
				"GITLAB_TOKEN":             "my-pat",
				"CI_JOB_TOKEN":             "ci-tok",
				"CI_SERVER_FQDN":           "ci.example.com",
			},
			repoHost:        "manual.example.com",
			expectedAuthKey: gitlab.AccessTokenHeaderName,
			expectedAuthVal: "my-pat",
			expectedBaseURL: "https://manual.example.com/api/v4/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			client, err := NewClientFromConfig(
				tt.repoHost,
				config.NewBlankConfig(),
				false,
				"test-agent",
			)
			require.NoError(t, err)

			key, value, err := client.AuthSource().Header(t.Context())
			require.NoError(t, err)
			assert.Equal(t, tt.expectedAuthKey, key)
			assert.Equal(t, tt.expectedAuthVal, value)
			assert.Equal(t, tt.expectedBaseURL, client.BaseURL())
		})
	}
}

func TestNewClientFromConfig_DuoWorkflowID(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "test-pat")
	t.Setenv("DUO_WORKFLOW_WORKFLOW_ID", "")

	baseURL, _ := url.Parse("https://example.com/api")

	t.Run("header injected when env var is set", func(t *testing.T) {
		t.Setenv("DUO_WORKFLOW_WORKFLOW_ID", "workflow-id-xyz")

		client, err := NewClientFromConfig("example.com", config.NewBlankConfig(), false, "test-agent")
		require.NoError(t, err)

		req, err := NewHTTPRequest(t.Context(), client, "GET", baseURL, nil, []string{}, false)
		require.NoError(t, err)
		assert.Equal(t, "workflow-id-xyz", req.Header.Get("X-Gitlab-Duo-Workflow-Id"))
	})

	t.Run("no header when env var is empty", func(t *testing.T) {
		t.Setenv("DUO_WORKFLOW_WORKFLOW_ID", "")

		client, err := NewClientFromConfig("example.com", config.NewBlankConfig(), false, "test-agent")
		require.NoError(t, err)

		req, err := NewHTTPRequest(t.Context(), client, "GET", baseURL, nil, []string{}, false)
		require.NoError(t, err)
		assert.Empty(t, req.Header.Get("X-Gitlab-Duo-Workflow-Id"))
	})

	t.Run("env var overrides X-Gitlab-Duo-Workflow-Id from config headers", func(t *testing.T) {
		t.Setenv("DUO_WORKFLOW_WORKFLOW_ID", "from-env")

		cfg := config.NewFromString(`
hosts:
  example.com:
    custom_headers:
      - name: X-Gitlab-Duo-Workflow-Id
        value: from-config
`)

		client, err := NewClientFromConfig("example.com", cfg, false, "test-agent")
		require.NoError(t, err)

		req, err := NewHTTPRequest(t.Context(), client, "GET", baseURL, nil, []string{}, false)
		require.NoError(t, err)
		assert.Equal(t, "from-env", req.Header.Get("X-Gitlab-Duo-Workflow-Id"))
	})

	t.Run("malformed workflow ID is ignored", func(t *testing.T) {
		t.Setenv("DUO_WORKFLOW_WORKFLOW_ID", "workflow\r\nid")

		client, err := NewClientFromConfig("example.com", config.NewBlankConfig(), false, "test-agent")
		require.NoError(t, err)

		req, err := NewHTTPRequest(t.Context(), client, "GET", baseURL, nil, []string{}, false)
		require.NoError(t, err)
		assert.Empty(t, req.Header.Get("X-Gitlab-Duo-Workflow-Id"))
	})
}

func TestNewClientFromConfig_OAuth2NoTokenReturnsError(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "")
	t.Setenv("GLAB_IS_OAUTH2", "true")

	_, err := NewClientFromConfig(
		"example.gitlab.com",
		config.NewBlankConfig(),
		false,
		"dummy user agent",
	)
	require.Error(t, err)
	// The message should be actionable rather than the old low-level jargon.
	assert.Contains(t, err.Error(), "re-authenticate")
}

func TestNewClientFromConfig_SurfacesKeyringReadError(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "")
	keyring.MockInitWithError(errors.New("keyring locked"))
	t.Cleanup(keyring.MockInit)

	cfg := config.NewBlankConfig()
	require.NoError(t, cfg.Set("example.gitlab.com", "use_keyring", "true"))

	// A failed keyring read must surface as a clear error rather than being
	// silently treated as an absent (empty) credential.
	_, err := NewClientFromConfig("example.gitlab.com", cfg, false, "dummy user agent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "keyring")
}
