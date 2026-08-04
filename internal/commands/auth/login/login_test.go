//go:build !integration

package login

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/shlex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// currentUserFactoryOption stubs the username lookup that a token login makes
// after storing the token.
func currentUserFactoryOption(t *testing.T) cmdtest.FactoryOption {
	t.Helper()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockUsers.EXPECT().
		CurrentUser(gomock.Any()).
		Return(&gitlab.User{Username: "john_smith"}, nil, nil).
		AnyTimes()

	return cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "gitlab.com", api.WithGitLabClient(tc.Client)))
}

func TestMain(m *testing.M) {
	cmdtest.InitTest(m, "auth_login_test")
}

func Test_NewCmdLogin(t *testing.T) {
	tests := []struct {
		name     string
		cli      string
		stdin    string
		wants    LoginOptions
		stdinTTY bool
		wantsErr bool
		err      string
	}{
		{
			name:  "nontty, stdin",
			stdin: "abc123\n",
			cli:   "--stdin",
			wants: LoginOptions{
				Hostname: "gitlab.com",
				Token:    "abc123",
			},
		},
		{
			name:  "tty, stdin",
			stdin: "def456",
			cli:   "--stdin",
			wants: LoginOptions{
				Hostname: "gitlab.com",
				Token:    "def456",
			},
			stdinTTY: true,
		},
		{
			name: "nontty, hostname",
			cli:  "--hostname salsa.debian.org --token dummy-token",
			wants: LoginOptions{
				Hostname: "salsa.debian.org",
				Token:    "dummy-token",
			},
			stdinTTY: false,
		},
		{
			name: "nontty",
			cli:  "--token dummy-token",
			wants: LoginOptions{
				Hostname: "gitlab.com",
				Token:    "dummy-token",
			},
			stdinTTY: false,
		},
		{
			name:  "nontty, stdin, hostname",
			cli:   "--hostname db.org --stdin",
			stdin: "abc123\n",
			wants: LoginOptions{
				Hostname: "db.org",
				Token:    "abc123",
			},
		},
		{
			name:  "tty, stdin, hostname",
			stdin: "gli789",
			cli:   "--stdin --hostname gl.io",
			wants: LoginOptions{
				Hostname: "gl.io",
				Token:    "gli789",
			},
			stdinTTY: true,
		},
		{
			name: "non-interactive hostname, token, api-host",
			cli:  "--hostname gl.io --token foo --api-host api.gitlab.com",
			wants: LoginOptions{
				Hostname: "gl.io",
				Token:    "foo",
				ApiHost:  "api.gitlab.com",
			},
		},
		{
			name: "non-interactive hostname, token, api-host, api-protocol, git-protocol",
			cli:  "--hostname gl.io --token foo --api-host gl.io:3443 --api-protocol https --git-protocol ssh",
			wants: LoginOptions{
				Hostname:    "gl.io",
				Token:       "foo",
				ApiHost:     "gl.io:3443",
				ApiProtocol: "https",
				GitProtocol: "ssh",
			},
		},
		{
			name:  "non-interactive hostname, api-host, api-protocol, git-protocol with stdin token",
			cli:   "--hostname gl.io --api-host gl.io:3443 --api-protocol https --git-protocol ssh --stdin",
			stdin: "gli789",
			wants: LoginOptions{
				Hostname:    "gl.io",
				Token:       "gli789",
				ApiHost:     "gl.io:3443",
				ApiProtocol: "https",
				GitProtocol: "ssh",
			},
			stdinTTY: true,
		},
		{
			name: "api-host with token",
			cli:  "--hostname gl.io --api-host api.gitlab.com --token foo",
			wants: LoginOptions{
				Hostname: "gl.io",
				Token:    "foo",
				ApiHost:  "api.gitlab.com",
			},
			stdinTTY: true,
		},
		{
			name: "api-protocol with token",
			cli:  "--hostname gl.io --api-protocol http --token foo",
			wants: LoginOptions{
				Hostname:    "gl.io",
				Token:       "foo",
				ApiProtocol: "http",
			},
			stdinTTY: true,
		},
		{
			name: "git-protocol with token",
			cli:  "--hostname gl.io --git-protocol ssh --token foo",
			wants: LoginOptions{
				Hostname:    "gl.io",
				Token:       "foo",
				GitProtocol: "ssh",
			},
			stdinTTY: true,
		},
		{
			name:     "web with token",
			cli:      "--hostname gl.io --web --token foo",
			wantsErr: true,
			err:      "none of the others can be",
		},
		{
			name:     "web with stdin",
			cli:      "--hostname gl.io --web --stdin",
			stdin:    "abc123\n",
			wantsErr: true,
			err:      "none of the others can be",
		},
		{
			name:     "web with job-token",
			cli:      "--hostname gl.io --web --job-token foo",
			wantsErr: true,
			err:      "none of the others can be",
		},
		{
			name:     "web with device",
			cli:      "--hostname gl.io --web --device",
			wantsErr: true,
			err:      "none of the others can be",
		},
		{
			name:     "device with token",
			cli:      "--hostname gl.io --device --token foo",
			wantsErr: true,
			err:      "none of the others can be",
		},
		{
			name:     "device with stdin",
			cli:      "--hostname gl.io --device --stdin",
			stdin:    "abc123\n",
			wantsErr: true,
			err:      "none of the others can be",
		},
		{
			name:     "device with job-token",
			cli:      "--hostname gl.io --device --job-token foo",
			wantsErr: true,
			err:      "none of the others can be",
		},
		// TODO: how to test survey
		//{
		//	name:     "tty, hostname",
		//	cli:      "--hostname local.dev",
		//	wants: LoginOptions{
		//		Hostname:    "local.dev",
		//		Token:       "",
		//		Interactive: true,
		//	},
		//	stdinTTY: true,
		//},
		//{
		//	name:     "tty",
		//	cli:      "",
		//	wants: LoginOptions{
		//		Hostname:    "",
		//		Token:       "",
		//		Interactive: true,
		//	},
		//	stdinTTY: true,
		//},
		{
			name:     "token and stdin",
			cli:      "--token xxxx --stdin",
			wantsErr: true,
			err:      "none of the others can be",
		},
		{
			name: "no keyring, token",
			cli:  "--token glpat-123",
			wants: LoginOptions{
				Hostname:   "gitlab.com",
				Token:      "glpat-123",
				UseKeyring: false,
			},
		},
		{
			name: "keyring, token",
			cli:  "--token glpat-123 --use-keyring",
			wants: LoginOptions{
				Hostname:   "gitlab.com",
				Token:      "glpat-123",
				UseKeyring: true,
			},
		},
		{
			name: "non-interactive hostname, jobToken, api-host",
			cli:  "--hostname gl.io --job-token foo --api-host api.gitlab.com",
			wants: LoginOptions{
				Hostname: "gl.io",
				JobToken: "foo",
				ApiHost:  "api.gitlab.com",
			},
		},
		{
			name: "non-interactive hostname, jobToken, api-host, api-protocol, git-protocol",
			cli:  "--hostname gl.io --job-token foo --api-host gl.io:3443 --api-protocol https --git-protocol ssh",
			wants: LoginOptions{
				Hostname:    "gl.io",
				JobToken:    "foo",
				ApiHost:     "gl.io:3443",
				ApiProtocol: "https",
				GitProtocol: "ssh",
			},
		},
		{
			name: "container-registry-domains flag",
			cli:  "--hostname gl.io --token foo --container-registry-domains a.com,b.com",
			wants: LoginOptions{
				Hostname:                 "gl.io",
				Token:                    "foo",
				ContainerRegistryDomains: "a.com,b.com",
			},
		},
		{
			name: "ssh-hostname flag",
			cli:  "--hostname gl.io --token foo --ssh-hostname ssh.gl.io",
			wants: LoginOptions{
				Hostname:    "gl.io",
				Token:       "foo",
				SSHHostname: "ssh.gl.io",
			},
		},
		{
			name: "all new flags combined with token",
			cli:  "--hostname gl.io --token foo --ssh-hostname ssh.gl.io --container-registry-domains a.com,b.com --git-protocol ssh",
			wants: LoginOptions{
				Hostname:                 "gl.io",
				Token:                    "foo",
				SSHHostname:              "ssh.gl.io",
				ContainerRegistryDomains: "a.com,b.com",
				GitProtocol:              "ssh",
			},
		},
	}

	// Enable keyring mocking, so no changes are made to it accidentally and to prevent failing in some environments
	keyring.MockInit()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := t.TempDir()
			t.Setenv("GLAB_CONFIG_DIR", d)

			io, stdin, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true), iostreams.WithStdin(nil, tt.stdinTTY))
			f := cmdtest.NewTestFactory(io, currentUserFactoryOption(t))

			if tt.stdin != "" {
				stdin.WriteString(tt.stdin)
			}

			argv, err := shlex.Split(tt.cli)
			require.NoError(t, err)

			cmd := NewCmdLogin(f)
			// TODO cobra hack-around
			cmd.Flags().BoolP("help", "x", false, "")

			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err = cmd.ExecuteC()

			if tt.wantsErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.err)
				return
			}
			require.NoError(t, err)

			assert.Equal(t, tt.wants.Token, opts.Token)
			assert.Equal(t, tt.wants.JobToken, opts.JobToken)
			assert.Equal(t, tt.wants.Hostname, opts.Hostname)
			assert.Equal(t, tt.wants.Interactive, opts.Interactive)
			assert.Equal(t, tt.wants.ApiHost, opts.ApiHost)
			assert.Equal(t, tt.wants.ApiProtocol, opts.ApiProtocol)
			assert.Equal(t, tt.wants.GitProtocol, opts.GitProtocol)
			assert.Equal(t, tt.wants.WebLogin, opts.WebLogin)
			assert.Equal(t, tt.wants.DeviceLogin, opts.DeviceLogin)
			assert.Equal(t, tt.wants.SSHHostname, opts.SSHHostname)
			assert.Equal(t, tt.wants.ContainerRegistryDomains, opts.ContainerRegistryDomains)
		})
	}
}

func Test_hostnameValidator(t *testing.T) {
	t.Parallel()

	testMap := make(map[string]string)
	testMap["profclems"] = "glab"

	testCases := []struct {
		name     string
		hostname any
		expected string
	}{
		{
			name:     "valid",
			hostname: "localhost",
		},
		{
			name:     "valid-default-value",
			hostname: "gitlab.com",
		},
		{
			name:     "valid-external-instance-alpine",
			hostname: "gitlab.alpinelinux.org",
		},
		{
			name:     "valid-external-instance-freedesktop",
			hostname: "gitlab.freedesktop.org",
		},
		{
			name:     "valid-external-instance-gnome",
			hostname: "gitlab.gnome.org",
		},
		{
			name:     "valid-external-instance-debian",
			hostname: "salsa.debian.org",
		},
		{
			name:     "valid-external-instance-ip",
			hostname: "1.1.1.1",
		},
		{
			name:     "valid-external-instance-ip-with-port",
			hostname: "1.1.1.1:8080",
		},
		{
			name:     "empty",
			hostname: "",
			expected: "hostname cannot be empty",
		},
		{
			name:     "only whitespaces",
			hostname: " ",
			expected: "hostname cannot be empty",
		},
		{
			name:     "valid-hostname-slash",
			hostname: "localhost:9999/host",
		},
		{
			name:     "hostname-with-valid-port",
			hostname: "gitlab.mycompany.com:4000",
		},
		{
			name:     "hostname-with-invalid-port",
			hostname: "local:host",
			expected: `invalid hostname: parse "https://local:host": invalid port ":host" after host`,
		},
		{
			name:     "valid-hostname-with-path-and-dash",
			hostname: "internal.hostname.com/gitlab-licensed",
		},
	}
	for _, tC := range testCases {
		t.Run(tC.name, func(t *testing.T) {
			t.Parallel()

			err := hostnameValidator(tC.hostname)
			if tC.expected == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tC.expected)
			}
		})
	}
}

func Test_keyringLogin(t *testing.T) {
	keyring.MockInit()

	d := t.TempDir()
	t.Setenv("GLAB_CONFIG_DIR", d)

	token, err := keyring.Get("glab:gitlab.com:token", "")
	require.Error(t, err)
	assert.Empty(t, token)

	io, _, _, _ := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(io, currentUserFactoryOption(t))
	cmd := NewCmdLogin(f)
	cmd.Flags().BoolP("help", "x", false, "")

	cmd.SetArgs([]string{"--use-keyring", "--token", "glpat-1234"})

	_, err = cmd.ExecuteC()
	require.NoError(t, err)

	token, err = keyring.Get("glab:gitlab.com:token", "")
	require.NoError(t, err)
	assert.Equal(t, "glpat-1234", token)
}

func Test_defaultKeyringLogin(t *testing.T) {
	keyring.MockInit()
	// Exercise the non-CI default-keyring path even when the test suite runs
	// inside CI (where GITLAB_CI/CI would otherwise route storage to the file).
	t.Setenv("GITLAB_CI", "")
	t.Setenv("CI", "")

	d := t.TempDir()
	t.Setenv("GLAB_CONFIG_DIR", d)

	io, _, _, _ := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(io, currentUserFactoryOption(t))
	cfg := config.NewBlankConfigInDir(d)
	f.ConfigStub = func() config.Config { return cfg }
	cmd := NewCmdLogin(f)
	cmd.Flags().BoolP("help", "x", false, "")

	cmd.SetArgs([]string{"--token", "glpat-default"})

	_, err := cmd.ExecuteC()
	require.NoError(t, err)

	// With a keyring available, the token is stored there by default even
	// without passing --use-keyring.
	got, err := keyring.Get("glab:gitlab.com:token", "")
	require.NoError(t, err)
	assert.Equal(t, "glpat-default", got)

	// The config file records the preference and holds no plaintext token.
	useKeyring, _ := cfg.Get("gitlab.com", "use_keyring")
	assert.Equal(t, "true", useKeyring)
	data, err := os.ReadFile(filepath.Join(d, "config.yml"))
	require.NoError(t, err)
	assert.NotContains(t, string(data), "glpat-default")
}

func Test_insecureStorageLogin(t *testing.T) {
	keyring.MockInit()

	d := t.TempDir()
	t.Setenv("GLAB_CONFIG_DIR", d)

	io, _, _, _ := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(io, currentUserFactoryOption(t))
	cfg := config.NewBlankConfigInDir(d)
	f.ConfigStub = func() config.Config { return cfg }
	cmd := NewCmdLogin(f)
	cmd.Flags().BoolP("help", "x", false, "")

	cmd.SetArgs([]string{"--insecure-storage", "--token", "glpat-plain"})

	_, err := cmd.ExecuteC()
	require.NoError(t, err)

	// --insecure-storage keeps the token out of the keyring entirely.
	_, err = keyring.Get("glab:gitlab.com:token", "")
	require.Error(t, err)

	// The token is stored as plaintext in the config file.
	data, err := os.ReadFile(filepath.Join(d, "config.yml"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "glpat-plain")
}

func Test_keyringUnavailableFallsBackToFile(t *testing.T) {
	keyring.MockInitWithError(errors.New("keyring unavailable"))
	t.Cleanup(keyring.MockInit)
	// Exercise the non-CI warning path. In CI the fallback is silent, so clear
	// GITLAB_CI/CI in case the suite itself runs inside CI.
	t.Setenv("GITLAB_CI", "")
	t.Setenv("CI", "")

	d := t.TempDir()
	t.Setenv("GLAB_CONFIG_DIR", d)

	io, _, _, stderr := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(io, currentUserFactoryOption(t))
	cfg := config.NewBlankConfigInDir(d)
	f.ConfigStub = func() config.Config { return cfg }
	cmd := NewCmdLogin(f)
	cmd.Flags().BoolP("help", "x", false, "")

	cmd.SetArgs([]string{"--token", "glpat-fallback"})

	_, err := cmd.ExecuteC()
	require.NoError(t, err)

	// With no keyring backend, the default warns and falls back to plaintext
	// file storage.
	assert.Contains(t, stderr.String(), "The operating system keyring is unavailable. Storing credentials as plaintext in the configuration file.")
	data, err := os.ReadFile(filepath.Join(d, "config.yml"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "glpat-fallback")
}

func Test_ciLoginDefaultsToFile(t *testing.T) {
	// A working keyring is available, but in CI we default to file storage
	// unless --use-keyring is passed explicitly.
	keyring.MockInit()
	t.Setenv("GITLAB_CI", "true")

	d := t.TempDir()
	t.Setenv("GLAB_CONFIG_DIR", d)

	io, _, _, _ := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(io, currentUserFactoryOption(t))
	cfg := config.NewBlankConfigInDir(d)
	f.ConfigStub = func() config.Config { return cfg }
	cmd := NewCmdLogin(f)
	cmd.Flags().BoolP("help", "x", false, "")

	cmd.SetArgs([]string{"--token", "glpat-ci"})

	_, err := cmd.ExecuteC()
	require.NoError(t, err)

	// Token is stored in the config file, not the keyring.
	_, err = keyring.Get("glab:gitlab.com:token", "")
	require.Error(t, err)
	data, err := os.ReadFile(filepath.Join(d, "config.yml"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "glpat-ci")
}

func Test_ciLoginHonorsExplicitUseKeyring(t *testing.T) {
	keyring.MockInit()
	t.Setenv("GITLAB_CI", "true")

	d := t.TempDir()
	t.Setenv("GLAB_CONFIG_DIR", d)

	io, _, _, _ := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(io, currentUserFactoryOption(t))
	cfg := config.NewBlankConfigInDir(d)
	f.ConfigStub = func() config.Config { return cfg }
	cmd := NewCmdLogin(f)
	cmd.Flags().BoolP("help", "x", false, "")

	cmd.SetArgs([]string{"--use-keyring", "--token", "glpat-ci-explicit"})

	_, err := cmd.ExecuteC()
	require.NoError(t, err)

	// Explicit --use-keyring overrides the CI default and stores in the keyring.
	got, err := keyring.Get("glab:gitlab.com:token", "")
	require.NoError(t, err)
	assert.Equal(t, "glpat-ci-explicit", got)
}

func Test_useKeyringDeprecatedFallsBackToFileWhenUnavailable(t *testing.T) {
	keyring.MockInitWithError(errors.New("keyring unavailable"))
	t.Cleanup(keyring.MockInit)
	t.Setenv("GITLAB_CI", "")
	t.Setenv("CI", "")

	d := t.TempDir()
	t.Setenv("GLAB_CONFIG_DIR", d)

	io, _, _, stderr := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(io, currentUserFactoryOption(t))
	cfg := config.NewBlankConfigInDir(d)
	f.ConfigStub = func() config.Config { return cfg }
	cmd := NewCmdLogin(f)
	cmd.Flags().BoolP("help", "x", false, "")

	cmd.SetArgs([]string{"--use-keyring", "--token", "glpat-deprecated"})

	_, err := cmd.ExecuteC()

	// The deprecated --use-keyring no longer errors when the keyring is
	// unavailable; it warns and falls back to plaintext file storage like the
	// default does.
	require.NoError(t, err)
	assert.Contains(t, stderr.String(), "The operating system keyring is unavailable")
	data, err := os.ReadFile(filepath.Join(d, "config.yml"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "glpat-deprecated")
}

func Test_tokenLogin_persistsUsername(t *testing.T) {
	keyring.MockInit()

	login := func(t *testing.T, cfg config.Config, opts ...cmdtest.FactoryOption) (*bytes.Buffer, error) {
		t.Helper()
		t.Setenv("GLAB_CONFIG_DIR", t.TempDir())

		io, _, _, stderr := cmdtest.TestIOStreams()
		cmd := NewCmdLogin(cmdtest.NewTestFactory(io, append([]cmdtest.FactoryOption{cmdtest.WithConfig(cfg)}, opts...)...))
		cmd.Flags().BoolP("help", "x", false, "")
		cmd.SetArgs([]string{"--hostname", "gitlab.com", "--token", "glpat-1234"})

		_, err := cmd.ExecuteC()
		return stderr, err
	}

	t.Run("records the account the token belongs to", func(t *testing.T) {
		cfg := config.NewBlankConfigInDir(t.TempDir())

		stderr, err := login(t, cfg, currentUserFactoryOption(t))
		require.NoError(t, err)

		user, err := cfg.Get("gitlab.com", "user")
		require.NoError(t, err)
		assert.Equal(t, "john_smith", user)
		assert.Contains(t, stderr.String(), "Logged in as john_smith")
	})

	// The lookup is best-effort so that `--token` keeps working for scripted
	// and CI logins that cannot reach the API.
	t.Run("still stores the token when the lookup fails", func(t *testing.T) {
		cfg := config.NewBlankConfigInDir(t.TempDir())

		tc := gitlabtesting.NewTestClient(t)
		tc.MockUsers.EXPECT().
			CurrentUser(gomock.Any()).
			Return(nil, nil, errors.New("no such host")).
			AnyTimes()
		apiClient := cmdtest.NewTestApiClient(t, nil, "", "gitlab.com", api.WithGitLabClient(tc.Client))

		stderr, err := login(t, cfg, cmdtest.WithApiClient(apiClient))
		require.NoError(t, err)

		token, err := cfg.Get("gitlab.com", "token")
		require.NoError(t, err)
		assert.Equal(t, "glpat-1234", token)

		user, err := cfg.Get("gitlab.com", "user")
		require.NoError(t, err)
		assert.Empty(t, user, "the credential helper falls back to a placeholder for this case")
		assert.Contains(t, stderr.String(), "Could not look up the username for this token")
	})
}

func Test_tokenLogin_warnsWhenEnvTokenTakesPrecedence(t *testing.T) {
	keyring.MockInit()

	login := func(t *testing.T) *bytes.Buffer {
		t.Helper()
		t.Setenv("GLAB_CONFIG_DIR", t.TempDir())
		cfg := config.NewBlankConfigInDir(t.TempDir())

		io, _, _, stderr := cmdtest.TestIOStreams()
		cmd := NewCmdLogin(cmdtest.NewTestFactory(io, cmdtest.WithConfig(cfg), currentUserFactoryOption(t)))
		cmd.Flags().BoolP("help", "x", false, "")
		cmd.SetArgs([]string{"--hostname", "gitlab.com", "--token", "glpat-1234"})

		_, err := cmd.ExecuteC()
		require.NoError(t, err)
		return stderr
	}

	t.Run("warns and points at removing the variable", func(t *testing.T) {
		t.Setenv("GITLAB_TOKEN", "glpat-from-env")

		stderr := login(t)

		assert.Contains(t, stderr.String(), "The environment variable GITLAB_TOKEN is set and takes precedence")
		assert.Contains(t, stderr.String(), "Run type glab to find the source: an alias such as 'op plugin run -- glab' means a wrapper (for example, a 1Password shell plugin) is injecting it, which is expected and needs no action.")
		assert.Contains(t, stderr.String(), "remove it there so glab uses your stored credentials")
	})

	t.Run("stays quiet when no token environment variable is set", func(t *testing.T) {
		// Clear every env var that maps to the "token" key. Setting them to the
		// empty string is equivalent to unsetting them here: GetFromEnvWithSource
		// only treats a non-empty value as a source.
		for _, e := range config.EnvKeyEquivalence("token") {
			t.Setenv(e, "")
		}

		stderr := login(t)

		assert.NotContains(t, stderr.String(), "takes precedence")
	})
}

func Test_initialAPIHostname(t *testing.T) {
	t.Run("flag wins over saved value", func(t *testing.T) {
		cfg := config.NewBlankConfigInDir(t.TempDir())
		require.NoError(t, cfg.Set("gl.io", "api_host", "saved.gl.io"))

		got := initialAPIHostname(cfg, "gl.io", "flag.gl.io")

		assert.Equal(t, "flag.gl.io", got)
	})

	t.Run("saved value wins over hostname when flag empty", func(t *testing.T) {
		cfg := config.NewBlankConfigInDir(t.TempDir())
		require.NoError(t, cfg.Set("gl.io", "api_host", "saved.gl.io"))

		got := initialAPIHostname(cfg, "gl.io", "")

		assert.Equal(t, "saved.gl.io", got)
	})

	t.Run("falls back to hostname when nothing saved and flag empty", func(t *testing.T) {
		cfg := config.NewBlankConfigInDir(t.TempDir())

		got := initialAPIHostname(cfg, "gl.io", "")

		assert.Equal(t, "gl.io", got)
	})
}

func Test_initialSSHHostname(t *testing.T) {
	t.Run("saved value is used", func(t *testing.T) {
		cfg := config.NewBlankConfigInDir(t.TempDir())
		require.NoError(t, cfg.Set("gl.io", "ssh_host", "ssh.gl.io"))

		got := initialSSHHostname(cfg, "gl.io")

		assert.Equal(t, "ssh.gl.io", got)
	})

	t.Run("uses SSH host detected from git remotes when nothing saved", func(t *testing.T) {
		t.Chdir(t.TempDir())
		require.NoError(t, exec.Command("git", "init").Run())
		require.NoError(t, exec.Command("git", "remote", "add", "origin", "ssh://ssh.gl.io/owner/repo.git").Run())
		cfg := config.NewBlankConfigInDir(t.TempDir())

		got := initialSSHHostname(cfg, "gl.io")

		assert.Equal(t, "ssh.gl.io", got)
	})

	t.Run("falls back to hostname when nothing saved and no git detection", func(t *testing.T) {
		t.Chdir(t.TempDir())
		cfg := config.NewBlankConfigInDir(t.TempDir())

		got := initialSSHHostname(cfg, "gl.io")

		assert.Equal(t, "gl.io", got)
	})
}

func Test_initialContainerRegistryDomains(t *testing.T) {
	t.Run("saved value is used", func(t *testing.T) {
		cfg := config.NewBlankConfigInDir(t.TempDir())
		require.NoError(t, cfg.Set("gitlab.com", "container_registry_domains", "my.custom.registry"))

		got := initialContainerRegistryDomains(cfg, "gitlab.com")

		assert.Equal(t, "my.custom.registry", got)
	})

	t.Run("falls back to hostname-derived default when nothing saved", func(t *testing.T) {
		cfg := config.NewBlankConfigInDir(t.TempDir())

		got := initialContainerRegistryDomains(cfg, "gitlab.com")

		assert.Equal(t, "gitlab.com,gitlab.com:443,registry.gitlab.com", got)
	})
}

func Test_persistProtocolFlags(t *testing.T) {
	t.Run("both flags set writes both keys lowercased", func(t *testing.T) {
		cfg := config.NewBlankConfigInDir(t.TempDir())

		err := persistProtocolFlags(cfg, "gl.io", "SSH", "HTTPS")
		require.NoError(t, err)

		gitProto, _ := cfg.Get("gl.io", "git_protocol")
		assert.Equal(t, "ssh", gitProto)
		apiProto, _ := cfg.Get("gl.io", "api_protocol")
		assert.Equal(t, "https", apiProto)
	})

	t.Run("only git flag set leaves api untouched", func(t *testing.T) {
		cfg := config.NewBlankConfigInDir(t.TempDir())
		require.NoError(t, cfg.Set("gl.io", "api_protocol", "http"))

		err := persistProtocolFlags(cfg, "gl.io", "ssh", "")
		require.NoError(t, err)

		gitProto, _ := cfg.Get("gl.io", "git_protocol")
		assert.Equal(t, "ssh", gitProto)
		apiProto, _ := cfg.Get("gl.io", "api_protocol")
		assert.Equal(t, "http", apiProto)
	})

	t.Run("only api flag set leaves git untouched", func(t *testing.T) {
		cfg := config.NewBlankConfigInDir(t.TempDir())
		require.NoError(t, cfg.Set("gl.io", "git_protocol", "ssh"))

		err := persistProtocolFlags(cfg, "gl.io", "", "http")
		require.NoError(t, err)

		gitProto, _ := cfg.Get("gl.io", "git_protocol")
		assert.Equal(t, "ssh", gitProto)
		apiProto, _ := cfg.Get("gl.io", "api_protocol")
		assert.Equal(t, "http", apiProto)
	})

	t.Run("neither flag set leaves config untouched", func(t *testing.T) {
		cfg := config.NewBlankConfigInDir(t.TempDir())
		require.NoError(t, cfg.Set("gl.io", "git_protocol", "ssh"))
		require.NoError(t, cfg.Set("gl.io", "api_protocol", "https"))

		err := persistProtocolFlags(cfg, "gl.io", "", "")
		require.NoError(t, err)

		gitProto, _ := cfg.Get("gl.io", "git_protocol")
		assert.Equal(t, "ssh", gitProto)
		apiProto, _ := cfg.Get("gl.io", "api_protocol")
		assert.Equal(t, "https", apiProto)
	})
}
