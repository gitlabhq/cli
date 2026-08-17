//go:build !integration

package api

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/config"
)

func TestCustomHeadersTransport(t *testing.T) {
	tests := []struct {
		name          string
		customHeaders map[string]string
		allowedHosts  map[string]struct{}
		requestURL    string
		requestHeader string
		expected      map[string]string
	}{
		{
			name: "adds one header",
			customHeaders: map[string]string{
				"X-Custom-Header": "custom-value",
			},
			expected: map[string]string{"X-Custom-Header": "custom-value"},
		},
		{
			name: "adds proxy authorization header",
			customHeaders: map[string]string{
				"Proxy-Authorization": "Bearer token123",
			},
			expected: map[string]string{"Proxy-Authorization": "Bearer token123"},
		},
		{
			name: "adds authorization header",
			customHeaders: map[string]string{
				"Authorization": "Bearer token456",
			},
			expected: map[string]string{"Authorization": "Bearer token456"},
		},
		{
			name: "adds cloudflare access headers",
			customHeaders: map[string]string{
				"Cf-Access-Client-Id":     "client-123",
				"Cf-Access-Client-Secret": "secret-456",
			},
			expected: map[string]string{
				"Cf-Access-Client-Id":     "client-123",
				"Cf-Access-Client-Secret": "secret-456",
			},
		},
		{
			name: "adds multiple headers",
			customHeaders: map[string]string{
				"X-Custom-Header":     "value1",
				"Proxy-Authorization": "Bearer proxy-token",
			},
			expected: map[string]string{
				"X-Custom-Header":     "value1",
				"Proxy-Authorization": "Bearer proxy-token",
			},
		},
		{
			name: "configured value replaces a request value",
			customHeaders: map[string]string{
				"X-Custom-Header": "configured-value",
			},
			requestHeader: "request-value",
			expected:      map[string]string{"X-Custom-Header": "configured-value"},
		},
		{
			name: "does not add headers without an allowed host",
			customHeaders: map[string]string{
				"Proxy-Authorization": "Bearer proxy-token",
			},
			allowedHosts: map[string]struct{}{},
			expected:     map[string]string{"Proxy-Authorization": ""},
		},
		{
			name: "does not add headers to another host",
			customHeaders: map[string]string{
				"Proxy-Authorization": "Bearer proxy-token",
			},
			allowedHosts: map[string]struct{}{"example.com": {}},
			requestURL:   "https://redirect.example.com",
			expected:     map[string]string{"Proxy-Authorization": ""},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var transportedRequest *http.Request
			allowedHosts := tc.allowedHosts
			if allowedHosts == nil {
				allowedHosts = map[string]struct{}{"example.com": {}}
			}
			transport := &customHeadersTransport{
				headers:      tc.customHeaders,
				allowedHosts: allowedHosts,
				rt: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					transportedRequest = req
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader("")),
						Request:    req,
					}, nil
				}),
			}

			requestURL := tc.requestURL
			if requestURL == "" {
				requestURL = "https://example.com"
			}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, requestURL, nil)
			require.NoError(t, err)
			if tc.requestHeader != "" {
				req.Header.Set("X-Custom-Header", tc.requestHeader)
			}

			resp, err := transport.RoundTrip(req)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())

			for name, value := range tc.expected {
				assert.Equal(t, value, transportedRequest.Header.Get(name))
			}
			if tc.requestHeader != "" {
				assert.Equal(t, []string{"configured-value"}, transportedRequest.Header.Values("X-Custom-Header"))
			}
			if tc.requestHeader == "" {
				assert.Empty(t, req.Header.Get("X-Custom-Header"), "the caller request must not be mutated")
			} else {
				assert.Equal(t, tc.requestHeader, req.Header.Get("X-Custom-Header"), "the caller request must not be mutated")
			}
		})
	}
}

func TestDebugTransportIncludesCustomHeaders(t *testing.T) {
	t.Setenv("GLAB_DEBUG_HTTP", "true")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(
		func(*http.Client) (gitlab.AuthSource, error) {
			return gitlab.Unauthenticated{}, nil
		},
		WithBaseURL(server.URL+"/api/v4/"),
		WithCustomHeaders(map[string]string{
			"X-Debug-Header":      "visible-value",
			"Proxy-Authorization": "Bearer secret-value",
		}),
	)
	require.NoError(t, err)

	customTransport, ok := client.HTTPClient().Transport.(*customHeadersTransport)
	require.True(t, ok, "custom header transport should be the outer transport")
	debug, ok := customTransport.rt.(*debugTransport)
	require.True(t, ok, "debug transport should receive the injected custom headers")
	var output bytes.Buffer
	debug.w = &output

	resp, err := client.HTTPClient().Get(server.URL)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	assert.Contains(t, output.String(), "X-Debug-Header: visible-value")
	assert.Contains(t, output.String(), "Proxy-Authorization: [REDACTED]")
	assert.NotContains(t, output.String(), "secret-value")
}

func TestCustomHeadersCoverAuthSourceAndAPIRequests(t *testing.T) {
	requestPaths := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requestPaths <- req.URL.Path + ":" + req.Header.Get("Proxy-Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(
		func(httpClient *http.Client) (gitlab.AuthSource, error) {
			resp, err := httpClient.Get(server.URL + "/oauth/token")
			if err != nil {
				return nil, err
			}
			if err := resp.Body.Close(); err != nil {
				return nil, err
			}
			return gitlab.Unauthenticated{}, nil
		},
		WithBaseURL(server.URL+"/api/v4/"),
		WithCustomHeaders(map[string]string{"Proxy-Authorization": "Bearer proxy-token"}),
	)
	require.NoError(t, err)

	baseURL, err := url.Parse(server.URL + "/api/v4/projects")
	require.NoError(t, err)
	req, err := NewHTTPRequest(t.Context(), client, http.MethodPost, baseURL, strings.NewReader(`{"key":"value"}`), nil, true)
	require.NoError(t, err)
	resp, err := client.HTTPClient().Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	assert.Equal(t, "/oauth/token:Bearer proxy-token", <-requestPaths)
	assert.Equal(t, "/api/v4/projects:Bearer proxy-token", <-requestPaths)
}

func TestCustomHeadersCoverOAuthHostWhenAPIHostDiffers(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "")
	t.Setenv("GITLAB_ACCESS_TOKEN", "")
	t.Setenv("OAUTH_TOKEN", "")
	t.Setenv("GLAB_IS_OAUTH2", "")

	oauthHeader := make(chan string, 1)
	oauthServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		oauthHeader <- req.Header.Get("Proxy-Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"refreshed-access-token","token_type":"Bearer","refresh_token":"refreshed-refresh-token","expires_in":3600}`)
	}))
	t.Cleanup(oauthServer.Close)

	apiHeader := make(chan string, 1)
	apiServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		apiHeader <- req.Header.Get("Proxy-Authorization")
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
    custom_headers:
      - name: Proxy-Authorization
        value: Bearer proxy-token
`, repoHost, apiHost, time.Now().Add(-time.Hour).Format(time.RFC3339)), t.TempDir())
	require.NoError(t, cfg.Write())

	client, err := NewClientFromConfig(repoHost, cfg, false, "test-agent")
	require.NoError(t, err)

	_, _, err = client.AuthSource().Header(t.Context())
	require.NoError(t, err)
	resp, err := client.HTTPClient().Get(apiServer.URL + "/api/v4/projects")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	assert.Equal(t, "Bearer proxy-token", <-oauthHeader)
	assert.Equal(t, "Bearer proxy-token", <-apiHeader)
}

func TestNewHTTPRequest_UnauthenticatedAuthSource(t *testing.T) {
	client := &Client{
		gitlabClient: &gitlab.Client{},
		authSource:   gitlab.Unauthenticated{},
	}

	baseURL, err := url.Parse("https://example.com/api")
	require.NoError(t, err)
	req, err := NewHTTPRequest(t.Context(), client, http.MethodGet, baseURL, nil, nil, false)
	require.NoError(t, err)
	require.NotNil(t, req)
	assert.Empty(t, req.Header.Get("PRIVATE-TOKEN"))
	assert.Empty(t, req.Header.Get("Authorization"))
	assert.Empty(t, req.Header.Get("Job-Token"))
}

func TestClientInitializationWithNoCustomHeaders(t *testing.T) {
	client, err := NewClient(
		func(*http.Client) (gitlab.AuthSource, error) {
			return gitlab.Unauthenticated{}, nil
		},
		WithBaseURL("https://example.com/api/v4/"),
	)
	require.NoError(t, err)

	_, wrapped := client.HTTPClient().Transport.(*customHeadersTransport)
	assert.False(t, wrapped)
}
