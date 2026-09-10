//go:build !integration

package pm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		assert.Equal(t, []string{"NODE_EXTRA_CA_CERTS=" + caPath}, Yarn().CATrustEnviron(caPath))
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
		assert.Equal(t, []string{"REQUESTS_CA_BUNDLE=" + caPath}, Poetry().CATrustEnviron(caPath))
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
}

// TestManagerExistingBundleVars pins each manager's declared CA-preservation
// variables. Every variable a manager's CATrustEnviron overwrites must be
// listed here, or a user who set only the undeclared one silently loses their
// trust anchors — a "wrong string, silent breakage" bug the engine can't catch.
func TestManagerExistingBundleVars(t *testing.T) {
	t.Parallel()
	cases := []struct {
		m    PackageManager
		want []string
	}{
		{NPM(), []string{"NODE_EXTRA_CA_CERTS"}},
		{Pnpm(), []string{"NODE_EXTRA_CA_CERTS"}},
		{Yarn(), []string{"NODE_EXTRA_CA_CERTS"}},
		{Pip(), []string{"PIP_CERT", "REQUESTS_CA_BUNDLE"}},
		{Pipenv(), []string{"PIP_CERT", "REQUESTS_CA_BUNDLE"}},
		{Uv(), []string{"SSL_CERT_FILE"}},
		{Poetry(), []string{"REQUESTS_CA_BUNDLE"}},
		{Twine(), []string{"TWINE_CERT", "REQUESTS_CA_BUNDLE"}},
		{Gem(), []string{"SSL_CERT_FILE"}},
		{Bundle(), []string{"SSL_CERT_FILE", "BUNDLE_SSL_CA_CERT"}},
		// Maven and Gradle preserve no CA bundle var: they trust the proxy
		// through a generated JVM truststore, not an env-named CA file.
		{Maven(), nil},
		{Gradle(), nil},
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
				if v == "REQUESTS_CA_BUNDLE" && c.m.Name() == "twine" {
					continue // twine reads REQUESTS_CA_BUNDLE but sets only TWINE_CERT
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
	for _, m := range []PackageManager{NPM(), Pnpm(), Yarn(), Pip(), Pipenv(), Uv(), Poetry(), Twine(), Gem(), Bundle()} {
		m.CleanupCAFiles(caPath)
	}
	_, err := os.Stat(caPath)
	assert.NoError(t, err, "env-proxy managers must not remove the engine-owned CA bundle")
}
