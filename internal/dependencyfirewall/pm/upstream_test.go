//go:build !integration

package pm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestUpstreamProxyResolvers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		manager PackageManager
		env     []string
		want    upstreamProxy
		wantOK  bool
	}{
		{
			name:    "npm https-proxy wins over proxy and HTTPS_PROXY",
			manager: NPM(),
			env:     []string{"HTTPS_PROXY=http://env:1", "npm_config_proxy=http://proxy:2", "npm_config_https_proxy=http://https-proxy:3"},
			want:    upstreamProxy{Source: "npm_config_https_proxy", URL: "http://https-proxy:3"},
			wantOK:  true,
		},
		{
			name:    "npm proxy wins over HTTPS_PROXY",
			manager: NPM(),
			env:     []string{"HTTPS_PROXY=http://env:1", "npm_config_proxy=http://proxy:2"},
			want:    upstreamProxy{Source: "npm_config_proxy", URL: "http://proxy:2"},
			wantOK:  true,
		},
		{
			name:    "npm config keys match case-insensitively",
			manager: Pnpm(),
			env:     []string{"NPM_CONFIG_HTTPS_PROXY=http://https-proxy:3"},
			want:    upstreamProxy{Source: "NPM_CONFIG_HTTPS_PROXY", URL: "http://https-proxy:3"},
			wantOK:  true,
		},
		{
			name:    "npm empty value is unset",
			manager: NPM(),
			env:     []string{"npm_config_https_proxy=", "npm_config_proxy=http://proxy:2"},
			want:    upstreamProxy{Source: "npm_config_proxy", URL: "http://proxy:2"},
			wantOK:  true,
		},
		{
			name:    "npm noproxy and NO_PROXY both apply",
			manager: NPM(),
			env:     []string{"npm_config_https_proxy=http://https-proxy:3", "npm_config_noproxy=internal.example", "NO_PROXY=mirror.example"},
			want:    upstreamProxy{Source: "npm_config_https_proxy", URL: "http://https-proxy:3", NoProxy: "internal.example,mirror.example"},
			wantOK:  true,
		},
		{
			name:    "npm HTTPS_PROXY keeps npm noproxy",
			manager: NPM(),
			env:     []string{"HTTPS_PROXY=http://env:1", "npm_config_noproxy=internal.example"},
			want:    upstreamProxy{Source: "HTTPS_PROXY", URL: "http://env:1", NoProxy: "internal.example"},
			wantOK:  true,
		},
		{
			name:    "npm without a proxy",
			manager: NPM(),
			env:     []string{"npm_config_noproxy=internal.example"},
		},
		{
			name:    "yarn YARN_HTTPS_PROXY wins and ignores NO_PROXY",
			manager: Yarn(),
			env:     []string{"npm_config_https_proxy=http://https-proxy:3", "YARN_HTTPS_PROXY=http://yarn:4", "NO_PROXY=mirror.example"},
			want:    upstreamProxy{Source: "YARN_HTTPS_PROXY", URL: "http://yarn:4"},
			wantOK:  true,
		},
		{
			name:    "yarn reads yarn_https_proxy in any case",
			manager: Yarn(),
			env:     []string{"yarn_https_proxy=http://yarn:4"},
			want:    upstreamProxy{Source: "yarn_https_proxy", URL: "http://yarn:4"},
			wantOK:  true,
		},
		{
			name:    "yarn classic npm_config_proxy",
			manager: Yarn(),
			env:     []string{"npm_config_proxy=http://proxy:2"},
			want:    upstreamProxy{Source: "npm_config_proxy", URL: "http://proxy:2"},
			wantOK:  true,
		},
		{
			name:    "yarn leaves HTTPS_PROXY to the default",
			manager: Yarn(),
			env:     []string{"HTTPS_PROXY=http://env:1"},
		},
		{
			name:    "pip PIP_PROXY ignores NO_PROXY",
			manager: Pipenv(),
			env:     []string{"HTTPS_PROXY=http://env:1", "PIP_PROXY=http://pip:5", "NO_PROXY=pypi.org"},
			want:    upstreamProxy{Source: "PIP_PROXY", URL: "http://pip:5"},
			wantOK:  true,
		},
		{
			name:    "pip without PIP_PROXY",
			manager: Pip(),
			env:     []string{"HTTPS_PROXY=http://env:1"},
		},
		{
			name:    "cargo CARGO_HTTP_PROXY keeps no_proxy",
			manager: Cargo(),
			env:     []string{"CARGO_HTTP_PROXY=cargo:6", "no_proxy=index.crates.io"},
			want:    upstreamProxy{Source: "CARGO_HTTP_PROXY", URL: "cargo:6", NoProxy: "index.crates.io"},
			wantOK:  true,
		},
		{
			name:    "cargo prefers NO_PROXY over no_proxy",
			manager: Cargo(),
			env:     []string{"CARGO_HTTP_PROXY=cargo:6", "NO_PROXY=upper.example", "no_proxy=lower.example"},
			want:    upstreamProxy{Source: "CARGO_HTTP_PROXY", URL: "cargo:6", NoProxy: "upper.example"},
			wantOK:  true,
		},
		{
			name:    "npm proxy=false falls back to HTTPS_PROXY",
			manager: NPM(),
			env:     []string{"HTTPS_PROXY=http://env:1", "npm_config_proxy=false"},
			want:    upstreamProxy{Source: "HTTPS_PROXY", URL: "http://env:1"},
			wantOK:  true,
		},
		{
			name:    "npm https-proxy=null falls back to proxy",
			manager: NPM(),
			env:     []string{"npm_config_https_proxy=null", "npm_config_proxy=http://proxy:2"},
			want:    upstreamProxy{Source: "npm_config_proxy", URL: "http://proxy:2"},
			wantOK:  true,
		},
		{
			name:    "npm noproxy key matches case-insensitively",
			manager: NPM(),
			env:     []string{"npm_config_https_proxy=http://https-proxy:3", "NPM_CONFIG_NOPROXY=internal.example"},
			want:    upstreamProxy{Source: "npm_config_https_proxy", URL: "http://https-proxy:3", NoProxy: "internal.example"},
			wantOK:  true,
		},
		{
			name:    "yarn classic https-proxy wins over proxy",
			manager: Yarn(),
			env:     []string{"npm_config_proxy=http://proxy:2", "npm_config_https_proxy=http://https-proxy:3"},
			want:    upstreamProxy{Source: "npm_config_https_proxy", URL: "http://https-proxy:3"},
			wantOK:  true,
		},
		{
			name:    "yarn classic proxy=false leaves HTTPS_PROXY to the default",
			manager: Yarn(),
			env:     []string{"HTTPS_PROXY=http://env:1", "npm_config_proxy=false"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resolver, ok := tt.manager.(upstreamProxyResolver)
			require.True(t, ok)
			got, gotOK := resolver.resolveUpstreamProxy(tt.env)
			assert.Equal(t, tt.wantOK, gotOK)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPlainHTTPProxyResolvers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		env    []string
		want   upstreamProxy
		wantOK bool
	}{
		{
			name: "yarn ignores YARN_HTTP_PROXY for plain HTTP",
			env:  []string{"YARN_HTTP_PROXY=http://berry:7", "HTTP_PROXY=http://env:1"},
		},
		{
			name:   "yarn classic npm_config_proxy",
			env:    []string{"npm_config_proxy=http://proxy:2"},
			want:   upstreamProxy{Source: "npm_config_proxy", URL: "http://proxy:2"},
			wantOK: true,
		},
		{
			name: "yarn HTTPS settings leave plain HTTP to the default",
			env:  []string{"YARN_HTTPS_PROXY=http://yarn:4", "npm_config_https_proxy=http://https-proxy:3", "HTTP_PROXY=http://env:1"},
		},
		{
			name: "yarn classic proxy=false leaves plain HTTP to the default",
			env:  []string{"npm_config_proxy=false"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resolver, ok := Yarn().(plainHTTPProxyResolver)
			require.True(t, ok)
			got, gotOK := resolver.resolvePlainHTTPProxy(tt.env)
			assert.Equal(t, tt.wantOK, gotOK)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestManagersWithoutOwnProxySettingHaveNoResolver(t *testing.T) {
	t.Parallel()
	// These read no proxy variable of their own, so the inspection proxy's
	// default, glab's HTTPS_PROXY and HTTP_PROXY, applies to them.
	for _, m := range []PackageManager{Poetry(), Uv(), Twine(), Gem(), Bundle(), Maven(), Gradle(), Golang()} {
		_, ok := m.(upstreamProxyResolver)
		assert.False(t, ok, m.Name())
		assert.Nil(t, OwnProxyVariables(m), m.Name())
	}
}

func TestPlainHTTPUsesHTTPSProxy(t *testing.T) {
	t.Parallel()
	assert.True(t, PlainHTTPUsesHTTPSProxy(NPM()))
	assert.True(t, PlainHTTPUsesHTTPSProxy(Pnpm()))
	assert.False(t, PlainHTTPUsesHTTPSProxy(Yarn()))
	assert.False(t, PlainHTTPUsesHTTPSProxy(Pip()))
	assert.False(t, PlainHTTPUsesHTTPSProxy(Pipenv()))
	assert.False(t, PlainHTTPUsesHTTPSProxy(Cargo()))
	assert.False(t, PlainHTTPUsesHTTPSProxy(Poetry()))
	assert.False(t, PlainHTTPUsesHTTPSProxy(Maven()))
}

func TestOwnProxyVariablesAreRead(t *testing.T) {
	t.Parallel()
	for _, m := range []PackageManager{NPM(), Pnpm(), Yarn(), Pip(), Pipenv(), Cargo()} {
		resolver, ok := m.(upstreamProxyResolver)
		require.True(t, ok, m.Name())
		vars := OwnProxyVariables(m)
		require.NotEmpty(t, vars, m.Name())
		for _, v := range vars {
			env := []string{v + "=http://own:1"}
			https, _ := resolver.resolveUpstreamProxy(env)
			plain := https
			if r, ok := m.(plainHTTPProxyResolver); ok {
				plain, _ = r.resolvePlainHTTPProxy(env)
			}
			assert.True(t, https.Source == v || plain.Source == v, "%s lists %s but reads it for neither scheme", m.Name(), v)
		}
	}
}

func TestUpstreamProxyFunc(t *testing.T) {
	t.Parallel()
	pick, err := upstreamProxy{URL: "corp:3128", NoProxy: "internal.example"}.proxyFunc()
	require.NoError(t, err)

	got, err := pick(httptest.NewRequest(http.MethodGet, "https://registry.npmjs.org/left-pad", http.NoBody))
	require.NoError(t, err)
	assert.Equal(t, "http://corp:3128", got.String())

	got, err = pick(httptest.NewRequest(http.MethodGet, "https://npm.internal.example/left-pad", http.NoBody))
	require.NoError(t, err)
	assert.Nil(t, got, "a NoProxy host is reached directly")
}

func TestUpstreamProxyFuncAcceptsSupportedSchemes(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"https://corp:443", "socks5://corp:1080", "socks5h://corp:1080"} {
		_, err := upstreamProxy{URL: raw}.proxyFunc()
		assert.NoError(t, err, raw)
	}
}

func TestUpstreamProxyFuncRejectsBadURLWithoutLeakingIt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		raw     string
		wantErr string
	}{
		{raw: "http://user:s3cret@[::1", wantErr: "not a valid proxy URL"},
		{raw: "http://user:s3cret@", wantErr: "not a valid proxy URL"},
		{raw: "http://", wantErr: "not a valid proxy URL"},
		{raw: ":3128", wantErr: "not a valid proxy URL"},
		{raw: "ftp://user:s3cret@corp:21", wantErr: `unsupported proxy scheme "ftp"`},
	}
	for _, tt := range tests {
		_, err := upstreamProxy{URL: tt.raw}.proxyFunc()
		require.EqualError(t, err, tt.wantErr, tt.raw)
	}
}

func TestRunRejectsInvalidManagerProxy(t *testing.T) {
	t.Setenv("GITLAB_CI", "") // honor fake mode even when the suite runs in CI
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
	t.Setenv("PIP_PROXY", "ftp://corp:21")
	ios, _, _, _ := cmdtest.TestIOStreams()
	exec := &fakeExecutor{}

	err := Run(t.Context(), Pip(), RunOptions{
		IO:        ios,
		Executor:  exec,
		BaseDir:   t.TempDir(),
		ProjectID: "group/proj",
		Args:      []string{"install", "six"},
	})
	require.ErrorContains(t, err, "invalid outbound proxy in PIP_PROXY")
	assert.Nil(t, exec.env, "the package manager must not run")
}

// refusingProxy records the target of each CONNECT and refuses it, so a test
// can see where upstream traffic was sent without reaching the internet.
type refusingProxy struct {
	mu      sync.Mutex
	targets []string
}

func (rp *refusingProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp.mu.Lock()
	rp.targets = append(rp.targets, r.Host)
	rp.mu.Unlock()
	w.WriteHeader(http.StatusForbidden)
}

func (rp *refusingProxy) Targets() []string {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	return append([]string(nil), rp.targets...)
}

func TestRunSendsRegistryTrafficThroughManagersOwnProxy(t *testing.T) {
	t.Setenv("GITLAB_CI", "") // honor fake mode even when the suite runs in CI
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
	corporate := &refusingProxy{}
	server := httptest.NewServer(corporate)
	t.Cleanup(server.Close)
	corporateURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("npm_config_https_proxy", corporateURL.String())

	ios, _, _, _ := cmdtest.TestIOStreams()
	// The fetch fails because the corporate proxy refuses the tunnel; the test
	// only checks where the inspection proxy sent it.
	_ = Run(t.Context(), NPM(), RunOptions{
		IO:        ios,
		Executor:  &curlingExecutor{path: "/left-pad/-/left-pad-1.3.0.tgz"},
		BaseDir:   t.TempDir(),
		ProjectID: "group/proj",
		Args:      []string{"install", "left-pad"},
	})

	assert.Equal(t, []string{"registry.npmjs.org:443"}, corporate.Targets())
}

// plainGettingExecutor ignores the package-manager binary and GETs url through
// the HTTP_PROXY it is handed, as a package manager does for a plain-HTTP
// registry.
type plainGettingExecutor struct{ url string }

func (plainGettingExecutor) LookPath(file string) (string, error) { return "/bin/" + file, nil }

func (e plainGettingExecutor) ExecWithIO(ctx context.Context, _ string, _ []string, env []string, _ io.Reader, _, _ io.Writer) error {
	proxyURL, err := url.Parse(lookupEnv(env, "HTTP_PROXY"))
	if err != nil {
		return err
	}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.url, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func TestRunSendsPlainHTTPThroughManagersPlainHTTPProxy(t *testing.T) {
	tests := []struct {
		name    string
		manager PackageManager
		// env maps a variable to the proxy it points at: "want" must carry the
		// plain-HTTP request and "other" must not see it.
		env map[string]string
	}{
		{
			name:    "yarn npm_config_proxy, not YARN_HTTPS_PROXY",
			manager: Yarn(),
			env:     map[string]string{"YARN_HTTPS_PROXY": "other", "npm_config_proxy": "want"},
		},
		{
			name:    "yarn without a plain-HTTP setting falls back to HTTP_PROXY",
			manager: Yarn(),
			env:     map[string]string{"YARN_HTTPS_PROXY": "other", "HTTP_PROXY": "want"},
		},
		{
			name:    "yarn YARN_HTTP_PROXY does not carry plain HTTP",
			manager: Yarn(),
			env:     map[string]string{"YARN_HTTP_PROXY": "other", "HTTP_PROXY": "want"},
		},
		{
			name:    "npm HTTPS_PROXY carries plain HTTP ahead of HTTP_PROXY",
			manager: NPM(),
			env:     map[string]string{"HTTPS_PROXY": "want", "HTTP_PROXY": "other"},
		},
		{
			name:    "pnpm HTTPS_PROXY carries plain HTTP ahead of HTTP_PROXY",
			manager: Pnpm(),
			env:     map[string]string{"HTTPS_PROXY": "want", "HTTP_PROXY": "other"},
		},
		{
			name:    "npm https-proxy carries plain HTTP too",
			manager: NPM(),
			env:     map[string]string{"npm_config_https_proxy": "want", "HTTP_PROXY": "other"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GITLAB_CI", "") // honor fake mode even when the suite runs in CI
			t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
			for _, k := range []string{
				"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy",
				"npm_config_https_proxy", "npm_config_proxy", "YARN_HTTPS_PROXY", "YARN_HTTP_PROXY", "YARN_PROXY",
			} {
				t.Setenv(k, "")
			}
			proxies := map[string]*refusingProxy{"want": {}, "other": {}}
			for k, which := range tt.env {
				server := httptest.NewServer(proxies[which])
				t.Cleanup(server.Close)
				t.Setenv(k, server.URL)
			}

			ios, _, _, _ := cmdtest.TestIOStreams()
			_ = Run(t.Context(), tt.manager, RunOptions{
				IO:        ios,
				Executor:  plainGettingExecutor{url: "http://registry.example/left-pad"},
				BaseDir:   t.TempDir(),
				ProjectID: "group/proj",
				Args:      []string{"install", "left-pad"},
			})

			assert.Equal(t, []string{"registry.example"}, proxies["want"].Targets())
			assert.Empty(t, proxies["other"].Targets())
		})
	}
}
