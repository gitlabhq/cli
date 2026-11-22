//go:build !integration

package pm

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// TestManagerEnvironAndCATrust pins the exact proxy-routing and CA-trust env
// vars each manager emits. A typo in any of these strings would silently fail
// to route traffic through the firewall proxy (fail-open) — the same class of
// bug as the Yarn Berry issue — so assert the concrete values, not just types.
func TestManagerEnvironAndCATrust(t *testing.T) {
	t.Parallel()
	const proxyURL = "http://127.0.0.1:9999"
	const caPath = "/tmp/ca.pem"

	t.Run("yarn", func(t *testing.T) {
		t.Parallel()
		// Yarn Berry (v2+) honors YARN_HTTP(S)_PROXY; Yarn Classic (v1) reads
		// .npmrc and the npm_config_* env vars, so both families are pinned,
		// with npm_config_noproxy neutralized to a non-empty sentinel.
		env := Yarn().Environment(proxyURL)
		assert.Contains(t, env, "YARN_HTTPS_PROXY="+proxyURL)
		assert.Contains(t, env, "YARN_HTTP_PROXY="+proxyURL)
		assert.Contains(t, env, "npm_config_proxy="+proxyURL)
		assert.Contains(t, env, "npm_config_https_proxy="+proxyURL)
		assert.Contains(t, env, "npm_config_noproxy=localhost")
		assert.NotContains(t, env, "npm_config_noproxy=", "empty noproxy is overridden by .npmrc; must be a non-empty sentinel")
		assert.Equal(t, []string{
			"NODE_EXTRA_CA_CERTS=" + caPath,
			"npm_config_cafile=" + caPath,
			"NPM_CONFIG_CAFILE=" + caPath,
		}, Yarn().CATrustEnviron(caPath))
	})

	// pip/pipenv/poetry pin PIP_PROXY so pip's trust_env=False path (triggered
	// by its own PIP_PROXY/pip.conf proxy) can't route around the MITM;
	// uv/twine/gem route through the universal HTTP(S)_PROXY the engine sets in
	// proxyEnviron, so their Environment adds nothing.
	t.Run("pip", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, []string{"PIP_PROXY=" + proxyURL}, Pip().Environment(proxyURL))
		assert.Equal(t, []string{"PIP_CERT=" + caPath, "REQUESTS_CA_BUNDLE=" + caPath}, Pip().CATrustEnviron(caPath))
	})

	t.Run("pipenv", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, []string{"PIP_PROXY=" + proxyURL}, Pipenv().Environment(proxyURL))
		assert.Equal(t, []string{"PIP_CERT=" + caPath, "REQUESTS_CA_BUNDLE=" + caPath}, Pipenv().CATrustEnviron(caPath))
	})

	t.Run("uv", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, Uv().Environment(proxyURL))
		assert.Equal(t, []string{"SSL_CERT_FILE=" + caPath}, Uv().CATrustEnviron(caPath))
	})

	t.Run("poetry", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, []string{"PIP_PROXY=" + proxyURL}, Poetry().Environment(proxyURL))
		assert.Equal(t, []string{"PIP_CERT=" + caPath, "REQUESTS_CA_BUNDLE=" + caPath}, Poetry().CATrustEnviron(caPath))
	})

	t.Run("twine", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, Twine().Environment(proxyURL))
		assert.Equal(t, []string{"TWINE_CERT=" + caPath}, Twine().CATrustEnviron(caPath))
	})

	t.Run("gem", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, Gem().Environment(proxyURL))
		assert.Equal(t, []string{"SSL_CERT_FILE=" + caPath}, Gem().CATrustEnviron(caPath))
	})

	t.Run("bundle", func(t *testing.T) {
		t.Parallel()
		// Only the lowercase names Ruby's Net::HTTP reads are added; the
		// uppercase names are already set by the engine and not duplicated.
		env := Bundle().Environment(proxyURL)
		assert.Contains(t, env, "https_proxy="+proxyURL)
		assert.Contains(t, env, "http_proxy="+proxyURL)
		assert.NotContains(t, env, "HTTPS_PROXY="+proxyURL, "uppercase is the engine's job; must not be duplicated here")
		assert.Equal(t, []string{"SSL_CERT_FILE=" + caPath, "BUNDLE_SSL_CA_CERT=" + caPath}, Bundle().CATrustEnviron(caPath))
	})

	t.Run("cargo", func(t *testing.T) {
		t.Parallel()
		// cargo pins CARGO_HTTP_PROXY: an http.proxy in cargo or git config
		// outranks the engine's HTTPS_PROXY, and curl ignores the uppercase
		// HTTP_PROXY for plaintext registries.
		assert.Equal(t, []string{"CARGO_HTTP_PROXY=" + proxyURL}, Cargo().Environment(proxyURL))
		assert.Equal(t, []string{"CARGO_HTTP_CAINFO=" + caPath}, Cargo().CATrustEnviron(caPath))
	})

	t.Run("golang", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, []string{"SSL_CERT_FILE=" + caPath}, Golang().CATrustEnviron(caPath))
	})
}

// TestManagerExistingBundleVars pins each manager's declared CA-preservation
// variables. Every variable a manager's CATrustEnviron overwrites must be
// listed here, or a user who set only the undeclared one silently loses their
// trust anchors — a "wrong string, silent breakage" bug the engine can't catch.
func TestManagerExistingBundleVars(t *testing.T) {
	t.Parallel()
	cases := []struct {
		m               PackageManager
		want            []string
		notSetByCATrust []string
	}{
		{NPM(), []string{"NODE_EXTRA_CA_CERTS", "npm_config_cafile", "NPM_CONFIG_CAFILE"}, nil},
		{Pnpm(), []string{"NODE_EXTRA_CA_CERTS", "npm_config_cafile", "NPM_CONFIG_CAFILE"}, nil},
		{
			Yarn(),
			[]string{"NODE_EXTRA_CA_CERTS", "npm_config_cafile", "NPM_CONFIG_CAFILE", "YARN_CAFILE", "YARN_HTTPS_CA_FILE_PATH", "YARN_CA_FILE_PATH"},
			[]string{"YARN_CAFILE", "YARN_HTTPS_CA_FILE_PATH", "YARN_CA_FILE_PATH"},
		},
		{Pip(), []string{"PIP_CERT", "REQUESTS_CA_BUNDLE"}, nil},
		{Pipenv(), []string{"PIP_CERT", "REQUESTS_CA_BUNDLE"}, nil},
		{Uv(), []string{"SSL_CERT_FILE"}, nil},
		{Poetry(), []string{"PIP_CERT", "REQUESTS_CA_BUNDLE"}, nil},
		{Twine(), []string{"TWINE_CERT", "REQUESTS_CA_BUNDLE"}, []string{"REQUESTS_CA_BUNDLE"}},
		{Gem(), []string{"SSL_CERT_FILE"}, nil},
		{Bundle(), []string{"SSL_CERT_FILE", "BUNDLE_SSL_CA_CERT"}, nil},
		// Maven and Gradle preserve no CA bundle var: they trust the proxy
		// through a generated JVM truststore, not an env-named CA file.
		{Maven(), nil, nil},
		{Gradle(), nil, nil},
		{Cargo(), []string{"CARGO_HTTP_CAINFO"}, nil},
		{Golang(), []string{"SSL_CERT_FILE"}, nil},
	}
	for _, c := range cases {
		t.Run(c.m.Name(), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, c.m.ExistingBundleVars())
			// Every declared var must be one CATrustEnviron actually sets, so
			// the declaration and the overwrite can't drift apart.
			set := c.m.CATrustEnviron("/tmp/ca.pem")
			declared := make(map[string]struct{}, len(c.m.ExistingBundleVars()))
			for _, v := range c.m.ExistingBundleVars() {
				declared[v] = struct{}{}
				if slices.Contains(c.notSetByCATrust, v) {
					continue
				}
				found := false
				for _, e := range set {
					if k, _, _ := strings.Cut(e, "="); k == v {
						found = true
						break
					}
				}
				assert.Truef(t, found, "%s declares %q but CATrustEnviron does not set it", c.m.Name(), v)
			}
			// And the reverse (pm.go: "Every variable CATrustEnviron sets must
			// appear here"): a new CA-bundle var added to CATrustEnviron but not
			// declared here would silently clobber a user's anchors on that var
			// and still pass the forward check above.
			for _, e := range set {
				k, _, _ := strings.Cut(e, "=")
				_, ok := declared[k]
				assert.Truef(t, ok, "%s CATrustEnviron sets %q but ExistingBundleVars does not declare it", c.m.Name(), k)
			}
			// Registry connections trust these files whichever manager runs.
			for _, v := range c.m.ExistingBundleVars() {
				assert.Containsf(t, upstreamCAVars, v, "%s declares %q but upstreamCAVars does not list it", c.m.Name(), v)
			}
		})
	}
}

func TestRunMergesUserCAIntoOverriddenVar(t *testing.T) {
	cases := []struct {
		m   PackageManager
		key string
	}{
		{Poetry(), "PIP_CERT"},
		{NPM(), "npm_config_cafile"},
		{NPM(), "NPM_CONFIG_CAFILE"},
		{Pnpm(), "npm_config_cafile"},
		{Yarn(), "npm_config_cafile"},
		{Yarn(), "YARN_CAFILE"},
	}
	for _, c := range cases {
		t.Run(c.m.Name()+"/"+c.key, func(t *testing.T) {
			t.Setenv("GITLAB_CI", "")
			t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
			clearCAVars(t)
			userPEM := newTestAuthority(t).pem()
			userPath := filepath.Join(t.TempDir(), "corp-ca.pem")
			require.NoError(t, os.WriteFile(userPath, userPEM, 0o600))
			t.Setenv(c.key, userPath)
			ios, _, _, _ := cmdtest.TestIOStreams()
			exec := &fakeExecutor{}
			var bundle []byte
			exec.onExec = func() { bundle, _ = os.ReadFile(lookupEnv(exec.env, c.key)) }

			require.NoError(t, Run(t.Context(), c.m, RunOptions{
				IO: ios, Executor: exec, BaseDir: t.TempDir(), ProjectID: "group/proj", Args: []string{"install"},
			}))

			assert.NotEqual(t, userPath, lookupEnv(exec.env, c.key))
			assert.True(t, bytes.HasPrefix(bundle, userPEM), "the child must still trust the user's CA")
			assert.Equal(t, 2+len(systemRoots()), countPEMBlocks(bundle), "the bundle must hold the user's CA, the proxy CA and the system roots")
		})
	}
}

func TestRunTrustsNPMAndYarnCAVarsInAnyCase(t *testing.T) {
	cases := []struct {
		m        PackageManager
		key      string
		childVar string
	}{
		{Yarn(), "yarn_cafile", "YARN_CAFILE"},
		{Yarn(), "Yarn_Cafile", "YARN_CAFILE"},
		{Yarn(), "YARN_HTTPS_CA_FILE_PATH", ""},
		{Yarn(), "yarn_https_ca_file_path", ""},
		{Yarn(), "Yarn_Ca_File_Path", ""},
		{Yarn(), "Npm_Config_Cafile", "npm_config_cafile"},
		{NPM(), "Npm_Config_Cafile", "npm_config_cafile"},
	}
	for _, c := range cases {
		t.Run(c.m.Name()+"/"+c.key, func(t *testing.T) {
			clearCAVars(t)
			user := newTestAuthority(t)
			userPath := filepath.Join(t.TempDir(), "corp-ca.pem")
			require.NoError(t, os.WriteFile(userPath, user.pem(), 0o600))
			t.Setenv(c.key, userPath)

			env, bundle := runChild(t, c.m)
			roots, _, err := upstreamRootCAs(os.Environ(), "", nil)

			assert.True(t, bytes.HasPrefix(bundle, user.pem()), "the child must still trust the user's CA")
			assert.Equal(t, 2+len(systemRoots()), countPEMBlocks(bundle), "the bundle must hold the user's CA, the proxy CA and the system roots")
			if c.childVar != "" {
				assert.NotContains(t, valuesAnyCase(env, c.childVar), userPath, "an inherited spelling must not outrank glab's bundle")
				assert.Contains(t, valuesAnyCase(env, c.childVar), lookupEnv(env, "NODE_EXTRA_CA_CERTS"))
			}
			require.NoError(t, err)
			require.NotNil(t, roots, "registry connections must trust the user's CA")
			assertVerifies(t, roots, user.leaf(t, "registry.test"))
		})
	}
}

func clearCAVars(t *testing.T) {
	t.Helper()
	for _, v := range upstreamCAVars {
		t.Setenv(v, "")
	}
}

func runChild(t *testing.T, m PackageManager) ([]string, []byte) {
	t.Helper()
	t.Setenv("GITLAB_CI", "")
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
	ios, _, _, _ := cmdtest.TestIOStreams()
	exec := &fakeExecutor{}
	var bundle []byte
	exec.onExec = func() { bundle, _ = os.ReadFile(lookupEnv(exec.env, "NODE_EXTRA_CA_CERTS")) }
	require.NoError(t, Run(t.Context(), m, RunOptions{
		IO: ios, Executor: exec, BaseDir: t.TempDir(), ProjectID: "group/proj", Args: []string{"install"},
	}))
	return exec.env, bundle
}

func valuesAnyCase(env []string, key string) []string {
	var values []string
	for _, e := range env {
		if k, v, ok := strings.Cut(e, "="); ok && strings.EqualFold(k, key) {
			values = append(values, v)
		}
	}
	return values
}

func TestRunPointsNPMCafileAtBundle(t *testing.T) {
	for _, m := range []PackageManager{NPM(), Pnpm(), Yarn()} {
		t.Run(m.Name(), func(t *testing.T) {
			env, _ := runChild(t, m)
			bundle := lookupEnv(env, "NODE_EXTRA_CA_CERTS")
			require.NotEmpty(t, bundle)
			assert.Equal(t, []string{bundle, bundle}, valuesAnyCase(env, "npm_config_cafile"))
		})
	}
}

func TestRunDropsInheritedNPMInlineCA(t *testing.T) {
	for _, m := range []PackageManager{NPM(), Pnpm()} {
		t.Run(m.Name(), func(t *testing.T) {
			t.Setenv("npm_config_ca", string(newTestAuthority(t).pem()))
			env, _ := runChild(t, m)
			for _, v := range valuesAnyCase(env, "npm_config_ca") {
				assert.Empty(t, v, "an empty value is unset to npm")
			}
		})
	}
}

func TestRunReplacesInheritedYarnCafileInAnyCase(t *testing.T) {
	userPath := filepath.Join(t.TempDir(), "corp-ca.pem")
	require.NoError(t, os.WriteFile(userPath, newTestAuthority(t).pem(), 0o600))
	t.Setenv("yarn_cafile", userPath)

	env, _ := runChild(t, Yarn())

	assert.Equal(t, []string{lookupEnv(env, "NODE_EXTRA_CA_CERTS")}, valuesAnyCase(env, "YARN_CAFILE"))
}

func TestRunMergesYarnBerryCAFileIntoBundle(t *testing.T) {
	for _, key := range []string{"YARN_HTTPS_CA_FILE_PATH", "YARN_CA_FILE_PATH"} {
		t.Run(key, func(t *testing.T) {
			clearCAVars(t)
			userPEM := newTestAuthority(t).pem()
			userPath := filepath.Join(t.TempDir(), "corp-ca.pem")
			require.NoError(t, os.WriteFile(userPath, userPEM, 0o600))
			t.Setenv(key, userPath)

			_, bundle := runChild(t, Yarn())

			assert.True(t, bytes.HasPrefix(bundle, userPEM), "the plugin points %s at this bundle, so it must keep the user's CA", key)
		})
	}
}

func TestYarnSetupSetsYarnCafileOnlyWhenInherited(t *testing.T) {
	t.Parallel()
	const caPath = "/tmp/glab-ca.pem"
	cases := []struct {
		name   string
		parent []string
		want   []string
	}{
		{"uppercase", []string{"YARN_CAFILE=/corp.pem"}, []string{caPath}},
		{"lowercase", []string{"yarn_cafile=/corp.pem"}, []string{caPath}},
		{"unset", []string{"PATH=/usr/bin"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			res, err := yarnManager{}.Setup(t.Context(), setupInput{parent: c.parent, caPath: caPath})
			require.NoError(t, err)
			t.Cleanup(res.stop)
			assert.Equal(t, c.want, valuesAnyCase(res.env, "YARN_CAFILE"))
		})
	}
}

// TestCleanupCAFilesNoOpForEnvProxyManagers asserts the env-proxy managers'
// CleanupCAFiles does not touch the CA bundle path (only the engine removes it)
// and does not panic.
func TestCleanupCAFilesNoOpForEnvProxyManagers(t *testing.T) {
	t.Parallel()
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caPath, []byte("x"), 0o600))
	for _, m := range []PackageManager{NPM(), Pnpm(), Yarn(), Pip(), Pipenv(), Uv(), Poetry(), Twine(), Gem(), Bundle(), Cargo(), Golang()} {
		m.CleanupCAFiles(caPath)
	}
	_, err := os.Stat(caPath)
	assert.NoError(t, err, "env-proxy managers must not remove the engine-owned CA bundle")
}

func TestYarnSetupInjectsFirewallPlugin(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		parent      []string
		userPlugins string
	}{
		{"uppercase", []string{"YARN_PLUGINS=/home/u/user.cjs"}, "/home/u/user.cjs"},
		{"lowercase", []string{"yarn_plugins=/home/u/user.cjs"}, "/home/u/user.cjs"},
		{"last of both cases wins", []string{"YARN_PLUGINS=/home/u/a.cjs", "yarn_plugins=/home/u/b.cjs"}, "/home/u/b.cjs"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			res, err := yarnManager{}.Setup(t.Context(), setupInput{parent: c.parent})
			require.NoError(t, err)
			assert.Nil(t, res.verdicts, "yarn records no verdicts of its own")
			pluginPath, userPlugins, ok := strings.Cut(lookupEnv(res.env, "YARN_PLUGINS"), ";")
			require.True(t, ok, "YARN_PLUGINS must list the firewall plugin and keep the user's")
			assert.Equal(t, c.userPlugins, userPlugins)

			plugin, err := os.ReadFile(pluginPath)
			require.NoError(t, err)
			assert.Equal(t, yarnFirewallPlugin, plugin)

			res.stop()
			_, err = os.Stat(pluginPath)
			assert.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestYarnSetupWithoutUserPluginsListsOnlyFirewallPlugin(t *testing.T) {
	t.Parallel()
	res, err := yarnManager{}.Setup(t.Context(), setupInput{parent: []string{"PATH=/usr/bin"}})
	require.NoError(t, err)
	t.Cleanup(res.stop)
	// A trailing ";" would add an empty entry, which Berry resolves to the
	// working directory and tries to load as a plugin.
	pluginPath := lookupEnv(res.env, "YARN_PLUGINS")
	assert.NotContains(t, pluginPath, ";")
	assert.FileExists(t, pluginPath)
}

func TestYarnSetupFailsWhenPluginCannotBeWritten(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	t.Setenv("TMPDIR", missing)
	t.Setenv("TMP", missing)
	t.Setenv("TEMP", missing)

	res, err := yarnManager{}.Setup(t.Context(), setupInput{})
	require.Error(t, err)
	assert.Empty(t, res.env, "a failed write must not point YARN_PLUGINS at any path")
}
