//go:build !integration

package rotate

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// noMorePages creates a response that indicates no more pages are available
func noMorePages() *gitlab.Response {
	return &gitlab.Response{NextPage: 0}
}

func parseTime(s string) *time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return &t
}

func TestRotatePersonalAccessToken(t *testing.T) {
	// NOTE: Subtests cannot run in parallel because they use cmdutils.GroupOverride()
	// which modifies global viper state (SetEnvPrefix, BindEnv).

	type testCase struct {
		name        string
		cli         string
		expectedOut string
		wantJSON    bool
		wantErr     bool
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	testUser := &gitlab.User{
		ID:       1,
		Username: "johndoe",
		Name:     "John Doe",
		Email:    "john.doe@acme.com",
	}

	testPAT := &gitlab.PersonalAccessToken{
		ID:        10183862,
		Name:      "my-pat",
		Revoked:   false,
		CreatedAt: parseTime("2024-07-08T01:23:04.311Z"),
		Scopes:    []string{"k8s_proxy"},
		UserID:    926857,
		Active:    true,
		ExpiresAt: new(gitlab.ISOTime(*parseTime("2024-08-07T00:00:00Z"))),
		Token:     "sometoken",
	}

	testCases := []testCase{
		{
			name:        "rotate PAT as text",
			cli:         "--user @me my-pat",
			expectedOut: "sometoken\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(testUser, nil, nil)
				tc.MockPersonalAccessTokens.EXPECT().
					ListPersonalAccessTokens(gomock.Any(), gomock.Any(), gomock.Any()).
					Return([]*gitlab.PersonalAccessToken{testPAT}, noMorePages(), nil)
				tc.MockPersonalAccessTokens.EXPECT().
					RotatePersonalAccessToken(int64(10183862), gomock.Any(), gomock.Any()).
					Return(testPAT, nil, nil)
			},
		},
		{
			name:     "rotate PAT as json",
			cli:      "--user @me --output json my-pat",
			wantJSON: true,
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(testUser, nil, nil)
				tc.MockPersonalAccessTokens.EXPECT().
					ListPersonalAccessTokens(gomock.Any(), gomock.Any(), gomock.Any()).
					Return([]*gitlab.PersonalAccessToken{testPAT}, noMorePages(), nil)
				tc.MockPersonalAccessTokens.EXPECT().
					RotatePersonalAccessToken(int64(10183862), gomock.Any(), gomock.Any()).
					Return(testPAT, nil, nil)
			},
		},
		{
			name:        "rotate PAT by ID",
			cli:         "--user @me 10183862",
			expectedOut: "sometoken\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(testUser, nil, nil)
				tc.MockPersonalAccessTokens.EXPECT().
					ListPersonalAccessTokens(gomock.Any(), gomock.Any(), gomock.Any()).
					Return([]*gitlab.PersonalAccessToken{testPAT}, noMorePages(), nil)
				tc.MockPersonalAccessTokens.EXPECT().
					RotatePersonalAccessToken(int64(10183862), gomock.Any(), gomock.Any()).
					Return(testPAT, nil, nil)
			},
		},
		{
			name:       "error when PAT not found by ID",
			cli:        "--user @me 99999",
			wantErr:    true,
			wantStderr: "no active token found with the ID '99999'",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(testUser, nil, nil)
				tc.MockPersonalAccessTokens.EXPECT().
					ListPersonalAccessTokens(gomock.Any(), gomock.Any(), gomock.Any()).
					Return([]*gitlab.PersonalAccessToken{testPAT}, noMorePages(), nil)
			},
		},
		{
			name:       "error when missing argument without self",
			cli:        "",
			wantErr:    true,
			wantStderr: "requires either '--self' or a token name/ID argument",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdRotate,
				true,
				cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
			)

			// WHEN
			out, err := exec(tc.cli)

			// THEN
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantStderr)
				return
			}
			require.NoError(t, err)
			if tc.wantJSON {
				var result map[string]any
				err := json.Unmarshal(out.OutBuf.Bytes(), &result)
				require.NoError(t, err)
				assert.Equal(t, "my-pat", result["name"])
			} else if tc.expectedOut != "" {
				assert.Equal(t, tc.expectedOut, out.OutBuf.String())
			}
			assert.Empty(t, out.ErrBuf.String())
		})
	}
}

func TestRotateGroupAccessToken(t *testing.T) {
	// NOTE: Subtests cannot run in parallel because they use cmdutils.GroupOverride()
	// which modifies global viper state (SetEnvPrefix, BindEnv).

	type testCase struct {
		name        string
		cli         string
		expectedOut string
		wantJSON    bool
		wantErr     bool
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	testGroupToken := &gitlab.GroupAccessToken{
		PersonalAccessToken: gitlab.PersonalAccessToken{
			ID:        10190772,
			UserID:    21989300,
			Name:      "my-group-token",
			Scopes:    []string{"read_registry", "read_repository"},
			CreatedAt: parseTime("2024-07-08T17:33:34.829Z"),
			ExpiresAt: new(gitlab.ISOTime(*parseTime("2024-08-07T00:00:00Z"))),
			Active:    true,
			Revoked:   false,
			Token:     "glpat-yz2791KMU-xxxxxxxxx",
		},
		AccessLevel: gitlab.DeveloperPermissions,
	}

	testCases := []testCase{
		{
			name:        "rotate group token as text",
			cli:         "--group GROUP my-group-token",
			expectedOut: "glpat-yz2791KMU-xxxxxxxxx\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroupAccessTokens.EXPECT().
					ListGroupAccessTokens("GROUP", gomock.Any(), gomock.Any(), gomock.Any()).
					Return([]*gitlab.GroupAccessToken{testGroupToken}, noMorePages(), nil)
				tc.MockGroupAccessTokens.EXPECT().
					RotateGroupAccessToken("GROUP", int64(10190772), gomock.Any(), gomock.Any()).
					Return(testGroupToken, nil, nil)
			},
		},
		{
			name:     "rotate group token as json",
			cli:      "--group GROUP my-group-token --output json",
			wantJSON: true,
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroupAccessTokens.EXPECT().
					ListGroupAccessTokens("GROUP", gomock.Any(), gomock.Any(), gomock.Any()).
					Return([]*gitlab.GroupAccessToken{testGroupToken}, noMorePages(), nil)
				tc.MockGroupAccessTokens.EXPECT().
					RotateGroupAccessToken("GROUP", int64(10190772), gomock.Any(), gomock.Any()).
					Return(testGroupToken, nil, nil)
			},
		},
		{
			name:        "rotate group token by ID",
			cli:         "--group GROUP 10190772",
			expectedOut: "glpat-yz2791KMU-xxxxxxxxx\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroupAccessTokens.EXPECT().
					ListGroupAccessTokens("GROUP", gomock.Any(), gomock.Any(), gomock.Any()).
					Return([]*gitlab.GroupAccessToken{testGroupToken}, noMorePages(), nil)
				tc.MockGroupAccessTokens.EXPECT().
					RotateGroupAccessToken("GROUP", int64(10190772), gomock.Any(), gomock.Any()).
					Return(testGroupToken, nil, nil)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdRotate,
				true,
				cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
			)

			// WHEN
			out, err := exec(tc.cli)

			// THEN
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantStderr)
				return
			}
			require.NoError(t, err)
			if tc.wantJSON {
				var result map[string]any
				err := json.Unmarshal(out.OutBuf.Bytes(), &result)
				require.NoError(t, err)
				assert.Equal(t, "my-group-token", result["name"])
			} else if tc.expectedOut != "" {
				assert.Equal(t, tc.expectedOut, out.OutBuf.String())
			}
			assert.Empty(t, out.ErrBuf.String())
		})
	}
}

func TestRotateProjectAccessToken(t *testing.T) {
	// NOTE: Subtests cannot run in parallel because they use cmdutils.GroupOverride()
	// which modifies global viper state (SetEnvPrefix, BindEnv).

	type testCase struct {
		name        string
		cli         string
		expectedOut string
		wantJSON    bool
		wantErr     bool
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	testProjectToken := &gitlab.ProjectAccessToken{
		PersonalAccessToken: gitlab.PersonalAccessToken{
			ID:        10191548,
			UserID:    21990679,
			Name:      "my-project-token",
			Scopes:    []string{"api", "read_repository"},
			CreatedAt: parseTime("2024-07-08T19:47:14.727Z"),
			ExpiresAt: new(gitlab.ISOTime(*parseTime("2024-08-07T00:00:00Z"))),
			Active:    true,
			Revoked:   false,
			Token:     "glpat-dfsdfjksjdfslkdfjsd",
		},
		AccessLevel: gitlab.DeveloperPermissions,
	}

	testCases := []testCase{
		{
			name:        "rotate project token as text",
			cli:         "my-project-token",
			expectedOut: "glpat-dfsdfjksjdfslkdfjsd\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjectAccessTokens.EXPECT().
					ListProjectAccessTokens("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
					Return([]*gitlab.ProjectAccessToken{testProjectToken}, noMorePages(), nil)
				tc.MockProjectAccessTokens.EXPECT().
					RotateProjectAccessToken("OWNER/REPO", int64(10191548), gomock.Any(), gomock.Any()).
					Return(testProjectToken, nil, nil)
			},
		},
		{
			name:     "rotate project token as json",
			cli:      "--output json my-project-token",
			wantJSON: true,
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjectAccessTokens.EXPECT().
					ListProjectAccessTokens("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
					Return([]*gitlab.ProjectAccessToken{testProjectToken}, noMorePages(), nil)
				tc.MockProjectAccessTokens.EXPECT().
					RotateProjectAccessToken("OWNER/REPO", int64(10191548), gomock.Any(), gomock.Any()).
					Return(testProjectToken, nil, nil)
			},
		},
		{
			name:        "rotate project token by ID",
			cli:         "10191548",
			expectedOut: "glpat-dfsdfjksjdfslkdfjsd\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjectAccessTokens.EXPECT().
					ListProjectAccessTokens("OWNER/REPO", gomock.Any(), gomock.Any(), gomock.Any()).
					Return([]*gitlab.ProjectAccessToken{testProjectToken}, noMorePages(), nil)
				tc.MockProjectAccessTokens.EXPECT().
					RotateProjectAccessToken("OWNER/REPO", int64(10191548), gomock.Any(), gomock.Any()).
					Return(testProjectToken, nil, nil)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdRotate,
				true,
				cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
			)

			// WHEN
			out, err := exec(tc.cli)

			// THEN
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantStderr)
				return
			}
			require.NoError(t, err)
			if tc.wantJSON {
				var result map[string]any
				err := json.Unmarshal(out.OutBuf.Bytes(), &result)
				require.NoError(t, err)
				assert.Equal(t, "my-project-token", result["name"])
			} else if tc.expectedOut != "" {
				assert.Equal(t, tc.expectedOut, out.OutBuf.String())
			}
			assert.Empty(t, out.ErrBuf.String())
		})
	}
}

func TestRotatePersonalAccessTokenSelf(t *testing.T) {
	// NOTE: Subtests cannot run in parallel because they use cmdutils.GroupOverride()
	// which modifies global viper state (SetEnvPrefix, BindEnv).

	type testCase struct {
		name        string
		cli         string
		expectedOut string
		wantJSON    bool
		wantErr     bool
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	testPAT := &gitlab.PersonalAccessToken{
		ID:        10183862,
		Name:      "my-pat",
		Revoked:   false,
		CreatedAt: parseTime("2024-07-08T01:23:04.311Z"),
		Scopes:    []string{"k8s_proxy"},
		UserID:    926857,
		Active:    true,
		ExpiresAt: new(gitlab.ISOTime(*parseTime("2024-08-07T00:00:00Z"))),
		Token:     "newtoken",
	}

	testCases := []testCase{
		{
			name:        "rotate self as text",
			cli:         "--self",
			expectedOut: "newtoken\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockPersonalAccessTokens.EXPECT().
					RotatePersonalAccessTokenSelf(gomock.Any(), gomock.Any()).
					Return(testPAT, nil, nil)
			},
		},
		{
			name:     "rotate self as json",
			cli:      "--self --output json",
			wantJSON: true,
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockPersonalAccessTokens.EXPECT().
					RotatePersonalAccessTokenSelf(gomock.Any(), gomock.Any()).
					Return(testPAT, nil, nil)
			},
		},
		{
			name:       "error when self with positional argument",
			cli:        "--self my-token",
			wantErr:    true,
			wantStderr: "'--self' does not accept a positional argument",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdRotate,
				true,
				cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "gitlab.example.com", api.WithGitLabClient(testClient.Client))),
			)

			// WHEN
			out, err := exec(tc.cli)

			// THEN
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantStderr)
				return
			}
			require.NoError(t, err)
			if tc.wantJSON {
				var result map[string]any
				err := json.Unmarshal(out.OutBuf.Bytes(), &result)
				require.NoError(t, err)
				assert.Equal(t, "my-pat", result["name"])
			} else if tc.expectedOut != "" {
				assert.Equal(t, tc.expectedOut, out.OutBuf.String())
			}
			assert.Empty(t, out.ErrBuf.String())
		})
	}
}

// failingSetConfig makes the credential write fail, to prove the new token is
// printed before glab tries to store it.
type failingSetConfig struct{ config.Config }

func (failingSetConfig) Set(string, string, string) error { return errors.New("disk full") }

func TestRotateSelf_StoredToken(t *testing.T) {
	const host = "gitlab.example.com"

	rotated := &gitlab.PersonalAccessToken{ID: 1, Name: "my-pat", Active: true, Token: "newtoken"}

	tests := []struct {
		name       string
		hostsYAML  string
		seedToken  bool
		seedLegacy bool
		envToken   string
		apiHost    string
		noBaseRepo bool
		wrap       func(config.Config) config.Config
		cli        string
		wantErr    string
		wantStored string
		wantStderr []string
		notStderr  []string
		noStderr   bool
	}{
		{
			name:       "config file token is replaced",
			hostsYAML:  "hosts:\n  gitlab.example.com:\n    token: oldtoken\n    use_keyring: \"false\"\n",
			cli:        "--self",
			wantStored: "newtoken",
			wantStderr: []string{"Updated the stored token for gitlab.example.com", "config file"},
		},
		{
			name:       "keyring token is replaced",
			hostsYAML:  "hosts:\n  gitlab.example.com:\n    use_keyring: \"true\"\n",
			seedToken:  true,
			cli:        "--self",
			wantStored: "newtoken",
			wantStderr: []string{"Updated the stored token for gitlab.example.com", "keyring"},
		},
		{
			name:       "stdout stays clean JSON while stderr reports the update",
			hostsYAML:  "hosts:\n  gitlab.example.com:\n    token: oldtoken\n    use_keyring: \"false\"\n",
			cli:        "--self --output json",
			wantStored: "newtoken",
			wantStderr: []string{"Updated the stored token for gitlab.example.com"},
		},
		{
			name:       "environment token cannot be updated, so glab warns",
			hostsYAML:  "hosts:\n  gitlab.example.com:\n    use_keyring: \"false\"\n",
			envToken:   "envtoken",
			cli:        "--self",
			wantStored: "",
			wantStderr: []string{"GITLAB_TOKEN", "gitlab.example.com", "can't update"},
		},
		{
			name:      "no stored token means nothing to update and nothing printed",
			hostsYAML: "hosts:\n  gitlab.example.com:\n    use_keyring: \"false\"\n",
			cli:       "--self",
			noStderr:  true,
		},
		{
			name:       "an api_host is not the key the token is stored under",
			hostsYAML:  "hosts:\n  gitlab.example.com:\n    token: oldtoken\n    api_host: api.example.com\n    use_keyring: \"false\"\n",
			apiHost:    "api.example.com",
			cli:        "--self",
			wantStored: "newtoken",
			wantStderr: []string{"Updated the stored token for gitlab.example.com", "config file"},
		},
		{
			name:       "outside a repository the default host is the key",
			hostsYAML:  "hosts:\n  gitlab.example.com:\n    token: oldtoken\n    use_keyring: \"false\"\n",
			noBaseRepo: true,
			cli:        "--self",
			wantStored: "newtoken",
			wantStderr: []string{"Updated the stored token for gitlab.example.com", "config file"},
		},
		{
			name:       "a legacy keyring token moves to the config file and the message says so",
			hostsYAML:  "hosts:\n  gitlab.example.com:\n    api_protocol: https\n",
			seedLegacy: true,
			cli:        "--self",
			wantStored: "newtoken",
			wantStderr: []string{"Updated the stored token for gitlab.example.com", "config file"},
			notStderr:  []string{"keyring"},
		},
		{
			name:       "a failed write still prints the token and reports the failure",
			hostsYAML:  "hosts:\n  gitlab.example.com:\n    token: oldtoken\n    use_keyring: \"false\"\n",
			wrap:       func(c config.Config) config.Config { return failingSetConfig{c} },
			cli:        "--self",
			wantErr:    "could not store the new token for gitlab.example.com",
			wantStderr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keyring.MockInit()
			for _, k := range []string{"GITLAB_TOKEN", "GITLAB_ACCESS_TOKEN", "OAUTH_TOKEN", "CI_JOB_TOKEN", "GITLAB_CI"} {
				t.Setenv(k, "")
			}
			if tt.envToken != "" {
				t.Setenv("GITLAB_TOKEN", tt.envToken)
			}

			cfg := config.NewFromStringInDir(tt.hostsYAML, t.TempDir())
			if tt.seedToken {
				require.NoError(t, cfg.Set(host, "token", "oldtoken"))
			}
			if tt.seedLegacy {
				require.NoError(t, keyring.Set("glab:"+host+":token", "", "oldtoken"))
			}
			factoryCfg := cfg
			if tt.wrap != nil {
				factoryCfg = tt.wrap(cfg)
			}

			testClient := gitlabtesting.NewTestClient(t)
			testClient.MockPersonalAccessTokens.EXPECT().
				RotatePersonalAccessTokenSelf(gomock.Any(), gomock.Any()).
				Return(rotated, nil, nil)
			apiHost := host
			if tt.apiHost != "" {
				apiHost = tt.apiHost
			}
			repoOption := cmdtest.WithBaseRepo("OWNER", "REPO", host)
			if tt.noBaseRepo {
				repoOption = cmdtest.WithBaseRepoError(errors.New("no repository"))
			}
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdRotate,
				true,
				cmdtest.WithConfig(factoryCfg),
				cmdtest.WithDefaultHostname(host),
				repoOption,
				cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", apiHost, api.WithGitLabClient(testClient.Client))),
			)

			out, err := exec(tt.cli)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.Equal(t, "newtoken\n", out.OutBuf.String(), "the token must be printed before the store is attempted")
				return
			}
			require.NoError(t, err)
			if tt.cli == "--self" {
				assert.Equal(t, "newtoken\n", out.OutBuf.String(), "stdout carries only the token")
			} else {
				var parsed map[string]any
				require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &parsed), "stdout must stay valid JSON")
			}
			if tt.noStderr {
				assert.Empty(t, out.ErrBuf.String())
			}
			for _, want := range tt.wantStderr {
				assert.Contains(t, out.ErrBuf.String(), want)
			}
			for _, unwanted := range tt.notStderr {
				assert.NotContains(t, out.ErrBuf.String(), unwanted)
			}
			got, _, getErr := cfg.GetWithSource(host, "token", false)
			require.NoError(t, getErr)
			assert.Equal(t, tt.wantStored, got)
		})
	}
}
