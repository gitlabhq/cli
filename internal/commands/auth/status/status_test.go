//go:build !integration

package status

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/google/shlex"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_NewCmdStatus(t *testing.T) {
	tests := []struct {
		name  string
		cli   string
		wants options
	}{
		{
			name:  "no arguments",
			cli:   "",
			wants: options{},
		},
		{
			name: "hostname set",
			cli:  "--hostname gitlab.example.com",
			wants: options{
				hostname: "gitlab.example.com",
			},
		},
		{
			name: "show token",
			cli:  "--show-token",
			wants: options{
				showToken: true,
			},
		},
		{
			name: "all flag set",
			cli:  "--all",
			wants: options{
				all: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := cmdtest.NewTestFactory(nil)

			argv, err := shlex.Split(tt.cli)
			require.NoError(t, err)

			var gotOpts *options
			cmd := NewCmdStatus(f, func(opts *options) error {
				gotOpts = opts
				return nil
			})

			// TODO cobra hack-around
			cmd.Flags().BoolP("help", "x", false, "")

			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err = cmd.ExecuteC()
			require.NoError(t, err)

			assert.Equal(t, tt.wants.hostname, gotOpts.hostname)
			assert.Equal(t, tt.wants.showToken, gotOpts.showToken)
			assert.Equal(t, tt.wants.all, gotOpts.all)
		})
	}
}

func Test_statusRun(t *testing.T) {
	// Force the keyring to be unavailable so the plaintext-migration hint is
	// not emitted; these fixtures assert the base output.
	keyring.MockInitWithError(errors.New("keyring unavailable"))
	t.Cleanup(keyring.MockInit)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yml"), []byte(`---
hosts:
  gitlab.example.com:
    token: xxxxxxxxxxxxxxxxxxxx
    git_protocol: ssh
    api_protocol: https
  gitlab2.example.com:
    token: glpat-xxxxxxxxxxxxxxxxxxxx
    git_protocol: ssh
    api_protocol: https
  gitlab3.example.com:
    token: glpat-xxxxxxxxxxxxxxxxxxxx
    git_protocol: ssh
    api_protocol: https
  another.example:
    token: isinvalid
  test.example:
    token:
`), 0o600))

	cfgFile := config.ConfigFile()
	configs, err := config.ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)

	tests := []struct {
		name    string
		opts    *options
		envVar  bool
		wantErr bool
		stderr  string
	}{
		{
			name: "hostname set with old token format",
			opts: &options{
				hostname: "gitlab.example.com",
			},
			wantErr: false,
			stderr: fmt.Sprintf(`gitlab.example.com
  ✓ Logged in to gitlab.example.com as john_smith (%s)
  ✓ Git operations for gitlab.example.com configured to use ssh protocol.
  ✓ API calls for gitlab.example.com are made over https protocol.
  ✓ REST API Endpoint: https://gitlab.example.com/api/v4/
  ✓ GraphQL Endpoint: https://gitlab.example.com/api/graphql/
  ✓ Token found in configuration file (plaintext): **************************
  ! To store this token more securely, run glab auth login --hostname gitlab.example.com to move it into the operating system keyring.
`, cfgFile),
		},
		{
			name: "hostname set with new token format",
			opts: &options{
				hostname: "gitlab2.example.com",
			},
			wantErr: false,
			stderr: fmt.Sprintf(`gitlab2.example.com
  ✓ Logged in to gitlab2.example.com as john_doe (%s)
  ✓ Git operations for gitlab2.example.com configured to use ssh protocol.
  ✓ API calls for gitlab2.example.com are made over https protocol.
  ✓ REST API Endpoint: https://gitlab2.example.com/api/v4/
  ✓ GraphQL Endpoint: https://gitlab2.example.com/api/graphql/
  ✓ Token found in configuration file (plaintext): **************************
  ! To store this token more securely, run glab auth login --hostname gitlab2.example.com to move it into the operating system keyring.
`, cfgFile),
		},
		{
			name: "instance not authenticated",
			opts: &options{
				hostname: "invalid.example",
			},
			wantErr: true,
			stderr:  "x invalid.example has not been authenticated with glab; run `glab auth login --hostname invalid.example` to authenticate",
		},
		{
			name: "with token set in env variable",
			opts: &options{
				hostname: "gitlab3.example.com",
			},
			envVar:  true,
			wantErr: false,
			stderr: `gitlab3.example.com
  ✓ Logged in to gitlab3.example.com as john_doe (GITLAB_TOKEN)
  ✓ Git operations for gitlab3.example.com configured to use ssh protocol.
  ✓ API calls for gitlab3.example.com are made over https protocol.
  ✓ REST API Endpoint: https://gitlab3.example.com/api/v4/
  ✓ GraphQL Endpoint: https://gitlab3.example.com/api/graphql/
  ✓ Token found in environment variable GITLAB_TOKEN: **************************

! Token is from environment variable GITLAB_TOKEN. This takes precedence over tokens stored in config or keyring.
  If a wrapper (e.g., 'op plugin run -- glab') is setting this, run type glab in your shell to check.
`,
		},
	}

	tc := gitlabtesting.NewTestClient(t)
	gomock.InOrder(
		tc.MockUsers.EXPECT().CurrentUser(gomock.Any()).Return(&gitlab.User{Username: "john_smith"}, nil, nil),
		tc.MockUsers.EXPECT().CurrentUser(gomock.Any()).Return(&gitlab.User{Username: "john_doe"}, nil, nil),
		tc.MockUsers.EXPECT().CurrentUser(gomock.Any()).Return(&gitlab.User{Username: "john_doe"}, nil, nil),
	)

	client := func(token, hostname string) (*api.Client, error) { //nolint:unparam
		return cmdtest.NewTestApiClient(t, nil, token, hostname, api.WithGitLabClient(tc.Client)), nil
	}

	for _, tt := range tests {
		io, _, stdout, stderr := cmdtest.TestIOStreams()
		tt.opts.config = func() config.Config {
			return configs
		}
		tt.opts.io = io
		tt.opts.httpClientOverride = client
		tt.opts.apiClient = func(repoHost string) (*api.Client, error) {
			return client("", repoHost)
		}
		t.Run(tt.name, func(t *testing.T) {
			if tt.envVar {
				t.Setenv("GITLAB_TOKEN", "foo")
			} else {
				t.Setenv("GITLAB_TOKEN", "")
			}

			err := tt.opts.run(t.Context())
			if (err != nil) != tt.wantErr {
				t.Errorf("statusRun() error = %v, wantErr %v", err, tt.wantErr)
			}

			assert.Empty(t, stdout.String())

			if tt.wantErr {
				require.Error(t, err)
				assert.Equal(t, tt.stderr, err.Error())
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.stderr, stderr.String())
			}
		})
	}
}

func Test_statusRun_authFailureWithEnvToken(t *testing.T) {
	keyring.MockInitWithError(errors.New("keyring unavailable"))
	t.Cleanup(keyring.MockInit)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yml"), []byte(`---
hosts:
  gitlab.example.com:
    token: xxxxxxxxxxxxxxxxxxxx
    git_protocol: ssh
    api_protocol: https
`), 0o600))

	tc := gitlabtesting.NewTestClient(t)
	tc.MockUsers.EXPECT().CurrentUser(gomock.Any()).Return(nil, &gitlab.Response{Response: &http.Response{StatusCode: http.StatusUnauthorized}}, errors.New("GET https://gitlab.example.com/api/v4/user: 401 {error: invalid_token}"))

	client := func(token, hostname string) (*api.Client, error) { //nolint:unparam
		return cmdtest.NewTestApiClient(t, nil, token, hostname, api.WithGitLabClient(tc.Client)), nil
	}

	t.Setenv("GITLAB_TOKEN", "glpat-expired-token")
	configs, err := config.ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)
	io, _, stdout, stderr := cmdtest.TestIOStreams()

	opts := &options{
		hostname: "gitlab.example.com",
		config: func() config.Config {
			return configs
		},
		apiClient: func(repoHost string) (*api.Client, error) {
			return client("", repoHost)
		},
		httpClientOverride: client,
		io:                 io,
	}

	err = opts.run(t.Context())
	require.Error(t, err)
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "Token is from environment variable GITLAB_TOKEN. A wrapper may be injecting a different or expired token.")
	assert.Contains(t, stderr.String(), "To investigate, run in your shell: type glab")
	assert.Contains(t, stderr.String(), "To see the token value in use, run: env | grep -E 'GITLAB_TOKEN|GITLAB_ACCESS_TOKEN|OAUTH_TOKEN'")
	assert.Contains(t, stderr.String(), "Token is from environment variable GITLAB_TOKEN. This takes precedence over tokens stored in config or keyring.")
	assert.Contains(t, stderr.String(), "If a wrapper (e.g., 'op plugin run -- glab') is setting this, run type glab in your shell to check.")
}

func Test_statusRun_noHostnameSpecified(t *testing.T) {
	keyring.MockInitWithError(errors.New("keyring unavailable"))
	t.Cleanup(keyring.MockInit)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yml"), []byte(`---
hosts:
  gitlab.example.com:
    token: xxxxxxxxxxxxxxxxxxxx
    git_protocol: ssh
    api_protocol: https
  another.example:
    token: isinvalid
  test.example:
    token:
`), 0o600))

	cfgFile := config.ConfigFile()

	tc := gitlabtesting.NewTestClient(t)
	gomock.InOrder(
		tc.MockUsers.EXPECT().CurrentUser(gomock.Any()).Return(&gitlab.User{Username: "john_smith"}, &gitlab.Response{Response: &http.Response{StatusCode: http.StatusOK}}, nil),
		tc.MockUsers.EXPECT().CurrentUser(gomock.Any()).Return(nil, &gitlab.Response{Response: &http.Response{StatusCode: http.StatusUnauthorized}}, errors.New("GET https://another.example/api/v4/user: 401 {message: invalid token}")),
		tc.MockUsers.EXPECT().CurrentUser(gomock.Any()).Return(nil, &gitlab.Response{Response: &http.Response{StatusCode: http.StatusUnauthorized}}, errors.New("GET https://test.example/api/v4/user: 401 {message: no token provided}")),
	)

	client := func(token, hostname string) (*api.Client, error) { //nolint:unparam
		return cmdtest.NewTestApiClient(t, nil, token, hostname, api.WithGitLabClient(tc.Client)), nil
	}

	expectedOutput := fmt.Sprintf(`gitlab.example.com
  ✓ Logged in to gitlab.example.com as john_smith (%s)
  ✓ Git operations for gitlab.example.com configured to use ssh protocol.
  ✓ API calls for gitlab.example.com are made over https protocol.
  ✓ REST API Endpoint: https://gitlab.example.com/api/v4/
  ✓ GraphQL Endpoint: https://gitlab.example.com/api/graphql/
  ✓ Token found in configuration file (plaintext): **************************
  ! To store this token more securely, run glab auth login --hostname gitlab.example.com to move it into the operating system keyring.
another.example
  x another.example: API call failed: GET https://another.example/api/v4/user: 401 {message: invalid token}
  ✓ Git operations for another.example configured to use ssh protocol.
  ✓ API calls for another.example are made over https protocol.
  ✓ REST API Endpoint: https://another.example/api/v4/
  ✓ GraphQL Endpoint: https://another.example/api/graphql/
  ✓ Token found in configuration file (plaintext): **************************
  ! To store this token more securely, run glab auth login --hostname another.example to move it into the operating system keyring.
test.example
  x test.example: API call failed: GET https://test.example/api/v4/user: 401 {message: no token provided}
  ✓ Git operations for test.example configured to use ssh protocol.
  ✓ API calls for test.example are made over https protocol.
  ✓ REST API Endpoint: https://test.example/api/v4/
  ✓ GraphQL Endpoint: https://test.example/api/graphql/
  ! No token found (checked config file, keyring, and environment variables).
`, cfgFile)

	t.Setenv("GITLAB_TOKEN", "")
	configs, err := config.ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)
	io, _, stdout, stderr := cmdtest.TestIOStreams()

	opts := &options{
		config: func() config.Config {
			return configs
		},
		apiClient: func(repoHost string) (*api.Client, error) {
			return client("", repoHost)
		},
		httpClientOverride: client,
		io:                 io,
	}

	err = opts.run(t.Context())
	assert.Equal(t, "\nx could not authenticate to one or more of the configured GitLab instances", err.Error())
	assert.Empty(t, stdout.String())
	assert.Equal(t, expectedOutput, stderr.String())
}

func Test_statusRun_keyringMigrationHint(t *testing.T) {
	// The token is stored as plaintext in the config file, so status should
	// report the storage location and nudge the user to migrate it.
	keyring.MockInitWithError(errors.New("keyring unavailable"))
	t.Cleanup(keyring.MockInit)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yml"), []byte(`---
hosts:
  gitlab.example.com:
    token: xxxxxxxxxxxxxxxxxxxx
    git_protocol: ssh
    api_protocol: https
`), 0o600))

	tc := gitlabtesting.NewTestClient(t)
	tc.MockUsers.EXPECT().CurrentUser(gomock.Any()).Return(&gitlab.User{Username: "john_smith"}, nil, nil)
	client := func(token, hostname string) (*api.Client, error) { //nolint:unparam
		return cmdtest.NewTestApiClient(t, nil, token, hostname, api.WithGitLabClient(tc.Client)), nil
	}

	t.Setenv("GITLAB_TOKEN", "")
	configs, err := config.ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)
	io, _, _, stderr := cmdtest.TestIOStreams()

	opts := &options{
		hostname: "gitlab.example.com",
		config: func() config.Config {
			return configs
		},
		apiClient: func(repoHost string) (*api.Client, error) {
			return client("", repoHost)
		},
		httpClientOverride: client,
		io:                 io,
	}

	require.NoError(t, opts.run(t.Context()))
	assert.Contains(t, stderr.String(), "Token found in configuration file (plaintext):")
	assert.Contains(t, stderr.String(), "To store this token more securely, run glab auth login --hostname gitlab.example.com to move it into the operating system keyring.")
}

func Test_statusRun_surfacesKeyringReadError(t *testing.T) {
	// use_keyring is enabled but the keyring read fails (locked/denied). Status
	// should report that error rather than silently showing "No token found".
	keyring.MockInitWithError(errors.New("keyring locked"))
	t.Cleanup(keyring.MockInit)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yml"), []byte(`---
hosts:
  gitlab.example.com:
    use_keyring: "true"
    git_protocol: ssh
    api_protocol: https
`), 0o600))

	t.Setenv("GITLAB_TOKEN", "")
	configs, err := config.ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)
	io, _, stdout, stderr := cmdtest.TestIOStreams()

	opts := &options{
		hostname: "gitlab.example.com",
		config: func() config.Config {
			return configs
		},
		apiClient: func(repoHost string) (*api.Client, error) {
			return nil, nil
		},
		io: io,
	}

	err = opts.run(t.Context())
	require.Error(t, err)
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "could not read the token")
	assert.Contains(t, stderr.String(), "keyring locked")
}

func Test_statusRun_noInstance(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yml"), []byte(`---
git_protocol: ssh
`), 0o600))

	configs, err := config.ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)
	io, _, stdout, _ := cmdtest.TestIOStreams()

	opts := &options{
		config: func() config.Config {
			return configs
		},
		apiClient: func(repoHost string) (*api.Client, error) {
			return nil, nil
		},
		io: io,
	}
	t.Run("no instance authenticated", func(t *testing.T) {
		err := opts.run(t.Context())
		assert.Equal(t, "no GitLab instances have been authenticated with glab; run `glab auth login` to authenticate", err.Error())
		assert.Empty(t, stdout.String())
	})
}

func Test_statusRun_flagValidation(t *testing.T) {
	exec := cmdtest.SetupCmdForTest(
		t,
		func(f cmdutils.Factory) *cobra.Command { return NewCmdStatus(f, nil) },
		false,
		cmdtest.WithConfig(config.NewFromString(heredoc.Doc(`
			hosts:
			  gitlab.example.com:
			    token: glpat-xxxxxxxxxxxxxxxxxxxx
			    git_protocol: ssh
			    api_protocol: https
			`,
		))),
	)

	_, err := exec("--all --hostname gitlab.example.com")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "if any flags in the group [all hostname] are set none of the others can be")
}
