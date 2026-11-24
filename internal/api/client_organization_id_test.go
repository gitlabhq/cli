//go:build !integration

package api

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/config"
)

// organizationIDRequest builds a client for a host whose config is hostCfg,
// sends one request to it, and reports the headers the host received.
func organizationIDRequest(t *testing.T, hostCfg string) http.Header {
	t.Helper()

	sent := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		sent <- req.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	host := strings.TrimPrefix(server.URL, "http://")
	cfg := config.NewFromStringInDir(fmt.Sprintf("hosts:\n  %q:\n    api_protocol: http\n    token: test-token\n%s", host, hostCfg), t.TempDir())
	require.NoError(t, cfg.Write())

	client, err := NewClientFromConfig(host, cfg, false, "test-agent")
	require.NoError(t, err)

	resp, err := client.HTTPClient().Get(server.URL + "/api/v4/projects")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	return <-sent
}

func TestNewClientFromConfig_OrganizationIDFromEnvironment(t *testing.T) {
	tests := []struct {
		name     string
		env      string
		expected []string
	}{
		{name: "unset sends no header", env: "", expected: nil},
		{name: "set sends the header", env: "7", expected: []string{"7"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GITLAB_ORGANIZATION_ID", tc.env)

			assert.Equal(t, tc.expected, organizationIDRequest(t, "").Values(organizationIDHeader))
		})
	}
}

func TestNewClientFromConfig_OrganizationIDReplacesACustomHeaderOfAnyCasing(t *testing.T) {
	// ResolveCustomHeaders canonicalizes names, so every spelling lands in the
	// slot the environment value overwrites.
	tests := []struct {
		name       string
		headerName string
	}{
		{name: "the casing glab sends", headerName: "X-Gitlab-Organization-Id"},
		{name: "the casing GitLab documents", headerName: "X-GitLab-Organization-ID"},
		{name: "lowercase, as HTTP/2 sends it", headerName: "x-gitlab-organization-id"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GITLAB_ORGANIZATION_ID", "1")

			hostCfg := "    custom_headers:\n" +
				"      - name: " + tc.headerName + "\n" +
				"        value: from-custom-headers\n" +
				"      - name: Proxy-Authorization\n" +
				"        value: Bearer proxy-token\n"

			headers := organizationIDRequest(t, hostCfg)

			assert.Equal(t, []string{"1"}, headers.Values(organizationIDHeader))
			assert.Equal(t, "Bearer proxy-token", headers.Get("Proxy-Authorization"),
				"dropping the case variant must not drop unrelated headers")
		})
	}
}

func TestNewClientFromConfig_OrganizationIDOnHostWithNoConfigSection(t *testing.T) {
	// ResolveCustomHeaders returns a nil map for a host absent from 'hosts:',
	// and a non-nil empty one for a host that is listed but sets no custom
	// headers. The nil is the one path where the map has to be allocated.
	t.Setenv("GITLAB_ORGANIZATION_ID", "7")
	t.Setenv("GLAB_API_PROTOCOL", "http")

	sent := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		sent <- req.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	cfg := config.NewFromStringInDir("hosts:\n  gitlab.example.com:\n    token: test-token\n", t.TempDir())
	require.NoError(t, cfg.Write())

	host := strings.TrimPrefix(server.URL, "http://")
	client, err := NewClientFromConfig(host, cfg, false, "test-agent", WithoutAuthentication())
	require.NoError(t, err)

	resp, err := client.HTTPClient().Get(server.URL + "/api/v4/projects")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	assert.Equal(t, []string{"7"}, (<-sent).Values(organizationIDHeader))
}

func TestNewClientFromConfig_OrganizationIDRejectsNonNumeric(t *testing.T) {
	// A bad value fails every client build for the host, so the message has to
	// name the variable to change.
	t.Setenv("GITLAB_ORGANIZATION_ID", "my-org")

	cfg := config.NewFromStringInDir("hosts:\n  gitlab.example.com:\n    token: test-token\n", t.TempDir())
	require.NoError(t, cfg.Write())

	_, err := NewClientFromConfig("gitlab.example.com", cfg, false, "test-agent")
	require.ErrorContains(t, err, "invalid GITLAB_ORGANIZATION_ID")
}

func Test_validateOrganizationID(t *testing.T) {
	// A leading plus is accepted because the router's BigInt parse accepts one
	// too.
	for _, id := range []string{"1", "7", "+7", "9223372036854775807"} {
		require.NoError(t, validateOrganizationID(id), id)
	}
	// The last value is int64 max plus one: the router parses the claim as an
	// int64, so a larger number is not an organization it can resolve.
	for _, id := range []string{"my-org", "", "0", "-1", "7.0", "7 ", "0x7", "7\r", "7\n", "7\x00", "9223372036854775808"} {
		require.Error(t, validateOrganizationID(id), id)
	}
}

func TestNewClientFromConfig_OrganizationIDOnRefreshAndAPIHosts(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "")
	t.Setenv("GITLAB_ACCESS_TOKEN", "")
	t.Setenv("OAUTH_TOKEN", "")
	t.Setenv("GLAB_IS_OAUTH2", "")
	t.Setenv("GITLAB_ORGANIZATION_ID", "7")

	oauthHeader := make(chan string, 1)
	oauthServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		oauthHeader <- req.Header.Get("X-Gitlab-Organization-Id")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"refreshed-access-token","token_type":"Bearer","refresh_token":"refreshed-refresh-token","expires_in":3600}`)
	}))
	t.Cleanup(oauthServer.Close)

	apiHeader := make(chan string, 1)
	apiServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		apiHeader <- req.Header.Get("X-Gitlab-Organization-Id")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(apiServer.Close)

	repoHost := strings.TrimPrefix(oauthServer.URL, "https://")
	apiHost := strings.TrimPrefix(apiServer.URL, "https://")
	cfg := config.NewFromStringInDir(fmt.Sprintf(`hosts:
  %q:
    api_host: %q
    api_protocol: https
    skip_tls_verify: true
    is_oauth2: true
    client_id: test-client-id
    token: expired-access-token
    oauth2_refresh_token: stored-refresh-token
    oauth2_expiry_date: %q
`, repoHost, apiHost, time.Now().Add(-time.Hour).Format(time.RFC3339)), t.TempDir())
	require.NoError(t, cfg.Write())

	client, err := NewClientFromConfig(repoHost, cfg, false, "test-agent")
	require.NoError(t, err)

	_, _, err = client.AuthSource().Header(t.Context())
	require.NoError(t, err)
	resp, err := client.HTTPClient().Get(apiServer.URL + "/api/v4/projects")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	assert.Equal(t, "7", <-oauthHeader)
	assert.Equal(t, "7", <-apiHeader)
}
