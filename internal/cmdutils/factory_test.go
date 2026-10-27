//go:build !integration

package cmdutils

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

func TestFactory_ResolveHostNameFromConfig(t *testing.T) {
	// GIVEN
	isolateHost(t)
	cfg := config.NewFromString(heredoc.Doc(`
		host: gitlab.example.com
	`))

	// WHEN
	f := NewFactory(nil, false, cfg, api.BuildInfo{})

	// THEN
	assert.Equal(t, "gitlab.example.com", f.defaultHostname)
}

func TestFactory_ResolveHostNameFromEnv(t *testing.T) {
	// GIVEN
	isolateHost(t)
	t.Setenv("GITLAB_HOST", "gitlab.example.com")
	cfg := config.NewFromString(heredoc.Doc(`
		host: another.gitlab.example.com
	`))

	// WHEN
	f := NewFactory(nil, false, cfg, api.BuildInfo{})

	// THEN
	assert.Equal(t, "gitlab.example.com", f.defaultHostname)
}

func TestFactory_ResolveToGitLabComByDefault(t *testing.T) {
	// GIVEN
	isolateHost(t)
	cfg := config.NewBlankConfig()

	// WHEN
	f := NewFactory(nil, false, cfg, api.BuildInfo{})

	// THEN
	assert.Equal(t, "gitlab.com", f.defaultHostname)
}

func TestFactory_GitLabClientUsesCorrectHost(t *testing.T) {
	// GIVEN
	tests := []struct {
		name                         string
		cfg                          config.Config
		env                          map[string]string
		expectedGitLabClientHostname *url.URL
	}{
		{
			name:                         "default",
			cfg:                          config.NewBlankConfig(),
			expectedGitLabClientHostname: mustURL(t, "https://gitlab.com/api/v4/"),
		},
		{
			name: "host from config",
			cfg: config.NewFromString(heredoc.Doc(`
				host: gitlab.example.com
			`)),
			expectedGitLabClientHostname: mustURL(t, "https://gitlab.example.com/api/v4/"),
		},
		{
			name: "host from env",
			env:  map[string]string{"GITLAB_HOST": "gitlab.example.com"},
			cfg: config.NewFromString(heredoc.Doc(`
				host: another.gitlab.example.com
			`)),
			expectedGitLabClientHostname: mustURL(t, "https://gitlab.example.com/api/v4/"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// GIVEN
			isolateHost(t)
			if tt.env != nil {
				for k, v := range tt.env {
					t.Setenv(k, v)
				}
			}

			// WHEN
			f := NewFactory(nil, false, tt.cfg, api.BuildInfo{})
			c, err := f.GitLabClient()
			require.NoError(t, err)

			// THEN
			assert.Equal(t, tt.expectedGitLabClientHostname, c.BaseURL())
		})
	}
}

func TestFactory_ApiClientsReuseConnection(t *testing.T) {
	// GIVEN
	var conns atomic.Int64
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			conns.Add(1)
		}
	}
	srv.StartTLS()
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "https://")
	cfg := config.NewFromString(fmt.Sprintf(`hosts:
  %q:
    api_host: %q
    token: glpat-test
    skip_tls_verify: true
`, host, host))
	f := NewFactory(nil, false, cfg, api.BuildInfo{})

	// WHEN
	for range 2 {
		c, err := f.ApiClient(host)
		require.NoError(t, err)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, http.NoBody)
		require.NoError(t, err)
		resp, err := c.HTTPClient().Do(req)
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	}

	// THEN
	assert.Equal(t, int64(1), conns.Load())
}

func TestFactory_ConfigHostDoesNotOverrideRemote(t *testing.T) {
	// GIVEN
	isolateHost(t)
	require.NoError(t, exec.Command("git", "remote", "add", "origin", "https://gitlab.com/owner/repo.git").Run())
	cfg := config.NewFromString(heredoc.Doc(`
		host: gitlab.example.com
	`))

	// WHEN
	f := NewFactory(nil, false, cfg, api.BuildInfo{})

	// THEN
	assert.Equal(t, "gitlab.com", f.DefaultHostname())
	repo, err := f.BaseRepo()
	require.NoError(t, err)
	assert.Equal(t, "gitlab.com", repo.RepoHost())

	require.NoError(t, f.RepoOverride("group/proj"))
	repo, err = f.BaseRepo()
	require.NoError(t, err)
	assert.Equal(t, "gitlab.com", repo.RepoHost())
	assert.Equal(t, "group/proj", repo.FullName())
}

func TestFactory_EnvHostOverridesRemote(t *testing.T) {
	// GIVEN
	isolateHost(t)
	require.NoError(t, exec.Command("git", "remote", "add", "origin", "https://gitlab.com/owner/repo.git").Run())
	t.Setenv("GITLAB_HOST", "gitlab.example.com")
	cfg := config.NewBlankConfig()

	// WHEN
	f := NewFactory(nil, false, cfg, api.BuildInfo{})

	// THEN
	assert.Equal(t, "gitlab.example.com", f.DefaultHostname())
	_, err := f.BaseRepo()
	require.ErrorContains(t, err, "GITLAB_HOST is currently set to gitlab.example.com")

	require.NoError(t, f.RepoOverride("group/proj"))
	repo, err := f.BaseRepo()
	require.NoError(t, err)
	assert.Equal(t, "gitlab.example.com", repo.RepoHost())
	assert.Equal(t, "group/proj", repo.FullName())
}

func TestFactory_CIHostOverridesRemote(t *testing.T) {
	// GIVEN
	isolateHost(t)
	require.NoError(t, exec.Command("git", "remote", "add", "origin", "https://gitlab.com/owner/repo.git").Run())
	t.Setenv("GLAB_ENABLE_CI_AUTOLOGIN", "true")
	t.Setenv("GITLAB_CI", "true")
	t.Setenv("CI_SERVER_FQDN", "gitlab.example.com")
	cfg := config.NewBlankConfig()

	// WHEN
	f := NewFactory(nil, false, cfg, api.BuildInfo{})

	// THEN
	assert.Equal(t, "gitlab.example.com", f.DefaultHostname())
	_, err := f.BaseRepo()
	require.ErrorContains(t, err, "correspond to the CI_SERVER_FQDN environment variable")
	require.ErrorContains(t, err, "CI_SERVER_FQDN is currently set to gitlab.example.com")
}

// isolateHost keeps the developer's environment and the glab checkout's own
// remotes out of host resolution.
func isolateHost(t *testing.T) {
	t.Helper()

	git.InitGitRepo(t)
	t.Setenv("GLAB_ENABLE_CI_AUTOLOGIN", "")
	for _, key := range config.EnvKeyEquivalence("host") {
		t.Setenv(key, "")
	}
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()

	u, err := url.Parse(s)
	require.NoError(t, err)

	return u
}

// TestFactory_ApiClientEnvTokenWarning pins that a token from the environment
// is only sent to trusted hosts (see config.IsTrustedHost), and that withholding
// it is reported once per host on stderr.
func TestFactory_ApiClientEnvTokenWarning(t *testing.T) {
	for _, e := range []string{"GITLAB_TOKEN", "GITLAB_ACCESS_TOKEN", "OAUTH_TOKEN", "JOB_TOKEN", "GITLAB_HOST", "GITLAB_URI", "GL_HOST", "GITLAB_REPO"} {
		t.Setenv(e, "")
	}

	newFactoryWithStderr := func(cfg config.Config) (*DefaultFactory, *bytes.Buffer) {
		stderr := &bytes.Buffer{}
		ios := iostreams.New(
			iostreams.WithStdin(io.NopCloser(&bytes.Buffer{}), false),
			iostreams.WithStdout(&bytes.Buffer{}, false),
			iostreams.WithStderr(stderr, false),
		)
		return NewFactory(ios, false, cfg, api.BuildInfo{}), stderr
	}

	t.Run("warns when the environment token does not apply to the host", func(t *testing.T) {
		t.Setenv("GITLAB_TOKEN", "env-token")
		f, stderr := newFactoryWithStderr(config.NewBlankConfig())

		client, err := f.ApiClient("attacker.example.com")
		require.NoError(t, err)

		assert.Contains(t, stderr.String(), "GITLAB_TOKEN is set")
		assert.Contains(t, stderr.String(), `"attacker.example.com"`)
		assert.Contains(t, stderr.String(), "did not send the credential")
		assert.Contains(t, stderr.String(), "glab auth login --hostname attacker.example.com")

		// And the client carries no credential.
		_, _, err = client.AuthSource().Header(t.Context())
		require.ErrorContains(t, err, "unauthenticated")
	})

	t.Run("warns when JOB_TOKEN alone does not apply to the host", func(t *testing.T) {
		t.Setenv("JOB_TOKEN", "env-job-token")
		f, stderr := newFactoryWithStderr(config.NewBlankConfig())

		_, err := f.ApiClient("attacker.example.com")
		require.NoError(t, err)

		assert.Contains(t, stderr.String(), "JOB_TOKEN is set")
		assert.Contains(t, stderr.String(), "did not send the credential")
	})

	t.Run("lists every credential env var that was withheld", func(t *testing.T) {
		t.Setenv("GITLAB_TOKEN", "env-token")
		t.Setenv("JOB_TOKEN", "env-job-token")
		f, stderr := newFactoryWithStderr(config.NewBlankConfig())

		_, err := f.ApiClient("attacker.example.com")
		require.NoError(t, err)

		assert.Contains(t, stderr.String(), "GITLAB_TOKEN, JOB_TOKEN is set")
	})

	t.Run("stays quiet for the default host", func(t *testing.T) {
		t.Setenv("GITLAB_TOKEN", "env-token")
		f, stderr := newFactoryWithStderr(config.NewBlankConfig())

		_, err := f.ApiClient("gitlab.com")
		require.NoError(t, err)
		assert.Empty(t, stderr.String())
	})

	t.Run("stays quiet for a configured host", func(t *testing.T) {
		t.Setenv("GITLAB_TOKEN", "env-token")
		f, stderr := newFactoryWithStderr(config.NewFromString("hosts:\n  gitlab.example.com:\n    user: someone\n"))

		_, err := f.ApiClient("gitlab.example.com")
		require.NoError(t, err)
		assert.Empty(t, stderr.String())
	})

	t.Run("stays quiet without a token environment variable", func(t *testing.T) {
		t.Setenv("GITLAB_TOKEN", "")
		f, stderr := newFactoryWithStderr(config.NewBlankConfig())

		_, err := f.ApiClient("attacker.example.com")
		require.NoError(t, err)
		assert.Empty(t, stderr.String())
	})

	t.Run("sends the token to a GITLAB_HOST with a subfolder", func(t *testing.T) {
		isolateHost(t)
		t.Setenv("GITLAB_TOKEN", "env-token")
		t.Setenv("GITLAB_HOST", "http://127.0.0.1:8099/gitlab")
		f, stderr := newFactoryWithStderr(config.NewBlankConfig())

		client, err := f.ApiClient("")
		require.NoError(t, err)
		assert.Empty(t, stderr.String())

		_, value, err := client.AuthSource().Header(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "env-token", value)
	})

	t.Run("warns once per host when several clients are built", func(t *testing.T) {
		isolateHost(t)
		t.Setenv("GITLAB_TOKEN", "env-token")
		f, stderr := newFactoryWithStderr(config.NewBlankConfig())
		require.NoError(t, f.RepoOverride("https://attacker.example.com/group/proj"))

		_, err := f.GitLabClient()
		require.NoError(t, err)
		_, err = f.ApiClient("attacker.example.com")
		require.NoError(t, err)
		assert.Equal(t, 1, strings.Count(stderr.String(), "is neither the default host"))
	})

	t.Run("sends the token to the host of a GITLAB_REPO URL", func(t *testing.T) {
		isolateHost(t)
		t.Setenv("GITLAB_TOKEN", "env-token")
		t.Setenv("GITLAB_REPO", "https://gitlab.example.com/group/proj")
		f, stderr := newFactoryWithStderr(config.NewBlankConfig())
		require.NoError(t, f.RepoOverride("https://gitlab.example.com/group/proj"))

		repo, err := f.BaseRepo()
		require.NoError(t, err)
		client, err := f.ApiClient(repo.RepoHost())
		require.NoError(t, err)
		assert.Empty(t, stderr.String())

		_, value, err := client.AuthSource().Header(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "env-token", value)
	})

	t.Run("warns from GitLabClient when a repository override names an unconfigured host", func(t *testing.T) {
		isolateHost(t)
		t.Setenv("GITLAB_TOKEN", "env-token")
		f, stderr := newFactoryWithStderr(config.NewBlankConfig())
		require.NoError(t, f.RepoOverride("https://attacker.example.com/group/proj"))

		_, err := f.GitLabClient()
		require.NoError(t, err)
		assert.Contains(t, stderr.String(), "GITLAB_TOKEN is set")
		assert.Contains(t, stderr.String(), `"attacker.example.com"`)
	})
}
