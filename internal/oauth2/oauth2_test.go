//go:build !integration

package oauth2

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
	validClientID := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	t.Setenv("GITLAB_CLIENT_ID", validClientID)
	cfg := config.NewBlankConfig()

	clientID, err := oauthClientID(cfg, "salsa.debian.org")
	require.NoError(t, err)
	assert.Equal(t, validClientID, clientID)

	// A non-interactive io would surface the non-interactive error instead
	// of the env value if resolveClientID ever reached the prompt.
	clientID, persist, err := resolveClientID(t.Context(), cfg, newNonInteractiveIOStreams(), "salsa.debian.org")
	require.NoError(t, err)
	assert.Equal(t, validClientID, clientID)
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

			baseURL, err := oauthBaseURL(cfg, "gitlab.example.com", glinstance.DefaultProtocol)

			require.NoError(t, err)
			assert.Equal(t, tt.expectedURL, baseURL)
		})
	}
}

func TestOAuthBaseURLReturnsConfigError(t *testing.T) {
	t.Parallel()

	expectedErr := errors.New("read config")
	cfg := stubConfig{getErr: expectedErr}

	baseURL, err := oauthBaseURL(cfg, "gitlab.example.com", glinstance.DefaultProtocol)

	require.ErrorIs(t, err, expectedErr)
	assert.Empty(t, baseURL)
}

func TestResolveClientID_RejectsInvalidConfigClientID(t *testing.T) {
	tests := []struct {
		name     string
		clientID string
		wantErr  string
	}{
		{
			name:     "personal access token",
			clientID: "glpat-abcdef1234567890abcdef1234567890abcdef1234567890abcdef123456",
			wantErr:  "value is a personal access token (glpat-), not an OAuth application ID",
		},
		{
			name:     "application secret",
			clientID: "gloas-abcdef1234567890abcdef1234567890abcdef1234567890abcdef123456",
			wantErr:  "value is an OAuth application secret (gloas-), not the Application ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := stubConfig{
				hosts: map[string]map[string]string{
					"salsa.debian.org": {
						"client_id": tt.clientID,
					},
				},
			}

			clientID, persist, err := resolveClientID(t.Context(), cfg, newNonInteractiveIOStreams(), "salsa.debian.org")
			require.Error(t, err)
			assert.Empty(t, clientID)
			assert.Nil(t, persist)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestResolveClientID_RejectsNonHexConfigClientID(t *testing.T) {
	t.Run("configured value", func(t *testing.T) {
		cfg := stubConfig{
			hosts: map[string]map[string]string{
				"salsa.debian.org": {
					"client_id": "  custom-legacy-id  \n",
				},
			},
		}

		clientID, persist, err := resolveClientID(t.Context(), cfg, newNonInteractiveIOStreams(), "salsa.debian.org")
		require.Error(t, err)
		assert.Empty(t, clientID)
		assert.Nil(t, persist)
		assert.Contains(t, err.Error(), "OAuth application ID must be 64 hexadecimal characters")
	})

	t.Run("GITLAB_CLIENT_ID env var", func(t *testing.T) {
		t.Setenv("GITLAB_CLIENT_ID", "custom-legacy-id")
		cfg := config.NewBlankConfig()

		clientID, persist, err := resolveClientID(t.Context(), cfg, newNonInteractiveIOStreams(), "salsa.debian.org")
		require.Error(t, err)
		assert.Empty(t, clientID)
		assert.Nil(t, persist)
		assert.Contains(t, err.Error(), "OAuth application ID must be 64 hexadecimal characters")
	})
}

func TestPreflightCheckClientID(t *testing.T) {
	validClientID := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	newTokenServer := func(t *testing.T, status int, contentType, body string) *httptest.Server {
		t.Helper()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "/oauth/token", r.URL.Path)
			assert.NoError(t, r.ParseForm())
			assert.Equal(t, "authorization_code", r.PostForm.Get("grant_type"))
			assert.Equal(t, validClientID, r.PostForm.Get("client_id"))
			assert.Equal(t, redirectURL, r.PostForm.Get("redirect_uri"))
			assert.False(t, hasIAMPrefix(r.PostForm.Get("code")), "the code must not route the request to IAM")

			w.Header().Set("Content-Type", contentType)
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(server.Close)
		return server
	}

	assertRejected := func(t *testing.T, err error, host string) {
		t.Helper()
		require.ErrorIs(t, err, ErrClientIDRejected)
		assert.Contains(t, err.Error(), host)
		assert.Contains(t, err.Error(), "the application is confidential")
		assert.Contains(t, err.Error(), "glab config set client_id")
		assert.Contains(t, err.Error(), "GITLAB_CLIENT_ID")
	}

	t.Run("fails fast on 401 invalid_client", func(t *testing.T) {
		server := newTokenServer(t, http.StatusUnauthorized, "application/json", `{"error":"invalid_client","error_description":"Client authentication failed"}`)
		host := strings.TrimPrefix(server.URL, "http://")

		err := preflightCheckClientID(t.Context(), server.Client(), server.URL, validClientID, host)
		assertRejected(t, err, host)
	})

	t.Run("fails fast on 400 invalid_client", func(t *testing.T) {
		server := newTokenServer(t, http.StatusBadRequest, "application/json", `{"error":"invalid_client"}`)
		host := strings.TrimPrefix(server.URL, "http://")

		err := preflightCheckClientID(t.Context(), server.Client(), server.URL, validClientID, host)
		assertRejected(t, err, host)
	})

	t.Run("continues on 400 invalid_grant from a known client", func(t *testing.T) {
		server := newTokenServer(t, http.StatusBadRequest, "application/json", `{"error":"invalid_grant","error_description":"The provided authorization grant is invalid"}`)
		host := strings.TrimPrefix(server.URL, "http://")

		err := preflightCheckClientID(t.Context(), server.Client(), server.URL, validClientID, host)
		require.NoError(t, err)
	})

	t.Run("continues when invalid_client appears only in the description", func(t *testing.T) {
		server := newTokenServer(t, http.StatusBadRequest, "application/json", `{"error":"invalid_request","error_description":"not an invalid_client"}`)
		host := strings.TrimPrefix(server.URL, "http://")

		err := preflightCheckClientID(t.Context(), server.Client(), server.URL, validClientID, host)
		require.NoError(t, err)
	})

	t.Run("continues on a 401 proxy page", func(t *testing.T) {
		server := newTokenServer(t, http.StatusUnauthorized, "text/html", `<html><body>invalid_client</body></html>`)
		host := strings.TrimPrefix(server.URL, "http://")

		err := preflightCheckClientID(t.Context(), server.Client(), server.URL, validClientID, host)
		require.NoError(t, err)
	})

	t.Run("continues on a redirect", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/users/sign_in", http.StatusFound)
		}))
		t.Cleanup(server.Close)
		host := strings.TrimPrefix(server.URL, "http://")

		err := preflightCheckClientID(t.Context(), server.Client(), server.URL, validClientID, host)
		require.NoError(t, err)
	})

	t.Run("continues on a server error", func(t *testing.T) {
		server := newTokenServer(t, http.StatusInternalServerError, "text/plain", "boom")
		host := strings.TrimPrefix(server.URL, "http://")

		err := preflightCheckClientID(t.Context(), server.Client(), server.URL, validClientID, host)
		require.NoError(t, err)
	})
}

func hasIAMPrefix(code string) bool {
	for _, prefix := range []string{"gliamac-", "gliamat-", "gliamrt-"} {
		if strings.HasPrefix(code, prefix) {
			return true
		}
	}
	return false
}
