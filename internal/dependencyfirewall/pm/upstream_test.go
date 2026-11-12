//go:build !integration

package pm

import (
	"cmp"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
		{
			name:    "yarn classic https-proxy=false connects directly",
			manager: Yarn(),
			env:     []string{"HTTPS_PROXY=http://env:1", "npm_config_proxy=http://proxy:2", "npm_config_https_proxy=false"},
			want:    upstreamProxy{Source: "npm_config_https_proxy"},
			wantOK:  true,
		},
		{
			name:    "yarn classic YARN_HTTPS_PROXY=false connects directly",
			manager: Yarn(),
			env:     []string{"npm_config_https_proxy=http://https-proxy:3", "YARN_HTTPS_PROXY=false"},
			want:    upstreamProxy{Source: "YARN_HTTPS_PROXY"},
			wantOK:  true,
		},
		{
			name:    "cargo empty CARGO_HTTP_PROXY connects directly",
			manager: Cargo(),
			env:     []string{"HTTPS_PROXY=http://env:1", "CARGO_HTTP_PROXY="},
			want:    upstreamProxy{Source: "CARGO_HTTP_PROXY"},
			wantOK:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resolver, ok := tt.manager.(upstreamProxyResolver)
			require.True(t, ok)
			got, gotOK, err := resolver.resolveUpstreamProxy(tt.env, t.TempDir(), nil)
			require.NoError(t, err)
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
		{
			name:   "yarn classic https-proxy=false leaves plain HTTP on proxy",
			env:    []string{"npm_config_https_proxy=false", "npm_config_proxy=http://proxy:2"},
			want:   upstreamProxy{Source: "npm_config_proxy", URL: "http://proxy:2"},
			wantOK: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resolver, ok := Yarn().(plainHTTPProxyResolver)
			require.True(t, ok)
			got, gotOK, err := resolver.resolvePlainHTTPProxy(tt.env, t.TempDir())
			require.NoError(t, err)
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
		assert.Nil(t, OwnProxyConfigFiles(m), m.Name())
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
			// No HOME or PATH, so no config file can supply the proxy instead.
			env := []string{v + "=http://own:1"}
			https, _, err := resolver.resolveUpstreamProxy(env, t.TempDir(), nil)
			require.NoError(t, err)
			plain := https
			if r, ok := m.(plainHTTPProxyResolver); ok {
				plain, _, err = r.resolvePlainHTTPProxy(env, t.TempDir())
				require.NoError(t, err)
			}
			assert.True(t, https.Source == v || plain.Source == v, "%s lists %s but reads it for neither scheme", m.Name(), v)
		}
	}
}

func TestOwnProxyConfigFilesAreRead(t *testing.T) {
	t.Parallel()
	// Each file the help lists, written with a proxy where the manager looks
	// for it. root is the temp directory; HOME is root/home and the manager
	// runs in root/proj.
	berry := map[string]string{"proj/package.json": `{"packageManager": "yarn@4.9.2"}`}
	fixtures := map[string]struct {
		path, content, key string
		env                []string
		extra              map[string]string
	}{
		"~/.npmrc":                {path: "home/.npmrc", content: "https-proxy=http://own:1\n", key: "https-proxy"},
		"$PREFIX/etc/npmrc":       {path: "prefix/etc/npmrc", content: "https-proxy=http://own:1\n", key: "https-proxy", env: []string{"PREFIX=$ROOT/prefix"}},
		"~/.yarnrc":               {path: "home/.yarnrc", content: "https-proxy \"http://own:1\"\n", key: "https-proxy"},
		"$PREFIX/etc/yarnrc":      {path: "prefix/etc/yarnrc", content: "https-proxy \"http://own:1\"\n", key: "https-proxy", env: []string{"PREFIX=$ROOT/prefix"}},
		"~/.yarnrc.yml":           {path: "home/.yarnrc.yml", content: "httpsProxy: http://own:1\n", key: "httpsProxy", extra: berry},
		"pip.conf":                {path: "home/.config/pip/pip.conf", content: "[global]\nproxy = http://own:1\n", key: "global.proxy"},
		"$PIP_CONFIG_FILE":        {path: "custom/pip.conf", content: "[global]\nproxy = http://own:1\n", key: "global.proxy", env: []string{"PIP_CONFIG_FILE=$ROOT/custom/pip.conf"}},
		"$CARGO_HOME/config.toml": {path: "cargo/config.toml", content: "[http]\nproxy = \"http://own:1\"\n", key: "http.proxy", env: []string{"CARGO_HOME=$ROOT/cargo"}},
		"~/.gitconfig":            {path: "home/.gitconfig", content: "[http]\n\tproxy = http://own:1\n", key: "http.proxy"},
	}
	managers := []PackageManager{NPM(), Yarn(), Cargo()}
	if runtime.GOOS != "windows" {
		managers = append(managers, Pip(), Pipenv())
	}
	for _, m := range managers {
		files := OwnProxyConfigFiles(m)
		require.NotEmpty(t, files, m.Name())
		var cases []configCase
		for _, f := range files {
			fixture, ok := fixtures[f]
			require.True(t, ok, "no fixture for %s, which the %s help lists", f, m.Name())
			written := map[string]string{fixture.path: fixture.content}
			maps.Copy(written, fixture.extra)
			cases = append(cases, configCase{
				name:   f,
				files:  written,
				env:    fixture.env,
				args:   []string{"install"},
				want:   upstreamProxy{Source: "$ROOT/" + fixture.path + " (" + fixture.key + ")", URL: "http://own:1"},
				wantOK: true,
			})
		}
		t.Run(m.Name(), func(t *testing.T) {
			t.Parallel()
			runConfigCases(t, m, cases)
		})
	}
	assert.Nil(t, OwnProxyConfigFiles(Pnpm()))
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
		{raw: "ftp://user:s3cret@corp:21", wantErr: "unsupported proxy scheme; use http, https, socks5 or socks5h"},
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

// isolateUserConfig points HOME and the other places the resolvers find user
// and global config at empty directories, so the developer's own files
// cannot change where a Run test sends registry traffic.
func isolateUserConfig(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("PREFIX", t.TempDir())
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_CONFIG_DIRS", "CARGO_HOME", "PIP_CONFIG_FILE", "NPM_CONFIG_USERCONFIG", "npm_config_userconfig", "YARN_USERCONFIG", "yarn_userconfig"} {
		t.Setenv(k, "")
	}
}

// clearProxyEnv leaves glab's environment without an outbound proxy of any
// kind. CARGO_HTTP_PROXY and YARN_HTTPS_PROXY are unset rather than emptied,
// because an empty one makes cargo or Yarn Berry connect directly.
func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy",
		"npm_config_https_proxy", "npm_config_proxy", "YARN_HTTPS_PROXY", "YARN_HTTP_PROXY", "YARN_PROXY",
		"PIP_PROXY", "CARGO_HTTP_PROXY",
	} {
		t.Setenv(k, "")
	}
	require.NoError(t, os.Unsetenv("CARGO_HTTP_PROXY"))
	require.NoError(t, os.Unsetenv("YARN_HTTPS_PROXY"))
}

func TestRunSendsRegistryTrafficThroughManagersOwnProxy(t *testing.T) {
	t.Setenv("GITLAB_CI", "") // honor fake mode even when the suite runs in CI
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
	corporate := &refusingProxy{}
	server := httptest.NewServer(corporate)
	t.Cleanup(server.Close)
	corporateURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	isolateUserConfig(t, t.TempDir())
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

// gettingExecutor ignores the package-manager binary and GETs url through the
// proxy it is handed, as a package manager does: HTTP_PROXY for a plain-HTTP
// url, else HTTPS_PROXY, trusting the proxy CA named by caVar.
type gettingExecutor struct{ url, caVar string }

func (gettingExecutor) LookPath(file string) (string, error) { return "/bin/" + file, nil }

func (e gettingExecutor) ExecWithIO(ctx context.Context, _ string, _ []string, env []string, _ io.Reader, _, _ io.Writer) error {
	transport := &http.Transport{}
	proxyVar := "HTTP_PROXY"
	if strings.HasPrefix(e.url, "https:") {
		proxyVar = "HTTPS_PROXY"
		pemBytes, err := os.ReadFile(lookupEnv(env, e.caVar))
		if err != nil {
			return err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemBytes) {
			return fmt.Errorf("no proxy CA in %s", e.caVar)
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	proxyURL, err := url.Parse(lookupEnv(env, proxyVar))
	if err != nil {
		return err
	}
	transport.Proxy = http.ProxyURL(proxyURL)
	client := &http.Client{Transport: transport}
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
			isolateUserConfig(t, t.TempDir())
			clearProxyEnv(t)
			proxies := map[string]*refusingProxy{"want": {}, "other": {}}
			for k, which := range tt.env {
				server := httptest.NewServer(proxies[which])
				t.Cleanup(server.Close)
				t.Setenv(k, server.URL)
			}

			ios, _, _, _ := cmdtest.TestIOStreams()
			_ = Run(t.Context(), tt.manager, RunOptions{
				IO:        ios,
				Executor:  gettingExecutor{url: "http://registry.example/left-pad"},
				BaseDir:   t.TempDir(),
				ProjectID: "group/proj",
				Args:      []string{"install", "left-pad"},
			})

			assert.Equal(t, []string{"registry.example"}, proxies["want"].Targets())
			assert.Empty(t, proxies["other"].Targets())
		})
	}
}

func TestRunSendsRegistryTrafficThroughUserConfigNotProjectConfig(t *testing.T) {
	berry := `{"packageManager": "yarn@4.9.2"}`
	tests := []struct {
		name    string
		manager PackageManager
		url     string
		caVar   string
		// files maps a path under the temp root, where HOME is root/home and
		// the manager runs in dir (default root/proj), to its content. links
		// maps a symlink to its target. $USER and $PROJECT stand for the two
		// proxies: only $USER may see the request.
		files map[string]string
		links map[string]string
		env   []string
		dir   string
	}{
		{
			name:    "npm",
			manager: NPM(),
			url:     "https://registry.invalid/left-pad",
			caVar:   "NODE_EXTRA_CA_CERTS",
			files:   map[string]string{"home/.npmrc": "https-proxy=$USER\n", "proj/package.json": "{}", "proj/.npmrc": "https-proxy=$PROJECT\n"},
		},
		{
			name:    "yarn classic",
			manager: Yarn(),
			url:     "https://registry.invalid/left-pad",
			caVar:   "NODE_EXTRA_CA_CERTS",
			files:   map[string]string{"home/.yarnrc": "https-proxy \"$USER\"\n", "proj/package.json": "{}", "proj/.yarnrc": "https-proxy \"$PROJECT\"\n", "proj/.npmrc": "https-proxy=$PROJECT\n"},
		},
		{
			name:    "yarn berry",
			manager: Yarn(),
			url:     "https://registry.invalid/left-pad",
			caVar:   "NODE_EXTRA_CA_CERTS",
			files:   map[string]string{"home/.yarnrc.yml": "httpsProxy: $USER\n", "proj/package.json": berry, "proj/.yarnrc.yml": "httpsProxy: $PROJECT\n"},
		},
		{
			name:    "pip",
			manager: Pip(),
			url:     "https://registry.invalid/simple/six/",
			caVar:   "PIP_CERT",
			files: map[string]string{
				"home/.config/pip/pip.conf": "[global]\nproxy = $USER\n",
				"proj/.venv/bin/pip":        "",
				"proj/.venv/pyvenv.cfg":     "",
				"proj/.venv/pip.conf":       "[global]\nproxy = $PROJECT\n",
			},
			env: []string{"PATH=$ROOT/proj/.venv/bin"},
		},
		{
			name:    "cargo",
			manager: Cargo(),
			url:     "https://registry.invalid/config.json",
			caVar:   "CARGO_HTTP_CAINFO",
			files:   map[string]string{"home/.cargo/config.toml": "[http]\nproxy = \"$USER\"\n", "proj/.cargo/config.toml": "[http]\nproxy = \"$PROJECT\"\n"},
		},
		{
			name:    "cargo with CARGO_HOME inside the project",
			manager: Cargo(),
			url:     "https://registry.invalid/config.json",
			caVar:   "CARGO_HTTP_CAINFO",
			files:   map[string]string{"proj/.cargo/config.toml": "[http]\nproxy = \"$PROJECT\"\n", "home/.gitconfig": "[http]\n\tproxy = $USER\n"},
			env:     []string{"CARGO_HOME=$ROOT/proj/.cargo"},
		},
		{
			name:    "npm with NPM_CONFIG_USERCONFIG inside the project",
			manager: NPM(),
			url:     "https://registry.invalid/left-pad",
			caVar:   "NODE_EXTRA_CA_CERTS",
			files:   map[string]string{"proj/.npmrc-ci": "https-proxy=$PROJECT\n"},
			env:     []string{"NPM_CONFIG_USERCONFIG=$ROOT/proj/.npmrc-ci", "HTTPS_PROXY=$USER"},
		},
		{
			name:    "npm with PREFIX inside the project",
			manager: NPM(),
			url:     "https://registry.invalid/left-pad",
			caVar:   "NODE_EXTRA_CA_CERTS",
			files:   map[string]string{"proj/tools/etc/npmrc": "https-proxy=$PROJECT\n", "home/.npmrc": "proxy=$USER\n"},
			env:     []string{"PREFIX=$ROOT/proj/tools"},
		},
		{
			name:    "npm with a user file that links into the project",
			manager: NPM(),
			url:     "https://registry.invalid/left-pad",
			caVar:   "NODE_EXTRA_CA_CERTS",
			files:   map[string]string{"proj/.npmrc": "https-proxy=$PROJECT\n"},
			links:   map[string]string{"home/.npmrc": "proj/.npmrc"},
			env:     []string{"HTTPS_PROXY=$USER"},
		},
		{
			name:    "npm with HOME inside the project",
			manager: NPM(),
			url:     "https://registry.invalid/left-pad",
			caVar:   "NODE_EXTRA_CA_CERTS",
			files:   map[string]string{"proj/home/.npmrc": "https-proxy=$PROJECT\n"},
			env:     []string{"HOME=$ROOT/proj/home", "HTTPS_PROXY=$USER"},
		},
		{
			name:    "pip with PIP_CONFIG_FILE inside the project",
			manager: Pip(),
			url:     "https://registry.invalid/simple/six/",
			caVar:   "PIP_CERT",
			files:   map[string]string{"proj/pip.conf": "[global]\nproxy = $PROJECT\n"},
			env:     []string{"PIP_CONFIG_FILE=$ROOT/proj/pip.conf", "HTTPS_PROXY=$USER"},
		},
		{
			name:    "pip from a subdirectory of the git work tree",
			manager: Pip(),
			url:     "https://registry.invalid/simple/six/",
			caVar:   "PIP_CERT",
			files: map[string]string{
				"proj/.git/HEAD":            "",
				"home/.config/pip/pip.conf": "[global]\nproxy = $USER\n",
				"proj/.venv/bin/pip":        "",
				"proj/.venv/pyvenv.cfg":     "",
				"proj/.venv/pip.conf":       "[global]\nproxy = $PROJECT\n",
			},
			env: []string{"PATH=$ROOT/proj/.venv/bin"},
			dir: "proj/sub",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.manager.Name() == "pip" && runtime.GOOS == "windows" {
				t.Skip("pip's user config lives under %APPDATA% on Windows")
			}
			if len(tt.links) > 0 && runtime.GOOS == "windows" {
				t.Skip("symlinks need privileges on Windows")
			}
			t.Setenv("GITLAB_CI", "") // honor fake mode even when the suite runs in CI
			t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
			user, project := &refusingProxy{}, &refusingProxy{}
			userServer, projectServer := httptest.NewServer(user), httptest.NewServer(project)
			t.Cleanup(userServer.Close)
			t.Cleanup(projectServer.Close)
			root := t.TempDir()
			expand := strings.NewReplacer("$ROOT", root, "$USER", userServer.URL, "$PROJECT", projectServer.URL).Replace
			writeConfigFiles(t, root, tt.files, expand)
			for link, target := range tt.links {
				require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, link)), 0o755))
				require.NoError(t, os.Symlink(filepath.Join(root, target), filepath.Join(root, link)))
			}
			dir := filepath.Join(root, cmp.Or(tt.dir, "proj"))
			require.NoError(t, os.MkdirAll(dir, 0o755))
			isolateUserConfig(t, filepath.Join(root, "home"))
			clearProxyEnv(t)
			for _, e := range tt.env {
				k, v, _ := strings.Cut(expand(e), "=")
				t.Setenv(k, v)
			}

			ios, _, _, _ := cmdtest.TestIOStreams()
			_ = Run(t.Context(), tt.manager, RunOptions{
				IO:        ios,
				Executor:  gettingExecutor{url: tt.url, caVar: tt.caVar},
				BaseDir:   dir,
				ProjectID: "group/proj",
				Args:      []string{"install", "left-pad"},
			})

			assert.NotEmpty(t, user.Targets(), "the proxy from the user's config must carry the request")
			assert.Empty(t, project.Targets(), "a proxy from a file in the project must never see the request")
		})
	}
}

func TestRunConnectsDirectlyWhenTheManagerDisablesItsProxy(t *testing.T) {
	tests := []struct {
		name    string
		manager PackageManager
		caVar   string
		files   map[string]string
		env     []string
		// wantDefault is set for the control case: with no setting of the
		// manager's own, HTTPS_PROXY must carry the request.
		wantDefault bool
	}{
		{
			name:    "cargo with an empty CARGO_HTTP_PROXY",
			manager: Cargo(),
			caVar:   "CARGO_HTTP_CAINFO",
			env:     []string{"CARGO_HTTP_PROXY="},
		},
		{
			name:    "cargo with an empty http.proxy",
			manager: Cargo(),
			caVar:   "CARGO_HTTP_CAINFO",
			files:   map[string]string{"home/.cargo/config.toml": "[http]\nproxy = \"\"\n"},
		},
		{
			name:    "yarn classic with https-proxy false",
			manager: Yarn(),
			caVar:   "NODE_EXTRA_CA_CERTS",
			files:   map[string]string{"home/.yarnrc": "https-proxy false\n"},
		},
		{
			name:    "yarn berry with an empty httpsProxy",
			manager: Yarn(),
			caVar:   "NODE_EXTRA_CA_CERTS",
			files:   map[string]string{"proj/package.json": `{"packageManager": "yarn@4.9.2"}`, "home/.yarnrc.yml": "httpsProxy: \"\"\n"},
		},
		{
			name:        "cargo without a proxy of its own",
			manager:     Cargo(),
			caVar:       "CARGO_HTTP_CAINFO",
			wantDefault: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GITLAB_CI", "") // honor fake mode even when the suite runs in CI
			t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
			glabProxy := &refusingProxy{}
			server := httptest.NewServer(glabProxy)
			t.Cleanup(server.Close)
			root := t.TempDir()
			writeConfigFiles(t, root, tt.files, func(s string) string { return s })
			require.NoError(t, os.MkdirAll(filepath.Join(root, "proj"), 0o755))
			isolateUserConfig(t, filepath.Join(root, "home"))
			clearProxyEnv(t)
			t.Setenv("HTTPS_PROXY", server.URL)
			for _, e := range tt.env {
				k, v, _ := strings.Cut(e, "=")
				t.Setenv(k, v)
			}

			ios, _, _, _ := cmdtest.TestIOStreams()
			// registry.invalid never resolves, so a direct request fails
			// without leaving the machine.
			err := Run(t.Context(), tt.manager, RunOptions{
				IO:        ios,
				Executor:  gettingExecutor{url: "https://registry.invalid/left-pad", caVar: tt.caVar},
				BaseDir:   filepath.Join(root, "proj"),
				ProjectID: "group/proj",
				Args:      []string{"install", "left-pad"},
			})

			_, ran := errors.AsType[*ExitError](err)
			require.True(t, ran, "the package manager must run: %v", err)
			if tt.wantDefault {
				assert.Equal(t, []string{"registry.invalid:443"}, glabProxy.Targets())
			} else {
				assert.Empty(t, glabProxy.Targets(), "a direct setting must not fall back to HTTPS_PROXY")
			}
		})
	}
}
