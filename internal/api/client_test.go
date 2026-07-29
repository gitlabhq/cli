package api

import (
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

func TestNewClientFromConfig_AuthContextPrecedence(t *testing.T) {
	tests := []struct {
		name                  string
		envVars               map[string]string
		expectedAuthKey       string
		expectedAuthValue     string
		expectedRefreshes     int64
		expectConfigUnchanged bool
	}{
		{
			name:                  "GITLAB_TOKEN PAT overrides stored OAuth",
			envVars:               map[string]string{"GITLAB_TOKEN": "env-pat"},
			expectedAuthKey:       gitlab.AccessTokenHeaderName,
			expectedAuthValue:     "env-pat",
			expectConfigUnchanged: true,
		},
		{
			name:                  "GITLAB_ACCESS_TOKEN PAT overrides stored OAuth",
			envVars:               map[string]string{"GITLAB_ACCESS_TOKEN": "env-pat"},
			expectedAuthKey:       gitlab.AccessTokenHeaderName,
			expectedAuthValue:     "env-pat",
			expectConfigUnchanged: true,
		},
		{
			name:              "OAUTH_TOKEN preserves stored OAuth context",
			envVars:           map[string]string{"OAUTH_TOKEN": "environment-oauth-token"},
			expectedAuthKey:   "Authorization",
			expectedAuthValue: "Bearer refreshed-access-token",
			expectedRefreshes: 1,
		},
		{
			name:              "stored OAuth is preserved without an override",
			expectedAuthKey:   "Authorization",
			expectedAuthValue: "Bearer refreshed-access-token",
			expectedRefreshes: 1,
		},
		{
			name: "explicit environment OAuth is preserved",
			envVars: map[string]string{
				"GITLAB_TOKEN":   "environment-oauth-token",
				"GLAB_IS_OAUTH2": "true",
			},
			expectedAuthKey:   "Authorization",
			expectedAuthValue: "Bearer refreshed-access-token",
			expectedRefreshes: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GITLAB_TOKEN", "")
			t.Setenv("GITLAB_ACCESS_TOKEN", "")
			t.Setenv("OAUTH_TOKEN", "")
			t.Setenv("GLAB_IS_OAUTH2", "")
			for key, value := range tt.envVars {
				t.Setenv(key, value)
			}

			var refreshRequests atomic.Int64
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				refreshRequests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"access_token":"refreshed-access-token","token_type":"Bearer","refresh_token":"refreshed-refresh-token","expires_in":3600}`)
			}))
			t.Cleanup(server.Close)

			hostname := strings.TrimPrefix(server.URL, "https://")
			configDir := t.TempDir()
			cfg := config.NewBlankConfigInDir(configDir)
			require.NoError(t, cfg.Set(hostname, "api_host", hostname))
			require.NoError(t, cfg.Set(hostname, "client_id", "test-client-id"))
			require.NoError(t, cfg.Set(hostname, "skip_tls_verify", "true"))
			require.NoError(t, cfg.Set(hostname, "is_oauth2", "true"))
			require.NoError(t, cfg.Set(hostname, "token", "stored-oauth-token"))
			require.NoError(t, cfg.Set(hostname, "oauth2_refresh_token", "stored-refresh-token"))
			require.NoError(t, cfg.Set(hostname, "oauth2_expiry_date", time.Now().Add(-time.Hour).Format(time.RFC3339)))
			require.NoError(t, cfg.Write())

			configPath := filepath.Join(configDir, "config.yml")
			configBefore, err := os.ReadFile(configPath)
			require.NoError(t, err)

			client, err := NewClientFromConfig(hostname, cfg, false, "test-agent")
			require.NoError(t, err)

			key, value, err := client.AuthSource().Header(t.Context())
			require.NoError(t, err)
			assert.Equal(t, tt.expectedAuthKey, key)
			assert.Equal(t, tt.expectedAuthValue, value)
			assert.Equal(t, tt.expectedRefreshes, refreshRequests.Load())

			configAfter, err := os.ReadFile(configPath)
			require.NoError(t, err)
			if tt.expectConfigUnchanged {
				assert.Equal(t, sha256.Sum256(configBefore), sha256.Sum256(configAfter))
			} else {
				assert.NotEqual(t, sha256.Sum256(configBefore), sha256.Sum256(configAfter))
			}
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
