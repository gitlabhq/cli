//go:build !integration

package login

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/oauth2"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

type roundTripFunc func(r *http.Request) (*http.Response, error)

func (s roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return s(r)
}

func Test_helperRun(t *testing.T) {
	// NOTE: we have to remove the values for the possible token env variables, because it'll take precedence over the config.
	// See config.EnvKeyEquivalence function.
	t.Setenv("GITLAB_TOKEN", "")
	t.Setenv("GITLAB_ACCESS_TOKEN", "")
	t.Setenv("OAUTH_TOKEN", "")
	t.Setenv("USER", "")

	oauth2SuccessExpiryTime := time.Now().Add(10 * time.Minute)
	// Expiry time resolution is down to minutes only. Truncate to a minute
	// boundary without platform-sized integer arithmetic.
	oauth2SuccessExpiryTime = oauth2SuccessExpiryTime.Truncate(time.Minute)

	oauth2ApiClient := func(config config.Config, responseFunc roundTripFunc) func(repoHost string) (*api.Client, error) {
		return func(repoHost string) (*api.Client, error) {
			tokenSource, _ := oauth2.NewConfigTokenSource(config, &http.Client{}, glinstance.DefaultProtocol, repoHost)
			if responseFunc != nil {
				return cmdtest.NewTestAuthSourceApiClient(t, &http.Client{Transport: responseFunc}, gitlab.OAuthTokenSource{TokenSource: tokenSource}, repoHost), nil
			}
			return cmdtest.NewTestAuthSourceApiClient(t, nil, gitlab.OAuthTokenSource{TokenSource: tokenSource}, repoHost), nil
		}
	}

	tests := []struct {
		name            string
		opts            options
		input           string
		wantStdout      []string
		wantStderr      string
		wantErr         bool
		wantValidateErr bool
		testOAuth2      bool
		apiResponse     roundTripFunc
	}{
		{
			name: "host only, credentials found",
			opts: options{
				operation: "get",
				config: func() config.Config {
					return config.NewFromString(heredoc.Doc(`
						_source: "/Users/monalisa/.config/glab/config.yml"
						hosts:
						  example.com:
						    user: "monalisa"
						    token: "some-password"
					`))
				},
			},
			input: heredoc.Doc(`
				protocol=https
				host=example.com
			`),
			wantErr:         false,
			wantValidateErr: false,
			wantStdout: []string{
				"username=monalisa",
				"password=some-password",
			},
			wantStderr: "",
		},
		{
			name: "host plus user",
			opts: options{
				operation: "get",
				config: func() config.Config {
					return config.NewFromString(heredoc.Doc(`
						_source: "/Users/monalisa/.config/glab/config.yml"
						hosts:
						  example.com:
						    user: "monalisa"
						    token: "some-password"
					`))
				},
			},
			input: heredoc.Doc(`
				protocol=https
				host=example.com
				username=monalisa
			`),
			wantErr:         false,
			wantValidateErr: false,
			wantStdout: []string{
				"username=monalisa",
				"password=some-password",
			},
			wantStderr: "",
		},
		{
			name: "url input",
			opts: options{
				operation: "get",
				config: func() config.Config {
					return config.NewFromString(heredoc.Doc(`
						_source: "/Users/monalisa/.config/glab/config.yml"
						hosts:
						  example.com:
						    user: "monalisa"
						    token: "some-password"
					`))
				},
			},
			input: heredoc.Doc(`
				url=https://monalisa@example.com
			`),
			wantErr:         false,
			wantValidateErr: false,
			wantStdout: []string{
				"username=monalisa",
				"password=some-password",
			},
			wantStderr: "",
		},
		{
			name: "host only, no credentials found",
			opts: options{
				operation: "get",
				config: func() config.Config {
					return config.NewFromString(heredoc.Doc(`
						_source: "/Users/monalisa/.config/glab/config.yml"
						hosts:
						  example.com:
						    user: "monalisa"
					`))
				},
			},
			input: heredoc.Doc(`
				protocol=https
				host=example.com
			`),
			wantErr:         true,
			wantValidateErr: false,
			wantStderr:      "",
		},
		{
			name: "token from env",
			opts: options{
				operation: "get",
				config: func() config.Config {
					return config.NewFromString(heredoc.Doc(`
						_source: "GITLAB_TOKEN"
						hosts:
						  example.com:
						    user: "clemsbot"
						    token: "some-password"
					`))
				},
			},
			input: heredoc.Doc(`
				protocol=https
				host=example.com
				username=clemsbot
			`),
			wantErr:         false,
			wantValidateErr: false,
			wantStdout: []string{
				"username=clemsbot",
				"password=some-password",
			},
			wantStderr: "",
		},
		{
			name: "support OAuth2",
			opts: options{
				operation: "get",
				config: func() config.Config {
					configTemplate := heredoc.Doc(`
						_source: "/Users/monalisa/.config/glab/config.yml"
						hosts:
						  example.com:
						    user: "monalisa"
						    is_oauth2: "true"
						    oauth2_refresh_token: "some-refresh-token"
						    oauth2_expiry_date: %s
						    token: "some-access-token"
						    client_id: "1234567890abcdef1234567890abcdef"
					`)
					return config.NewFromString(fmt.Sprintf(
						configTemplate,
						oauth2SuccessExpiryTime.Format(time.RFC822),
					))
				},
			},
			input: heredoc.Doc(`
				protocol=https
				host=example.com
			`),
			wantErr:         false,
			wantValidateErr: false,
			wantStdout: []string{
				"username=oauth2",
				"password=some-access-token",
				fmt.Sprintf("password_expiry_utc=%d", oauth2SuccessExpiryTime.UTC().Unix()),
				"oauth_refresh_token=some-refresh-token",
			},
			wantStderr: "",
			testOAuth2: true,
		},
		{
			name: "failed to refresh",
			opts: options{
				operation: "get",
				config: func() config.Config {
					configTemplate := heredoc.Doc(`
						_source: "/Users/monalisa/.config/glab/config.yml"
						hosts:
						  gitlab.example.com:
						    user: "monalisa"
						    is_oauth2: "true"
						    oauth2_refresh_token: "some-refresh-token"
						    oauth2_expiry_date: %s
						    token: "some-access-token"
						    client_id: "1234567890abcdef1234567890abcdef"
					`)
					return config.NewFromString(fmt.Sprintf(
						configTemplate,
						time.Now().Add(-10*time.Minute).Format(time.RFC822),
					))
				},
			},
			input: heredoc.Doc(`
				protocol=https
				host=gitlab.example.com
			`),
			wantErr:         true,
			wantValidateErr: false,
			wantStdout:      nil,
			wantStderr:      "",
			testOAuth2:      true,
			apiResponse: func(req *http.Request) (*http.Response, error) {
				return nil, fmt.Errorf("connection refused")
			},
		},
		{
			// Additional test case for gitlab.com, as it does not require
			// client_id.
			name: "failed to refresh official gitlab",
			opts: options{
				operation: "get",
				config: func() config.Config {
					configTemplate := heredoc.Doc(`
						_source: "/Users/monalisa/.config/glab/config.yml"
						hosts:
						  gitlab.com:
						    user: "monalisa"
						    is_oauth2: "true"
						    oauth2_refresh_token: "some-refresh-token"
						    oauth2_expiry_date: %s
						    token: "some-access-token"
					`)
					return config.NewFromString(fmt.Sprintf(
						configTemplate,
						time.Now().Add(-10*time.Minute).Format(time.RFC822),
					))
				},
			},
			input: heredoc.Doc(`
				protocol=https
				host=gitlab.com
			`),
			wantErr:         true,
			wantValidateErr: false,
			wantStdout:      nil,
			wantStderr:      "",
			testOAuth2:      true,
			apiResponse: func(req *http.Request) (*http.Response, error) {
				type RefreshErrorResponse struct {
					Error            string `json:"error"`
					ErrorDescription string `json:"error_description,omitempty"`
					ErrorUri         string `json:"error_uri,omitempty"`
				}

				responseBody, _ := json.Marshal(&RefreshErrorResponse{
					Error:            "invalid_client",
					ErrorDescription: "Not a valid client_id",
				})

				response := &http.Response{
					Proto:      "HTTP/1.1",
					Status:     "400 Bad Request",
					StatusCode: http.StatusBadRequest,
					Body:       io.NopCloser(bytes.NewBuffer(responseBody)),
				}
				return response, nil
			},
		},
		{
			name: "support Job Token",
			opts: options{
				operation: "get",
				config: func() config.Config {
					return config.NewFromString(heredoc.Doc(`
						_source: "/Users/monalisa/.config/glab/config.yml"
						hosts:
						  example.com:
						    user: "monalisa"
						    job_token: "some-job-token"
					`))
				},
			},
			input: heredoc.Doc(`
				protocol=https
				host=example.com
			`),
			wantErr:         false,
			wantValidateErr: false,
			wantStdout: []string{
				"username=gitlab-ci-token",
				"password=some-job-token",
			},
			wantStderr: "",
		},
		{
			name: "store command",
			opts: options{
				operation: "store",
				config: func() config.Config {
					return config.NewFromString(heredoc.Doc(`
						_source: "/Users/monalisa/.config/glab/config.yml"
						hosts:
						  example.com:
						    user: "monalisa"
						    token: "some-password"
					`))
				},
			},
			input: heredoc.Doc(`
				protocol=https
				host=example.com
			`),
			wantErr:         false,
			wantValidateErr: true,
			wantStdout:      nil,
			wantStderr:      "",
		},
		{
			name: "erase command",
			opts: options{
				operation: "erase",
				config: func() config.Config {
					return config.NewFromString(heredoc.Doc(`
						_source: "/Users/monalisa/.config/glab/config.yml"
						hosts:
						  example.com:
						    user: "monalisa"
						    token: "some-password"
					`))
				},
			},
			input: heredoc.Doc(`
				protocol=https
				host=example.com
			`),
			wantErr:         false,
			wantValidateErr: true,
			wantStdout:      nil,
			wantStderr:      "",
		},
		{
			name: "invalid command",
			opts: options{
				operation: "not-a-valid-command",
				config: func() config.Config {
					return config.NewFromString(heredoc.Doc(`
						_source: "/Users/monalisa/.config/glab/config.yml"
						hosts:
						  example.com:
						    user: "monalisa"
						    token: "some-password"
					`))
				},
			},
			input:           "",
			wantErr:         false,
			wantValidateErr: true,
			wantStdout:      nil,
			wantStderr:      "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.testOAuth2 {
				tt.opts.apiClient = oauth2ApiClient(tt.opts.config(), tt.apiResponse)
			}

			io, stdin, stdout, stderr := cmdtest.TestIOStreams()
			fmt.Fprint(stdin, tt.input)
			opts := &tt.opts
			opts.io = io

			validateErr := opts.validate()
			if tt.wantValidateErr {
				require.Error(t, validateErr)
			} else {
				require.NoError(t, validateErr)
			}

			// No need to run if validate error is expected
			if tt.wantValidateErr {
				return
			}

			runErr := opts.run()
			if tt.wantErr {
				require.Error(t, runErr)
			} else {
				require.NoError(t, runErr)
			}

			if tt.wantStdout != nil {
				stdout := stdout.String()
				assert.Truef(t, strings.HasPrefix(stdout, "capability[]=authtype\n"), "first line of stdout must always be the capability preamble")
				t.Log(stdout)
				for _, expectedKV := range tt.wantStdout {
					assert.Contains(t, stdout, expectedKV)
				}
			}

			if tt.wantStderr != "" {
				assert.Equal(t, tt.wantStderr, stderr.String())
			}
		})
	}
}

func Test_remoteNameScore(t *testing.T) {
	tests := []struct {
		name          string
		remoteName    string
		expectedScore int
	}{
		{"origin gets highest score", "origin", 3},
		{"upstream gets second highest", "upstream", 2},
		{"gitlab gets third", "gitlab", 1},
		{"other names get zero", "fork", 0},
		{"case insensitive - ORIGIN", "ORIGIN", 3},
		{"case insensitive - Upstream", "Upstream", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := remoteNameScore(tt.remoteName)
			assert.Equal(t, tt.expectedScore, score)
		})
	}
}

func Test_calculateHostScore(t *testing.T) {
	tests := []struct {
		name          string
		host          detectedHost
		expectedScore int
	}{
		{
			name: "gitlab.com with origin remote",
			host: detectedHost{
				hostname:      "gitlab.com",
				remotes:       []string{"origin"},
				authenticated: false,
			},
			expectedScore: 8, // gitlab.com bonus (5) + origin (3)
		},
		{
			name: "authenticated self-hosted with origin",
			host: detectedHost{
				hostname:      "gitlab.company.com",
				remotes:       []string{"origin"},
				authenticated: true,
			},
			expectedScore: 13, // authenticated (10) + origin (3)
		},
		{
			name: "multiple remotes to same host",
			host: detectedHost{
				hostname:      "gitlab.company.com",
				remotes:       []string{"origin", "upstream"},
				authenticated: false,
			},
			expectedScore: 5, // origin (3) + upstream (2)
		},
		{
			name: "unknown remote name",
			host: detectedHost{
				hostname:      "gitlab.example.com",
				remotes:       []string{"fork"},
				authenticated: false,
			},
			expectedScore: 0, // fork (0)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := calculateHostScore(&tt.host)
			assert.Equal(t, tt.expectedScore, score)
		})
	}
}

func Test_prioritizeHosts(t *testing.T) {
	tests := []struct {
		name          string
		hosts         []detectedHost
		expectedOrder []string // Expected order of hostnames
	}{
		{
			name: "authenticated host prioritized",
			hosts: []detectedHost{
				{hostname: "gitlab.com", remotes: []string{"fork"}, authenticated: false},
				{hostname: "gitlab.company.com", remotes: []string{"origin"}, authenticated: true},
			},
			expectedOrder: []string{"gitlab.company.com", "gitlab.com"},
		},
		{
			name: "origin beats upstream",
			hosts: []detectedHost{
				{hostname: "gitlab.upstream.com", remotes: []string{"upstream"}, authenticated: false},
				{hostname: "gitlab.origin.com", remotes: []string{"origin"}, authenticated: false},
			},
			expectedOrder: []string{"gitlab.origin.com", "gitlab.upstream.com"},
		},
		{
			name: "gitlab.com gets boost",
			hosts: []detectedHost{
				{hostname: "gitlab.example.com", remotes: []string{"origin"}, authenticated: false},
				{hostname: "gitlab.com", remotes: []string{"fork"}, authenticated: false},
			},
			expectedOrder: []string{"gitlab.com", "gitlab.example.com"}, // gitlab.com bonus (5) + fork (0) = 5 > origin (3)
		},
		{
			name: "multiple remotes increase score",
			hosts: []detectedHost{
				{hostname: "gitlab.single.com", remotes: []string{"origin"}, authenticated: false},
				{hostname: "gitlab.multi.com", remotes: []string{"origin", "upstream"}, authenticated: false},
			},
			expectedOrder: []string{"gitlab.multi.com", "gitlab.single.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sorted := prioritizeHosts(tt.hosts)
			var actualOrder []string
			for _, host := range sorted {
				actualOrder = append(actualOrder, host.hostname)
			}
			assert.Equal(t, tt.expectedOrder, actualOrder)
		})
	}
}

func Test_detectedHost_String(t *testing.T) {
	tests := []struct {
		name     string
		host     detectedHost
		expected string
	}{
		{
			name: "single remote, not authenticated",
			host: detectedHost{
				hostname:      "gitlab.com",
				remotes:       []string{"origin"},
				authenticated: false,
			},
			expected: "gitlab.com (origin)",
		},
		{
			name: "multiple remotes, authenticated",
			host: detectedHost{
				hostname:      "gitlab.company.com",
				remotes:       []string{"origin", "upstream"},
				authenticated: true,
			},
			expected: "gitlab.company.com (origin, upstream) [authenticated]",
		},
		{
			name: "remotes sorted alphabetically",
			host: detectedHost{
				hostname:      "gitlab.example.com",
				remotes:       []string{"upstream", "fork", "origin"},
				authenticated: false,
			},
			expected: "gitlab.example.com (fork, origin, upstream)",
		},
		{
			name: "nil remotes, not authenticated",
			host: detectedHost{
				hostname:      "gitlab.example.com",
				remotes:       nil,
				authenticated: false,
			},
			expected: "gitlab.example.com",
		},
		{
			name: "empty remotes, authenticated",
			host: detectedHost{
				hostname:      "gitlab.company.com",
				remotes:       []string{},
				authenticated: true,
			},
			expected: "gitlab.company.com [authenticated]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.host.String()
			assert.Equal(t, tt.expected, result)
		})
	}
}
