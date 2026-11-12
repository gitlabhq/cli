//go:build !integration

package pm

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "pm-system-root")
	if err != nil {
		panic(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	systemRoot = dir
	m.Run()
}

// configCase is one precedence case: files to write under a temp root,
// symlinks to make there (link to target), the environment, and the directory
// the manager runs in, relative to the root. HOME is root/home and the
// default directory is root/proj. "$ROOT" in file contents and env values is
// replaced with the root. plain resolves the plain-HTTP proxy instead of the
// HTTPS one.
type configCase struct {
	name    string
	files   map[string]string
	links   map[string]string
	env     []string
	dir     string
	args    []string
	plain   bool
	want    upstreamProxy
	wantOK  bool
	wantErr string
}

// writeConfigFiles writes each file under root, executable when it is a bin/
// entry, after passing its content through expand.
func writeConfigFiles(t *testing.T, root string, files map[string]string, expand func(string) string) {
	t.Helper()
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		mode := os.FileMode(0o600)
		if filepath.Base(filepath.Dir(path)) == "bin" {
			mode = 0o755
		}
		require.NoError(t, os.WriteFile(path, []byte(expand(content)), mode))
	}
}

func runConfigCases(t *testing.T, manager PackageManager, cases []configCase) {
	t.Helper()
	resolver, ok := manager.(upstreamProxyResolver)
	require.True(t, ok)
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Resolved, because the node lookup reports a symlink-free path.
			root, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			expand := func(s string) string { return strings.ReplaceAll(s, "$ROOT", root) }
			writeConfigFiles(t, root, tt.files, expand)
			for link, target := range tt.links {
				require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, link)), 0o755))
				require.NoError(t, os.Symlink(filepath.Join(root, target), filepath.Join(root, link)))
			}
			env := []string{"HOME=" + filepath.Join(root, "home")}
			for _, e := range tt.env {
				env = append(env, expand(e))
			}
			dir := filepath.Join(root, "proj")
			if tt.dir != "" {
				dir = filepath.Join(root, filepath.FromSlash(tt.dir))
			}
			require.NoError(t, os.MkdirAll(dir, 0o755))

			var got upstreamProxy
			var gotOK bool
			if tt.plain {
				plain, ok := manager.(plainHTTPProxyResolver)
				require.True(t, ok)
				got, gotOK, err = plain.resolvePlainHTTPProxy(env, dir)
			} else {
				got, gotOK, err = resolver.resolveUpstreamProxy(env, dir, tt.args)
			}
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantOK, gotOK)
			tt.want.Source = expand(filepath.FromSlash(tt.want.Source))
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNpmUpstreamProxyFromConfigFiles(t *testing.T) {
	t.Parallel()
	runConfigCases(t, NPM(), []configCase{
		{
			name:   "the user .npmrc sets https-proxy",
			files:  map[string]string{"home/.npmrc": "https-proxy=http://user:1\n"},
			want:   upstreamProxy{Source: "$ROOT/home/.npmrc (https-proxy)", URL: "http://user:1"},
			wantOK: true,
		},
		{
			name:   "npm_config_https_proxy wins over the user .npmrc",
			files:  map[string]string{"home/.npmrc": "https-proxy=http://user:1\n"},
			env:    []string{"npm_config_https_proxy=http://env:2"},
			want:   upstreamProxy{Source: "npm_config_https_proxy", URL: "http://env:2"},
			wantOK: true,
		},
		{
			name:   "https-proxy in a file wins over proxy in the environment",
			files:  map[string]string{"home/.npmrc": "https-proxy=http://user:1\n"},
			env:    []string{"npm_config_proxy=http://env:2"},
			want:   upstreamProxy{Source: "$ROOT/home/.npmrc (https-proxy)", URL: "http://user:1"},
			wantOK: true,
		},
		{
			name:   "proxy in a file wins over HTTPS_PROXY",
			files:  map[string]string{"home/.npmrc": "proxy=http://user:1\n"},
			env:    []string{"HTTPS_PROXY=http://env:2"},
			want:   upstreamProxy{Source: "$ROOT/home/.npmrc (proxy)", URL: "http://user:1"},
			wantOK: true,
		},
		{
			name:   "the user .npmrc wins over the global npmrc",
			files:  map[string]string{"home/.npmrc": "https-proxy=http://user:1\n", "prefix/etc/npmrc": "https-proxy=http://global:2\n"},
			env:    []string{"PREFIX=$ROOT/prefix"},
			want:   upstreamProxy{Source: "$ROOT/home/.npmrc (https-proxy)", URL: "http://user:1"},
			wantOK: true,
		},
		{
			name:   "NPM_CONFIG_USERCONFIG names the user file",
			files:  map[string]string{"other/npmrc": "https-proxy=http://other:1\n", "home/.npmrc": "https-proxy=http://user:2\n"},
			env:    []string{"NPM_CONFIG_USERCONFIG=$ROOT/other/npmrc"},
			want:   upstreamProxy{Source: "$ROOT/other/npmrc (https-proxy)", URL: "http://other:1"},
			wantOK: true,
		},
		{
			name:   "global https-proxy under PREFIX wins over a user proxy",
			files:  map[string]string{"prefix/etc/npmrc": "https-proxy=http://global:1\n", "home/.npmrc": "proxy=http://user:2\n"},
			env:    []string{"PREFIX=$ROOT/prefix"},
			want:   upstreamProxy{Source: "$ROOT/prefix/etc/npmrc (https-proxy)", URL: "http://global:1"},
			wantOK: true,
		},
		{
			name:   "prefix in the user .npmrc moves the global file",
			files:  map[string]string{"custom/etc/npmrc": "https-proxy=http://global:1\n", "home/.npmrc": "prefix=$ROOT/custom\n"},
			want:   upstreamProxy{Source: "$ROOT/custom/etc/npmrc (https-proxy)", URL: "http://global:1"},
			wantOK: true,
		},
		{
			name:   "global prefix defaults to the node binary's install prefix",
			files:  map[string]string{"node/bin/node": "", "node/etc/npmrc": "https-proxy=http://global:1\n"},
			env:    []string{"PATH=$ROOT/node/bin"},
			want:   upstreamProxy{Source: "$ROOT/node/etc/npmrc (https-proxy)", URL: "http://global:1"},
			wantOK: true,
		},
		{
			name:   "an empty https-proxy hides lower layers and falls back to proxy",
			files:  map[string]string{"home/.npmrc": "https-proxy=\n", "prefix/etc/npmrc": "https-proxy=http://global:1\nproxy=http://global:2\n"},
			env:    []string{"PREFIX=$ROOT/prefix"},
			want:   upstreamProxy{Source: "$ROOT/prefix/etc/npmrc (proxy)", URL: "http://global:2"},
			wantOK: true,
		},
		{
			name:   "https-proxy=null in a file falls back to proxy",
			files:  map[string]string{"home/.npmrc": "https-proxy=null\nproxy=http://user:2\n"},
			want:   upstreamProxy{Source: "$ROOT/home/.npmrc (proxy)", URL: "http://user:2"},
			wantOK: true,
		},
		{
			name:   "proxy=false in a file falls back to HTTPS_PROXY",
			files:  map[string]string{"home/.npmrc": "proxy=false\n"},
			env:    []string{"HTTPS_PROXY=http://env:1"},
			want:   upstreamProxy{Source: "HTTPS_PROXY", URL: "http://env:1"},
			wantOK: true,
		},
		{
			name:   "values expand environment variables",
			files:  map[string]string{"home/.npmrc": "https-proxy=${CORP_PROXY}\n"},
			env:    []string{"CORP_PROXY=http://corp:1"},
			want:   upstreamProxy{Source: "$ROOT/home/.npmrc (https-proxy)", URL: "http://corp:1"},
			wantOK: true,
		},
		{
			name:   "quoted values and comments",
			files:  map[string]string{"home/.npmrc": "; comment\nhttps-proxy = \"http://corp:1\"\nnoproxy=internal.example ; trailing\n"},
			want:   upstreamProxy{Source: "$ROOT/home/.npmrc (https-proxy)", URL: "http://corp:1", NoProxy: "internal.example"},
			wantOK: true,
		},
		{
			name:   "noproxy from a file and NO_PROXY both apply",
			files:  map[string]string{"home/.npmrc": "https-proxy=http://corp:1\nnoproxy[]=a.example\nnoproxy[]=b.example\n"},
			env:    []string{"NO_PROXY=c.example"},
			want:   upstreamProxy{Source: "$ROOT/home/.npmrc (https-proxy)", URL: "http://corp:1", NoProxy: "a.example,b.example,c.example"},
			wantOK: true,
		},
		{
			name:  "keys in a section are not top-level settings",
			files: map[string]string{"home/.npmrc": "[section]\nhttps-proxy=http://corp:1\n"},
		},
		{
			name:   "the project .npmrc is not read",
			files:  map[string]string{"proj/package.json": "{}", "proj/.npmrc": "https-proxy=http://project:1\n"},
			env:    []string{"HTTPS_PROXY=http://env:2"},
			want:   upstreamProxy{Source: "HTTPS_PROXY", URL: "http://env:2"},
			wantOK: true,
		},
		{
			name:   "the project .npmrc does not override the user .npmrc",
			files:  map[string]string{"proj/package.json": "{}", "proj/.npmrc": "https-proxy=http://project:1\n", "home/.npmrc": "https-proxy=http://user:2\n"},
			dir:    "proj/sub",
			want:   upstreamProxy{Source: "$ROOT/home/.npmrc (https-proxy)", URL: "http://user:2"},
			wantOK: true,
		},
		{
			name:  "the project .npmrc cannot move the user or global file",
			files: map[string]string{"proj/package.json": "{}", "proj/.npmrc": "userconfig=$ROOT/evil/npmrc\nglobalconfig=$ROOT/evil/npmrc\nprefix=$ROOT/evil\n", "evil/npmrc": "https-proxy=http://evil:1\n", "evil/etc/npmrc": "https-proxy=http://evil:2\n"},
		},
		{
			name:   "NPM_CONFIG_USERCONFIG inside the project is not read",
			files:  map[string]string{"proj/.npmrc-ci": "https-proxy=http://project:1\n", "prefix/etc/npmrc": "proxy=http://global:2\n"},
			env:    []string{"NPM_CONFIG_USERCONFIG=$ROOT/proj/.npmrc-ci", "PREFIX=$ROOT/prefix"},
			want:   upstreamProxy{Source: "$ROOT/prefix/etc/npmrc (proxy)", URL: "http://global:2"},
			wantOK: true,
		},
		{
			name:   "a PREFIX inside the project is not read",
			files:  map[string]string{"proj/tools/etc/npmrc": "https-proxy=http://project:1\n", "home/.npmrc": "proxy=http://user:2\n"},
			env:    []string{"PREFIX=$ROOT/proj/tools"},
			want:   upstreamProxy{Source: "$ROOT/home/.npmrc (proxy)", URL: "http://user:2"},
			wantOK: true,
		},
		{
			name:   "a HOME inside the project is not read",
			files:  map[string]string{"proj/home/.npmrc": "https-proxy=http://project:1\n"},
			env:    []string{"HOME=$ROOT/proj/home", "HTTPS_PROXY=http://env:2"},
			want:   upstreamProxy{Source: "HTTPS_PROXY", URL: "http://env:2"},
			wantOK: true,
		},
		{
			name:   "a user file that links into the project is not read",
			files:  map[string]string{"proj/.npmrc": "https-proxy=http://project:1\n"},
			links:  map[string]string{"home/.npmrc": "proj/.npmrc"},
			env:    []string{"HTTPS_PROXY=http://env:2"},
			want:   upstreamProxy{Source: "HTTPS_PROXY", URL: "http://env:2"},
			wantOK: true,
		},
		{
			name:   "the project is the git work tree around the working directory",
			files:  map[string]string{"proj/.git/HEAD": "", "proj/ci/npmrc": "https-proxy=http://project:1\n"},
			dir:    "proj/sub",
			env:    []string{"NPM_CONFIG_USERCONFIG=$ROOT/proj/ci/npmrc", "HTTPS_PROXY=http://env:2"},
			want:   upstreamProxy{Source: "HTTPS_PROXY", URL: "http://env:2"},
			wantOK: true,
		},
		{
			name:   "outside a git work tree the project is the working directory",
			files:  map[string]string{"proj/ci/npmrc": "https-proxy=http://ci:1\n"},
			dir:    "proj/sub",
			env:    []string{"NPM_CONFIG_USERCONFIG=$ROOT/proj/ci/npmrc"},
			want:   upstreamProxy{Source: "$ROOT/proj/ci/npmrc (https-proxy)", URL: "http://ci:1"},
			wantOK: true,
		},
	})
}

func TestPnpmReadsOnlyNpmConfigEnvironment(t *testing.T) {
	t.Parallel()
	runConfigCases(t, Pnpm(), []configCase{
		{
			name:  "npm config files are ignored",
			files: map[string]string{"proj/.npmrc": "https-proxy=http://project:1\n", "home/.npmrc": "proxy=http://user:2\n"},
		},
		{
			name:   "npm_config_https_proxy still applies",
			files:  map[string]string{"home/.npmrc": "https-proxy=http://user:1\n"},
			env:    []string{"npm_config_https_proxy=http://env:2"},
			want:   upstreamProxy{Source: "npm_config_https_proxy", URL: "http://env:2"},
			wantOK: true,
		},
	})
}

func TestYarnClassicUpstreamProxyFromConfigFiles(t *testing.T) {
	t.Parallel()
	runConfigCases(t, Yarn(), []configCase{
		{
			name:   "the user .yarnrc https-proxy wins over the user .npmrc https-proxy",
			files:  map[string]string{"home/.yarnrc": "https-proxy \"http://yarnrc:1\"\n", "home/.npmrc": "https-proxy=http://npmrc:2\n"},
			want:   upstreamProxy{Source: "$ROOT/home/.yarnrc (https-proxy)", URL: "http://yarnrc:1"},
			wantOK: true,
		},
		{
			name:   "the user .yarnrc https-proxy wins over npm_config_https_proxy",
			files:  map[string]string{"home/.yarnrc": "https-proxy \"http://yarnrc:1\"\n"},
			env:    []string{"npm_config_https_proxy=http://env:2"},
			want:   upstreamProxy{Source: "$ROOT/home/.yarnrc (https-proxy)", URL: "http://yarnrc:1"},
			wantOK: true,
		},
		{
			name:   ".npmrc https-proxy wins over .yarnrc proxy",
			files:  map[string]string{"home/.yarnrc": "proxy \"http://yarnrc:1\"\n", "home/.npmrc": "https-proxy=http://npmrc:2\n"},
			want:   upstreamProxy{Source: "$ROOT/home/.npmrc (https-proxy)", URL: "http://npmrc:2"},
			wantOK: true,
		},
		{
			name:   "YARN_HTTPS_PROXY wins over files",
			files:  map[string]string{"home/.yarnrc": "https-proxy \"http://yarnrc:1\"\n"},
			env:    []string{"YARN_HTTPS_PROXY=http://env:2"},
			want:   upstreamProxy{Source: "YARN_HTTPS_PROXY", URL: "http://env:2"},
			wantOK: true,
		},
		{
			name:   "YARN_PROXY wins over the user .yarnrc proxy and HTTPS_PROXY",
			files:  map[string]string{"home/.yarnrc": "proxy \"http://yarnrc:1\"\n"},
			env:    []string{"YARN_PROXY=http://env:2", "HTTPS_PROXY=http://https:3"},
			want:   upstreamProxy{Source: "YARN_PROXY", URL: "http://env:2"},
			wantOK: true,
		},
		{
			name:   "YARN_HTTPS_PROXY wins over YARN_PROXY",
			env:    []string{"YARN_PROXY=http://env:2", "YARN_HTTPS_PROXY=http://env:3"},
			want:   upstreamProxy{Source: "YARN_HTTPS_PROXY", URL: "http://env:3"},
			wantOK: true,
		},
		{
			name:   "YARN_PROXY in any case wins over npm_config_proxy for plain HTTP",
			env:    []string{"npm_config_proxy=http://npm:2", "yarn_proxy=http://env:3"},
			plain:  true,
			want:   upstreamProxy{Source: "yarn_proxy", URL: "http://env:3"},
			wantOK: true,
		},
		{
			name:   "the user .yarnrc wins over the global yarnrc",
			files:  map[string]string{"home/.yarnrc": "https-proxy: \"http://user:1\"\n", "prefix/etc/yarnrc": "https-proxy \"http://global:2\"\n"},
			env:    []string{"PREFIX=$ROOT/prefix"},
			want:   upstreamProxy{Source: "$ROOT/home/.yarnrc (https-proxy)", URL: "http://user:1"},
			wantOK: true,
		},
		{
			name:   "the user .npmrc wins over the global npmrc",
			files:  map[string]string{"home/.npmrc": "https-proxy=http://user:1\n", "prefix/etc/npmrc": "https-proxy=http://global:2\n"},
			env:    []string{"PREFIX=$ROOT/prefix"},
			want:   upstreamProxy{Source: "$ROOT/home/.npmrc (https-proxy)", URL: "http://user:1"},
			wantOK: true,
		},
		{
			name:   "YARN_USERCONFIG names the user .yarnrc",
			files:  map[string]string{"other/yarnrc": "https-proxy \"http://other:1\"\n", "home/.yarnrc": "https-proxy \"http://user:2\"\n"},
			env:    []string{"YARN_USERCONFIG=$ROOT/other/yarnrc"},
			want:   upstreamProxy{Source: "$ROOT/other/yarnrc (https-proxy)", URL: "http://other:1"},
			wantOK: true,
		},
		{
			name:   "https-proxy false connects directly",
			files:  map[string]string{"home/.yarnrc": "https-proxy false\nproxy \"http://yarnrc:1\"\n"},
			want:   upstreamProxy{Source: "$ROOT/home/.yarnrc (https-proxy)"},
			wantOK: true,
		},
		{
			name:   "https-proxy null in .npmrc falls back to proxy",
			files:  map[string]string{"home/.npmrc": "https-proxy=null\nproxy=http://npmrc:1\n"},
			want:   upstreamProxy{Source: "$ROOT/home/.npmrc (proxy)", URL: "http://npmrc:1"},
			wantOK: true,
		},
		{
			name:  "proxy false in .npmrc leaves HTTPS_PROXY to the default",
			files: map[string]string{"home/.npmrc": "proxy=false\n"},
			env:   []string{"HTTPS_PROXY=http://env:1"},
		},
		{
			name:  "NO_PROXY does not apply and HTTPS_PROXY is left to the default",
			files: map[string]string{"home/.yarnrc": "network-timeout 600000\n"},
			env:   []string{"HTTPS_PROXY=http://env:1"},
		},
		{
			name:    "an unset variable in the user .npmrc fails as in yarn",
			files:   map[string]string{"home/.npmrc": "https-proxy=${MISSING}\n"},
			wantErr: "unset environment variable",
		},
		{
			name:   "an unquoted URL leaves .yarnrc without settings, as in yarn",
			files:  map[string]string{"home/.yarnrc": "https-proxy http://alice:s3cret@yarnrc:1\n", "home/.npmrc": "proxy=http://npmrc:2\n"},
			want:   upstreamProxy{Source: "$ROOT/home/.npmrc (proxy)", URL: "http://npmrc:2"},
			wantOK: true,
		},
		{
			name:  "an unterminated quote leaves .yarnrc without settings, as in yarn",
			files: map[string]string{"home/.yarnrc": "https-proxy \"http://alice:s3cret@yarnrc:1\nproxy \"http://yarnrc:2\"\n"},
			env:   []string{"HTTPS_PROXY=http://env:3"},
		},
		{
			name:   "a .yarnrc written as YAML is read, as in yarn",
			files:  map[string]string{"home/.yarnrc": "https-proxy: http://yarnrc:1\n"},
			want:   upstreamProxy{Source: "$ROOT/home/.yarnrc (https-proxy)", URL: "http://yarnrc:1"},
			wantOK: true,
		},
		{
			name:    "a .yarnrc that is neither lockfile syntax nor YAML fails as in yarn",
			files:   map[string]string{"home/.yarnrc": "[ \"http://alice:s3cret@yarnrc:1\"\n"},
			wantErr: ".yarnrc line 1",
		},
		{
			name:   "comments, a quoted key and shared keys in .yarnrc",
			files:  map[string]string{"home/.yarnrc": "# comment\n\"proxy\", https-proxy \"http://yarnrc:1\" # trailing\n"},
			want:   upstreamProxy{Source: "$ROOT/home/.yarnrc (https-proxy)", URL: "http://yarnrc:1"},
			wantOK: true,
		},
		{
			name:  "an indented line ends the settings of .yarnrc, as in yarn",
			files: map[string]string{"home/.yarnrc": "network-timeout 600000\n  nested \"x\"\nproxy \"http://yarnrc:1\"\n"},
			plain: true,
		},
		{
			name:    "duplicated keys in a YAML .yarnrc fail as in yarn",
			files:   map[string]string{"home/.yarnrc": "https-proxy: http://yarnrc:1\nhttps-proxy: http://yarnrc:2\n"},
			wantErr: ".yarnrc line 1",
		},
		{
			name:   "NPM_CONFIG_USERCONFIG inside the project is not read",
			files:  map[string]string{"proj/.npmrc-ci": "https-proxy=http://project:1\n", "prefix/etc/npmrc": "proxy=http://global:2\n"},
			env:    []string{"NPM_CONFIG_USERCONFIG=$ROOT/proj/.npmrc-ci", "PREFIX=$ROOT/prefix"},
			want:   upstreamProxy{Source: "$ROOT/prefix/etc/npmrc (proxy)", URL: "http://global:2"},
			wantOK: true,
		},
		{
			name:  "a HOME inside the project is not read",
			files: map[string]string{"proj/home/.yarnrc": "https-proxy \"http://project:1\"\n", "proj/home/.npmrc": "proxy=http://project:2\n"},
			env:   []string{"HOME=$ROOT/proj/home"},
		},
		{
			name:   "YARN_USERCONFIG inside the project is not read",
			files:  map[string]string{"proj/.yarnrc-ci": "https-proxy \"http://project:1\"\n", "home/.npmrc": "proxy=http://user:2\n"},
			env:    []string{"YARN_USERCONFIG=$ROOT/proj/.yarnrc-ci", "NPM_CONFIG_USERCONFIG=$ROOT/home/.npmrc"},
			want:   upstreamProxy{Source: "$ROOT/home/.npmrc (proxy)", URL: "http://user:2"},
			wantOK: true,
		},
		{
			name:   "yarn_userconfig is read in any case",
			files:  map[string]string{"other/yarnrc": "https-proxy \"http://other:1\"\n"},
			env:    []string{"yarn_userconfig=$ROOT/other/yarnrc"},
			want:   upstreamProxy{Source: "$ROOT/other/yarnrc (https-proxy)", URL: "http://other:1"},
			wantOK: true,
		},
		{
			name:   "npm_config_userconfig is read in any case",
			files:  map[string]string{"other/npmrc": "https-proxy=http://other:1\n", "home/.npmrc": "https-proxy=http://user:2\n"},
			env:    []string{"npm_config_userconfig=$ROOT/other/npmrc"},
			want:   upstreamProxy{Source: "$ROOT/other/npmrc (https-proxy)", URL: "http://other:1"},
			wantOK: true,
		},
		{
			name:  "the project .yarnrc and .npmrc are not read",
			files: map[string]string{"proj/package.json": "{}", "proj/.yarnrc": "https-proxy \"http://project:1\"\n", "proj/.npmrc": "proxy=http://project:2\n"},
			env:   []string{"HTTPS_PROXY=http://env:3"},
		},
		{
			name:   "ancestor .yarnrc and .npmrc files are not read",
			files:  map[string]string{"proj/package.json": "{}", ".yarnrc": "https-proxy \"http://ancestor:1\"\n", ".npmrc": "https-proxy=http://ancestor:2\n", "home/.npmrc": "proxy=http://user:3\n"},
			dir:    "proj/sub",
			want:   upstreamProxy{Source: "$ROOT/home/.npmrc (proxy)", URL: "http://user:3"},
			wantOK: true,
		},
		{
			name:  "a malformed project .yarnrc does not fail the run",
			files: map[string]string{"proj/.yarnrc": "https-proxy http://project:1\n", "proj/.npmrc": "https-proxy=${MISSING}\n"},
		},
		{
			name:   "plain HTTP reads proxy, not https-proxy",
			files:  map[string]string{"home/.yarnrc": "https-proxy \"http://yarnrc:1\"\nproxy \"http://yarnrc:2\"\n"},
			plain:  true,
			want:   upstreamProxy{Source: "$ROOT/home/.yarnrc (proxy)", URL: "http://yarnrc:2"},
			wantOK: true,
		},
		{
			name:  "plain HTTP ignores https-proxy in files",
			files: map[string]string{"home/.npmrc": "https-proxy=http://npmrc:1\n"},
			plain: true,
		},
		{
			name:   "plain HTTP reads proxy from the global npmrc",
			files:  map[string]string{"prefix/etc/npmrc": "proxy=http://global:1\n"},
			env:    []string{"PREFIX=$ROOT/prefix"},
			plain:  true,
			want:   upstreamProxy{Source: "$ROOT/prefix/etc/npmrc (proxy)", URL: "http://global:1"},
			wantOK: true,
		},
		{
			name:  "plain HTTP ignores the project's proxy",
			files: map[string]string{"proj/package.json": "{}", "proj/.yarnrc": "proxy \"http://project:1\"\n", "proj/.npmrc": "proxy=http://project:2\n"},
			plain: true,
		},
	})
}

func TestYarnBerryUpstreamProxyFromConfigFiles(t *testing.T) {
	t.Parallel()
	berry := `{"packageManager": "yarn@4.9.2"}`
	runConfigCases(t, Yarn(), []configCase{
		{
			name:   "~/.yarnrc.yml sets httpsProxy",
			files:  map[string]string{"proj/package.json": berry, "home/.yarnrc.yml": "httpsProxy: http://home:1\n"},
			want:   upstreamProxy{Source: "$ROOT/home/.yarnrc.yml (httpsProxy)", URL: "http://home:1"},
			wantOK: true,
		},
		{
			name:   "YARN_HTTPS_PROXY wins over ~/.yarnrc.yml and ignores NO_PROXY",
			files:  map[string]string{"proj/package.json": berry, "home/.yarnrc.yml": "httpsProxy: http://home:1\n"},
			env:    []string{"YARN_HTTPS_PROXY=http://env:2", "NO_PROXY=registry.yarnpkg.com"},
			want:   upstreamProxy{Source: "YARN_HTTPS_PROXY", URL: "http://env:2"},
			wantOK: true,
		},
		{
			name:   "an empty YARN_HTTPS_PROXY connects directly over ~/.yarnrc.yml",
			files:  map[string]string{"proj/package.json": berry, "home/.yarnrc.yml": "httpsProxy: http://home:1\n"},
			env:    []string{"YARN_HTTPS_PROXY="},
			want:   upstreamProxy{Source: "YARN_HTTPS_PROXY"},
			wantOK: true,
		},
		{
			name:   "values expand environment variables with defaults",
			files:  map[string]string{"proj/package.json": berry, "home/.yarnrc.yml": "httpsProxy: ${CORP_PROXY:-http://fallback:1}\n"},
			want:   upstreamProxy{Source: "$ROOT/home/.yarnrc.yml (httpsProxy)", URL: "http://fallback:1"},
			wantOK: true,
		},
		{
			name:    "an unset variable without a default fails as in yarn",
			files:   map[string]string{"proj/package.json": berry, "home/.yarnrc.yml": "httpsProxy: ${MISSING}\n"},
			wantErr: "a value names an unset environment variable",
		},
		{
			name:   "an empty httpsProxy connects directly",
			files:  map[string]string{"proj/package.json": berry, "home/.yarnrc.yml": "httpsProxy: \"\"\n"},
			want:   upstreamProxy{Source: "$ROOT/home/.yarnrc.yml (httpsProxy)"},
			wantOK: true,
		},
		{
			name:  "Berry ignores .npmrc and .yarnrc",
			files: map[string]string{"proj/package.json": berry, "home/.npmrc": "https-proxy=http://npmrc:1\n", "home/.yarnrc": "https-proxy \"http://yarnrc:2\"\n"},
		},
		{
			name:   "yarnPath in the project's .yarnrc.yml selects Berry, without its httpsProxy",
			files:  map[string]string{"proj/.yarnrc.yml": "yarnPath: .yarn/releases/yarn-4.9.2.cjs\nhttpsProxy: http://project:1\n", "home/.yarnrc.yml": "httpsProxy: http://home:2\n", "home/.npmrc": "https-proxy=http://npmrc:3\n"},
			want:   upstreamProxy{Source: "$ROOT/home/.yarnrc.yml (httpsProxy)", URL: "http://home:2"},
			wantOK: true,
		},
		{
			name:   "the project's .yarnrc.yml is not read",
			files:  map[string]string{"proj/package.json": berry, "proj/.yarnrc.yml": "httpsProxy: http://project:1\n", "home/.yarnrc.yml": "httpsProxy: http://home:2\n"},
			want:   upstreamProxy{Source: "$ROOT/home/.yarnrc.yml (httpsProxy)", URL: "http://home:2"},
			wantOK: true,
		},
		{
			name:  "an ancestor's .yarnrc.yml is not read",
			files: map[string]string{"proj/package.json": berry, ".yarnrc.yml": "httpsProxy: http://ancestor:1\n"},
		},
		{
			name:  "a HOME inside the project is not read",
			files: map[string]string{"proj/package.json": berry, "proj/home/.yarnrc.yml": "httpsProxy: http://project:1\n"},
			env:   []string{"HOME=$ROOT/proj/home"},
		},
		{
			name:  "YARN_RC_FILENAME does not make a project file readable",
			files: map[string]string{"proj/package.json": berry, "proj/.custom.yml": "httpsProxy: http://custom:1\n"},
			env:   []string{"YARN_RC_FILENAME=.custom.yml"},
		},
		{
			name:  "plain HTTP takes the HTTPS path",
			files: map[string]string{"proj/package.json": berry, "home/.yarnrc.yml": "httpsProxy: http://home:1\n", "home/.npmrc": "proxy=http://npmrc:2\n"},
			env:   []string{"npm_config_proxy=http://env:3"},
			plain: true,
		},
	})
}

func TestPipUpstreamProxyFromConfigFiles(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("pip's user config lives under %APPDATA% on Windows")
	}
	install := []string{"install", "six"}
	runConfigCases(t, Pip(), []configCase{
		{
			name:   "the XDG user file wins over the legacy one",
			files:  map[string]string{"home/.pip/pip.conf": "[global]\nproxy = http://legacy:1\n", "home/.config/pip/pip.conf": "[global]\nproxy = http://xdg:2\n"},
			args:   install,
			want:   upstreamProxy{Source: "$ROOT/home/.config/pip/pip.conf (global.proxy)", URL: "http://xdg:2"},
			wantOK: true,
		},
		{
			name:   "the command's section wins over [global] in any file",
			files:  map[string]string{"home/.pip/pip.conf": "[install]\nproxy = http://legacy:1\n", "home/.config/pip/pip.conf": "[global]\nproxy = http://xdg:2\n"},
			args:   install,
			want:   upstreamProxy{Source: "$ROOT/home/.pip/pip.conf (install.proxy)", URL: "http://legacy:1"},
			wantOK: true,
		},
		{
			name:   "another command's section does not apply",
			files:  map[string]string{"home/.config/pip/pip.conf": "[download]\nproxy = http://download:1\n[global]\nproxy = http://global:2\n"},
			args:   []string{"--no-cache-dir", "install", "six"},
			want:   upstreamProxy{Source: "$ROOT/home/.config/pip/pip.conf (global.proxy)", URL: "http://global:2"},
			wantOK: true,
		},
		{
			name:   "a global option's value is not the command",
			files:  map[string]string{"home/.config/pip/pip.conf": "[global]\nproxy = http://global:1\n[install]\nproxy = http://install:2\n"},
			args:   []string{"--cache-dir", "/tmp/c", "install", "six"},
			want:   upstreamProxy{Source: "$ROOT/home/.config/pip/pip.conf (install.proxy)", URL: "http://install:2"},
			wantOK: true,
		},
		{
			name:   "PIP_PROXY wins over config files",
			files:  map[string]string{"home/.config/pip/pip.conf": "[install]\nproxy = http://xdg:2\n"},
			env:    []string{"PIP_PROXY=http://env:3"},
			args:   install,
			want:   upstreamProxy{Source: "PIP_PROXY", URL: "http://env:3"},
			wantOK: true,
		},
		{
			name:   "PIP_CONFIG_FILE wins and replaces the user files",
			files:  map[string]string{"home/.config/pip/pip.conf": "[install]\nproxy = http://xdg:2\n", "custom.conf": "[global]\nproxy = http://custom:4\n"},
			env:    []string{"PIP_CONFIG_FILE=$ROOT/custom.conf"},
			args:   install,
			want:   upstreamProxy{Source: "$ROOT/custom.conf (global.proxy)", URL: "http://custom:4"},
			wantOK: true,
		},
		{
			name:  "PIP_CONFIG_FILE set to the null device disables every file",
			files: map[string]string{"home/.config/pip/pip.conf": "[global]\nproxy = http://xdg:2\n"},
			env:   []string{"PIP_CONFIG_FILE=" + os.DevNull, "HTTPS_PROXY=http://env:1"},
			args:  install,
		},
		{
			name:  "an empty proxy in a later file clears an earlier one",
			files: map[string]string{"home/.pip/pip.conf": "[global]\nproxy = http://legacy:1\n", "home/.config/pip/pip.conf": "[global]\nproxy =\n"},
			args:  install,
		},
		{
			name:  "--isolated skips the user files and PIP_PROXY",
			files: map[string]string{"home/.config/pip/pip.conf": "[global]\nproxy = http://xdg:2\n"},
			env:   []string{"PIP_PROXY=http://env:3"},
			args:  []string{"--isolated", "install", "six"},
		},
		{
			name:   "the site file of a virtualenv outside the working directory wins over user files",
			files:  map[string]string{"venv/bin/pip": "", "venv/pyvenv.cfg": "home = /usr/bin\n", "venv/pip.conf": "[global]\nproxy = http://site:5\n", "home/.config/pip/pip.conf": "[global]\nproxy = http://xdg:2\n"},
			env:    []string{"PATH=$ROOT/venv/bin"},
			args:   install,
			want:   upstreamProxy{Source: "$ROOT/venv/pip.conf (global.proxy)", URL: "http://site:5"},
			wantOK: true,
		},
		{
			name:   "the site file of a virtualenv inside the working directory is not read",
			files:  map[string]string{"proj/.venv/bin/pip": "", "proj/.venv/pyvenv.cfg": "home = /usr/bin\n", "proj/.venv/pip.conf": "[global]\nproxy = http://project:5\n", "home/.config/pip/pip.conf": "[global]\nproxy = http://xdg:2\n"},
			env:    []string{"PATH=$ROOT/proj/.venv/bin"},
			args:   install,
			want:   upstreamProxy{Source: "$ROOT/home/.config/pip/pip.conf (global.proxy)", URL: "http://xdg:2"},
			wantOK: true,
		},
		{
			name:  "PIP_CONFIG_FILE inside the project is not read",
			files: map[string]string{"proj/pip.conf": "[global]\nproxy = http://project:1\n", "home/.config/pip/pip.conf": "[global]\nproxy = http://xdg:2\n"},
			env:   []string{"PIP_CONFIG_FILE=$ROOT/proj/pip.conf"},
			args:  install,
		},
		{
			name:   "the site file of a virtualenv in the git work tree is not read from a subdirectory",
			files:  map[string]string{"proj/.git/HEAD": "", "proj/.venv/bin/pip": "", "proj/.venv/pyvenv.cfg": "home = /usr/bin\n", "proj/.venv/pip.conf": "[global]\nproxy = http://project:5\n", "home/.config/pip/pip.conf": "[global]\nproxy = http://xdg:2\n"},
			dir:    "proj/sub",
			env:    []string{"PATH=$ROOT/proj/.venv/bin"},
			args:   install,
			want:   upstreamProxy{Source: "$ROOT/home/.config/pip/pip.conf (global.proxy)", URL: "http://xdg:2"},
			wantOK: true,
		},
		{
			name:    "a duplicate key fails as in pip",
			files:   map[string]string{"home/.config/pip/pip.conf": "[global]\nproxy = http://a:1\nPROXY = http://b:2\n"},
			args:    install,
			wantErr: "pip.conf line 3",
		},
	})
	runConfigCases(t, Pipenv(), []configCase{
		{
			name:   "pipenv reads only [global]",
			files:  map[string]string{"home/.config/pip/pip.conf": "[install]\nproxy = http://install:1\n[global]\nproxy = http://global:2\n"},
			args:   install,
			want:   upstreamProxy{Source: "$ROOT/home/.config/pip/pip.conf (global.proxy)", URL: "http://global:2"},
			wantOK: true,
		},
	})
}

func TestPipIgnoresVirtualenvInsideTheProjectThroughSymlink(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("pip's user config lives under %APPDATA% on Windows")
	}
	root := t.TempDir()
	writeConfigFiles(t, root, map[string]string{
		"proj/.venv/bin/pip":    "",
		"proj/.venv/pyvenv.cfg": "home = /usr/bin\n",
		"proj/.venv/pip.conf":   "[global]\nproxy = http://project:1\n",
	}, func(s string) string { return s })
	link := filepath.Join(root, "venv-link")
	require.NoError(t, os.Symlink(filepath.Join(root, "proj", ".venv"), link))
	resolver, ok := Pip().(upstreamProxyResolver)
	require.True(t, ok)

	got, gotOK, err := resolver.resolveUpstreamProxy([]string{"PATH=" + filepath.Join(link, "bin")}, filepath.Join(root, "proj"), []string{"install"})
	require.NoError(t, err)
	assert.False(t, gotOK, "the virtualenv is inside the project, whatever path reaches it")
	assert.Equal(t, upstreamProxy{}, got)
}

func TestConfigFilesSkipFilesInsideTheProject(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeConfigFiles(t, root, map[string]string{
		"proj/.git/HEAD":  "",
		"proj/sub/x":      "",
		"proj/ci/npmrc":   "",
		"outside/npmrc":   "",
		"outside/sub/rc":  "",
		"plain/sub/x":     "",
		"plain/ci/npmrc":  "",
		"plain/sub/npmrc": "",
	}, func(s string) string { return s })
	require.NoError(t, os.Symlink(filepath.Join(root, "proj", "ci", "npmrc"), filepath.Join(root, "outside", "link")))
	require.NoError(t, os.Symlink(filepath.Join(root, "proj"), filepath.Join(root, "outside", "dirlink")))

	inRepo := newConfigFiles(filepath.Join(root, "proj", "sub"))
	assert.True(t, inRepo.inProject(filepath.Join(root, "proj", "ci", "npmrc")), "the git work tree is the project")
	assert.True(t, inRepo.inProject(filepath.Join(root, "outside", "link")), "a symlink into the project")
	assert.True(t, inRepo.inProject(filepath.Join(root, "outside", "dirlink", "ci", "npmrc")), "a path through a symlinked directory")
	assert.False(t, inRepo.inProject(filepath.Join(root, "outside", "npmrc")))
	assert.False(t, inRepo.inProject(filepath.Join(root, "outside", "sub", "rc")))

	noRepo := newConfigFiles(filepath.Join(root, "plain", "sub"))
	assert.True(t, noRepo.inProject(filepath.Join(root, "plain", "sub", "npmrc")), "outside a git work tree the working directory is the project")
	assert.False(t, noRepo.inProject(filepath.Join(root, "plain", "ci", "npmrc")))
}

func TestConfigFilesFailClosed(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeConfigFiles(t, root, map[string]string{"home/npmrc": "https-proxy=http://user:1\n"}, func(s string) string { return s })
	file := filepath.Join(root, "home", "npmrc")

	unknown := newConfigFiles(filepath.Join(root, "missing"))
	assert.True(t, unknown.inProject(file), "with no project root, every file may be in the project")
	_, ok, err := unknown.read(file)
	require.NoError(t, err)
	assert.False(t, ok)

	require.NoError(t, os.MkdirAll(filepath.Join(root, "proj"), 0o755))
	files := newConfigFiles(filepath.Join(root, "proj"))
	require.NoError(t, os.Symlink(filepath.Join(root, "nowhere"), filepath.Join(root, "home", "dangling")))
	assert.True(t, files.inProject(filepath.Join(root, "home", "dangling")), "a path that cannot be resolved may be in the project")
	assert.False(t, files.inProject(file))
}

func TestConfigFilesMatchTheProjectInAnyCase(t *testing.T) {
	t.Parallel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	writeConfigFiles(t, root, map[string]string{"proj/npmrc": ""}, func(s string) string { return s })
	if _, err := os.Stat(filepath.Join(root, "PROJ")); err != nil {
		t.Skip("the file system is case-sensitive")
	}

	assert.True(t, newConfigFiles(filepath.Join(root, "proj")).inProject(filepath.Join(root, "PROJ", "npmrc")))
	assert.True(t, newConfigFiles(filepath.Join(root, "PROJ")).inProject(filepath.Join(root, "proj", "npmrc")))
}

func TestConfigPathsThatResolveInsideTheProjectAreNotRead(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	writeConfigFiles(t, root, map[string]string{
		"proj/.git/HEAD":          "",
		"proj/sub/pfx/etc/npmrc":  "https-proxy=http://project:1\n",
		"proj/sub/home/.npmrc":    "https-proxy=http://project:1\n",
		"proj/sub/.yarnrc":        "https-proxy \"http://project:1\"\n",
		"proj/sub/.npmrc-ci":      "https-proxy=http://project:1\n",
		"proj/sub/xdg/git/config": "[http]\n\tproxy = http://project:1\n",
		"proj/sub/.cargo/config":  "[http]\nproxy = \"http://project:1\"\n",
		"proj/x/etc/npmrc":        "https-proxy=http://project:1\n",
		"proj/d/e/.keep":          "",
		"proj/decoy.yarnrc":       "https-proxy \"http://project:1\"\n",
		"decoy.yarnrc":            "https-proxy \"http://outside:2\"\n",
		"outside/x/etc/npmrc":     "https-proxy=http://outside:2\n",
		"global/etc/npmrc":        "https-proxy=http://global:2\n",
	}, func(s string) string { return s })
	require.NoError(t, os.Symlink(filepath.Join("d", "e"), filepath.Join(root, "proj", "l")))
	require.NoError(t, os.Symlink(filepath.Join(root, "proj", "sub"), filepath.Join(root, "outside", "link")))
	home := "HOME=" + filepath.Join(root, "home")
	// Not filepath.Join, which would drop the ".." that the OS applies after the symlink.
	throughLink := filepath.Join(root, "proj", "l") + filepath.FromSlash("/../../decoy.yarnrc")

	tests := []struct {
		name    string
		manager PackageManager
		cwd     string
		env     []string
		want    upstreamProxy
		wantOK  bool
	}{
		{name: "npm PREFIX", manager: NPM(), env: []string{home, "PREFIX=pfx"}},
		{name: "npm HOME", manager: NPM(), env: []string{"HOME=home"}},
		{name: "npm NPM_CONFIG_USERCONFIG", manager: NPM(), env: []string{home, "NPM_CONFIG_USERCONFIG=.npmrc-ci"}},
		{name: "Yarn YARN_USERCONFIG", manager: Yarn(), env: []string{home, "YARN_USERCONFIG=.yarnrc"}},
		{name: "Yarn NPM_CONFIG_USERCONFIG", manager: Yarn(), env: []string{home, "NPM_CONFIG_USERCONFIG=.npmrc-ci"}},
		{name: "cargo XDG_CONFIG_HOME", manager: Cargo(), env: []string{home, "XDG_CONFIG_HOME=xdg"}},
		{name: "cargo CARGO_HOME", manager: Cargo(), env: []string{home, "CARGO_HOME=.cargo"}},
		{name: "Yarn YARN_USERCONFIG with .. after a symlink", manager: Yarn(), env: []string{home, "YARN_USERCONFIG=" + throughLink}},
		{name: "npm PREFIX with .. from a symlinked working directory", manager: NPM(), cwd: "outside/link", env: []string{home, "PREFIX=../x"}},
		{
			name:    "npm PREFIX outside the project",
			manager: NPM(),
			env:     []string{home, "PREFIX=../../global"},
			want:    upstreamProxy{Source: filepath.FromSlash("../../global/etc/npmrc") + " (https-proxy)", URL: "http://global:2"},
			wantOK:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(root, "proj", "sub")
			if tt.cwd != "" {
				dir = filepath.Join(root, filepath.FromSlash(tt.cwd))
			}
			t.Chdir(dir)
			resolver, ok := tt.manager.(upstreamProxyResolver)
			require.True(t, ok)

			got, gotOK, err := resolver.resolveUpstreamProxy(tt.env, dir, []string{"install"})
			require.NoError(t, err)
			assert.Equal(t, tt.wantOK, gotOK)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPipUserConfigOnMacOS(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	runConfigCases(t, Pip(), []configCase{{
		name: "an Application Support pip directory replaces ~/.config",
		files: map[string]string{
			"home/Library/Application Support/pip/pip.conf": "[global]\nproxy = http://appsupport:1\n",
			"home/.config/pip/pip.conf":                     "[global]\nproxy = http://xdg:2\n",
		},
		args:   []string{"install"},
		want:   upstreamProxy{Source: "$ROOT/home/Library/Application Support/pip/pip.conf (global.proxy)", URL: "http://appsupport:1"},
		wantOK: true,
	}})
}

func TestCargoUpstreamProxyFromConfigFiles(t *testing.T) {
	t.Parallel()
	runConfigCases(t, Cargo(), []configCase{
		{
			name:   "a git config comment ending in a backslash doesn't continue",
			files:  map[string]string{"home/.gitconfig": "[http]\n# note \\\n\tsslVerify = true ; c \\\n\tproxy = http://git:2\n"},
			want:   upstreamProxy{Source: "$ROOT/home/.gitconfig (http.proxy)", URL: "http://git:2"},
			wantOK: true,
		},
		{
			name:   "CARGO_HOME's config sets http.proxy",
			files:  map[string]string{"cargo/config.toml": "[http]\nproxy = \"http://home:2\"\n"},
			env:    []string{"CARGO_HOME=$ROOT/cargo"},
			want:   upstreamProxy{Source: "$ROOT/cargo/config.toml (http.proxy)", URL: "http://home:2"},
			wantOK: true,
		},
		{
			name:   "CARGO_HOME defaults to ~/.cargo and keeps NO_PROXY",
			files:  map[string]string{"home/.cargo/config.toml": "[http]\nproxy = \"home:2\"\n"},
			env:    []string{"NO_PROXY=crates.io"},
			want:   upstreamProxy{Source: "$ROOT/home/.cargo/config.toml (http.proxy)", URL: "home:2", NoProxy: "crates.io"},
			wantOK: true,
		},
		{
			name:   "config without an extension wins over config.toml",
			files:  map[string]string{"home/.cargo/config": "[http]\nproxy = \"http://legacy:1\"\n", "home/.cargo/config.toml": "[http]\nproxy = \"http://toml:2\"\n"},
			want:   upstreamProxy{Source: "$ROOT/home/.cargo/config (http.proxy)", URL: "http://legacy:1"},
			wantOK: true,
		},
		{
			name:   "CARGO_HTTP_PROXY wins over files",
			files:  map[string]string{"home/.cargo/config.toml": "[http]\nproxy = \"http://home:1\"\n"},
			env:    []string{"CARGO_HTTP_PROXY=http://env:3"},
			want:   upstreamProxy{Source: "CARGO_HTTP_PROXY", URL: "http://env:3"},
			wantOK: true,
		},
		{
			name:   "an empty CARGO_HTTP_PROXY connects directly",
			files:  map[string]string{"home/.cargo/config.toml": "[http]\nproxy = \"http://home:1\"\n"},
			env:    []string{"CARGO_HTTP_PROXY=", "HTTPS_PROXY=http://env:4"},
			want:   upstreamProxy{Source: "CARGO_HTTP_PROXY"},
			wantOK: true,
		},
		{
			name:   "an empty http.proxy connects directly",
			files:  map[string]string{"home/.cargo/config.toml": "[http]\nproxy = \"\"\n", "home/.gitconfig": "[http]\n\tproxy = http://git:2\n"},
			env:    []string{"HTTPS_PROXY=http://env:4"},
			want:   upstreamProxy{Source: "$ROOT/home/.cargo/config.toml (http.proxy)"},
			wantOK: true,
		},
		{
			name:   "git's http.proxy applies when cargo has none",
			files:  map[string]string{"home/.gitconfig": "[user]\n\tname = x\n[http \"https://example.com\"]\n\tproxy = http://scoped:1\n[HTTP]\n\tProxy = \"http://git:2\" ; comment\n", "home/.config/git/config": "[http]\n\tproxy = http://xdg:3\n"},
			env:    []string{"HTTPS_PROXY=http://env:4"},
			want:   upstreamProxy{Source: "$ROOT/home/.gitconfig (http.proxy)", URL: "http://git:2"},
			wantOK: true,
		},
		{
			name:   "XDG git config applies without ~/.gitconfig",
			files:  map[string]string{"home/.config/git/config": "[http]\n\tproxy = http://xdg:\\\n3\n"},
			want:   upstreamProxy{Source: "$ROOT/home/.config/git/config (http.proxy)", URL: "http://xdg:3"},
			wantOK: true,
		},
		{
			name:   "cargo config wins over git config",
			files:  map[string]string{"home/.cargo/config.toml": "[http]\nproxy = \"http://cargo:1\"\n", "home/.gitconfig": "[http]\n\tproxy = http://git:2\n"},
			want:   upstreamProxy{Source: "$ROOT/home/.cargo/config.toml (http.proxy)", URL: "http://cargo:1"},
			wantOK: true,
		},
		{
			name:    "malformed TOML fails as in cargo",
			files:   map[string]string{"home/.cargo/config.toml": "[http\nproxy = 1\n"},
			wantErr: "failed to parse",
		},
		{
			name:   "the working directory's .cargo/config.toml is not read",
			files:  map[string]string{"proj/.cargo/config.toml": "[http]\nproxy = \"http://project:1\"\n", "home/.cargo/config.toml": "[http]\nproxy = \"http://home:2\"\n"},
			want:   upstreamProxy{Source: "$ROOT/home/.cargo/config.toml (http.proxy)", URL: "http://home:2"},
			wantOK: true,
		},
		{
			name:   "an ancestor's .cargo/config is not read",
			files:  map[string]string{".cargo/config": "[http]\nproxy = \"http://ancestor:1\"\n", "home/.gitconfig": "[http]\n\tproxy = http://git:2\n"},
			dir:    "proj/sub",
			want:   upstreamProxy{Source: "$ROOT/home/.gitconfig (http.proxy)", URL: "http://git:2"},
			wantOK: true,
		},
		{
			name:  "a malformed project .cargo/config.toml does not fail the run",
			files: map[string]string{"proj/.cargo/config.toml": "[http\nproxy = 1\n"},
		},
		{
			name:   "a CARGO_HOME inside the project is not read",
			files:  map[string]string{"proj/.cargo/config.toml": "[http]\nproxy = \"http://project:1\"\n", "home/.gitconfig": "[http]\n\tproxy = http://git:2\n"},
			env:    []string{"CARGO_HOME=$ROOT/proj/.cargo"},
			want:   upstreamProxy{Source: "$ROOT/home/.gitconfig (http.proxy)", URL: "http://git:2"},
			wantOK: true,
		},
		{
			name:  "an XDG git config inside the project is not read",
			files: map[string]string{"proj/xdg/git/config": "[http]\n\tproxy = http://project:1\n"},
			env:   []string{"XDG_CONFIG_HOME=$ROOT/proj/xdg"},
		},
	})
}

func TestUpstreamProxyFuncConnectsDirectlyForEmptyURL(t *testing.T) {
	t.Parallel()
	pick, err := upstreamProxy{Source: "CARGO_HTTP_PROXY"}.proxyFunc()
	require.NoError(t, err)
	got, err := pick(nil)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestRunRejectsMalformedManagerConfig(t *testing.T) {
	t.Setenv("GITLAB_CI", "") // honor fake mode even when the suite runs in CI
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
	home := t.TempDir()
	isolateUserConfig(t, home)
	clearProxyEnv(t)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".cargo"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".cargo", "config.toml"), []byte("[http\n"), 0o600))
	ios, _, _, _ := cmdtest.TestIOStreams()
	exec := &fakeExecutor{}

	err := Run(t.Context(), Cargo(), RunOptions{
		IO:        ios,
		Executor:  exec,
		BaseDir:   t.TempDir(),
		ProjectID: "group/proj",
		Args:      []string{"fetch"},
	})
	require.ErrorContains(t, err, "failed to read the cargo proxy settings")
	assert.Nil(t, exec.env, "the package manager must not run")
}

func TestConfigErrorsNameTheFileWithoutQuotingIt(t *testing.T) {
	t.Parallel()
	berry := `{"packageManager": "yarn@4.9.2"}`
	tests := []struct {
		name    string
		manager PackageManager
		files   map[string]string
		// want is the whole error, with $ROOT for the temp root.
		want string
	}{
		{name: "malformed .yarnrc", manager: Yarn(), files: map[string]string{"home/.yarnrc": "[ \"http://alice:s3cret@yarnrc:1\"\n"}, want: "failed to parse $ROOT/home/.yarnrc line 1"},
		{name: "unset variable in .npmrc for yarn", manager: Yarn(), files: map[string]string{"home/.npmrc": "//registry.example/:_authToken=${S3CRET_TOKEN}\n"}, want: "failed to read $ROOT/home/.npmrc: a value names an unset environment variable"},
		{name: "wrong type in ~/.yarnrc.yml", manager: Yarn(), files: map[string]string{"proj/package.json": berry, "home/.yarnrc.yml": "httpsProxy: [\"http://alice:s3cret@yarnrc:1\"]\n"}, want: "failed to parse $ROOT/home/.yarnrc.yml line 1"},
		{name: "unset variable in ~/.yarnrc.yml", manager: Yarn(), files: map[string]string{"proj/package.json": berry, "home/.yarnrc.yml": "httpsProxy: ${S3CRET_VAR}\n"}, want: "httpsProxy in $ROOT/home/.yarnrc.yml: a value names an unset environment variable"},
		{name: "malformed package.json", manager: Yarn(), files: map[string]string{"proj/package.json": "{\n\"packageManager\": s3cret}\n"}, want: "failed to parse $ROOT/proj/package.json line 2"},
		{name: "duplicate pip section", manager: Pip(), files: map[string]string{"home/.config/pip/pip.conf": "[s3cret]\nproxy = http://a:1\n[s3cret]\n"}, want: "failed to parse $ROOT/home/.config/pip/pip.conf line 3"},
		{name: "malformed cargo config", manager: Cargo(), files: map[string]string{"home/.cargo/config.toml": "[http]\nproxy = 'http://alice:s3cret@cargo:1' s3cret\n"}, want: "failed to parse $ROOT/home/.cargo/config.toml line 2"},
		{name: "bad escape in git config", manager: Cargo(), files: map[string]string{"home/.gitconfig": "[http]\n\tproxy = \"http://alice:s3cret@git:1\\q\"\n"}, want: "failed to parse $ROOT/home/.gitconfig line 2"},
		{name: "bad escape in git config after a continued line", manager: Cargo(), files: map[string]string{"home/.gitconfig": "[http]\n\tcookieFile = a\\\n b\n\tproxy = \"http://alice:s3cret@git:1\\q\"\n"}, want: "failed to parse $ROOT/home/.gitconfig line 4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.manager.Name() == "pip" && runtime.GOOS == "windows" {
				t.Skip("pip's user config lives under %APPDATA% on Windows")
			}
			root, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			writeConfigFiles(t, root, tt.files, func(s string) string { return s })
			dir := filepath.Join(root, "proj")
			require.NoError(t, os.MkdirAll(dir, 0o755))
			resolver, ok := tt.manager.(upstreamProxyResolver)
			require.True(t, ok)

			_, _, err = resolver.resolveUpstreamProxy([]string{"HOME=" + filepath.Join(root, "home")}, dir, []string{"install"})
			require.EqualError(t, err, strings.ReplaceAll(tt.want, "$ROOT", root))
		})
	}
}

func TestInvalidProxyFromAFileIsNotQuoted(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeConfigFiles(t, root, map[string]string{"home/.npmrc": "https-proxy=s3cret://alice:pw@corp:1\n"}, func(s string) string { return s })
	require.NoError(t, os.MkdirAll(filepath.Join(root, "proj"), 0o755))

	_, err := upstreamProxyOptions(NPM(), []string{"HOME=" + filepath.Join(root, "home")}, filepath.Join(root, "proj"), nil)
	require.ErrorContains(t, err, "invalid outbound proxy in "+filepath.Join(root, "home", ".npmrc")+" (https-proxy)")
	assert.NotContains(t, err.Error(), "s3cret")
	assert.NotContains(t, err.Error(), "alice")
}
