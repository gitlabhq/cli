//go:build !integration

package login

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func npmrcPath(home string) string {
	return filepath.Join(home, ".npmrc")
}

func TestLoginNpm_CreatesFileWhenAbsent(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	require.NoError(t, loginNpm(mustURL(t, "https://registry.example.com/"), "tok1"))

	want := "//registry.example.com/:_authToken=tok1\n"

	got, err := os.ReadFile(npmrcPath(home))
	require.NoError(t, err)
	assert.Equal(t, want, string(got))

	fileInfo, err := os.Stat(npmrcPath(home))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fileInfo.Mode().Perm())
}

// TestLoginNpm_CreatesFileWhenAbsent_BareHost guards against building the
// line as u.Host+u.Path+":_authToken=" with no separator inserted when the
// registry URL has no trailing slash (and thus no path): that produces
// "//host:_authToken=...", missing the "/" npm requires before
// ":_authToken=".
func TestLoginNpm_CreatesFileWhenAbsent_BareHost(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	require.NoError(t, loginNpm(mustURL(t, "https://registry.example.com"), "tok1"))

	want := "//registry.example.com/:_authToken=tok1\n"

	got, err := os.ReadFile(npmrcPath(home))
	require.NoError(t, err)
	assert.Equal(t, want, string(got))
}

// TestLoginNpm_CreatesFileWhenAbsent_PathWithoutTrailingSlash guards
// against the same missing-separator bug for a registry URL that has a
// path but no trailing slash.
func TestLoginNpm_CreatesFileWhenAbsent_PathWithoutTrailingSlash(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	require.NoError(t, loginNpm(mustURL(t, "https://registry.example.com/api/v4/projects/1/packages/npm"), "tok1"))

	want := "//registry.example.com/api/v4/projects/1/packages/npm/:_authToken=tok1\n"

	got, err := os.ReadFile(npmrcPath(home))
	require.NoError(t, err)
	assert.Equal(t, want, string(got))
}

func TestLoginNpm_CreatesFileWhenAbsent_WithPath(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	require.NoError(t, loginNpm(mustURL(t, "https://registry.example.com/api/v4/projects/1/packages/npm/"), "tok1"))

	want := "//registry.example.com/api/v4/projects/1/packages/npm/:_authToken=tok1\n"

	got, err := os.ReadFile(npmrcPath(home))
	require.NoError(t, err)
	assert.Equal(t, want, string(got))
}

// TestLoginNpm_KeepsThePort pins that, unlike sbt, npm's key retains the
// registry's port: npm's .npmrc keys are the full registry URL including it,
// and npm matches on that. A registry on a non-standard port must not be
// normalized to a port-less key the way sbt's host is.
func TestLoginNpm_KeepsThePort(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	require.NoError(t, loginNpm(mustURL(t, "https://registry.example.com:8443/"), "tok1"))

	want := "//registry.example.com:8443/:_authToken=tok1\n"

	got, err := os.ReadFile(npmrcPath(home))
	require.NoError(t, err)
	assert.Equal(t, want, string(got))
}

// TestLoginNpm_DropsTheSchemeDefaultPort pins the other half of that rule: npm
// builds its key with the WHATWG URL parser, which omits the scheme's default
// port, so writing ":443" (or ":80" on http) leaves a key npm never looks up
// and installs go out anonymous after a green login.
func TestLoginNpm_DropsTheSchemeDefaultPort(t *testing.T) {
	for registry, want := range map[string]string{
		"https://ar.example.com:443/api/v4/": "//ar.example.com/api/v4/:_authToken=tok1\n",
		"http://ar.example.com:80/api/v4/":   "//ar.example.com/api/v4/:_authToken=tok1\n",
		"https://ar.example.com:80/api/v4/":  "//ar.example.com:80/api/v4/:_authToken=tok1\n",
	} {
		t.Run(registry, func(t *testing.T) {
			home := t.TempDir()
			setHome(t, home)

			require.NoError(t, loginNpm(mustURL(t, registry), "tok1"))

			got, err := os.ReadFile(npmrcPath(home))
			require.NoError(t, err)
			assert.Equal(t, want, string(got))
		})
	}
}

// TestLoginNpm_LowerCasesTheHost pins that the key is written with a
// lower-cased host. npm derives this key by parsing the registry URL, which
// normalizes the host to lower case, so a key holding what the user typed would
// never be found: a 401 at install time with nothing wrong at login time.
func TestLoginNpm_LowerCasesTheHost(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	require.NoError(t, loginNpm(mustURL(t, "https://AR.Example.COM:8443/API/v4/"), "tok1"))

	// The path keeps its case, since URL paths are case-sensitive.
	want := "//ar.example.com:8443/API/v4/:_authToken=tok1\n"

	got, err := os.ReadFile(npmrcPath(home))
	require.NoError(t, err)
	assert.Equal(t, want, string(got))
}

// TestLoginNpm_RefreshMatchesDifferentlyCasedRegistry covers the upsert side of
// lower-casing: the same registry typed two ways must land on one line.
func TestLoginNpm_RefreshMatchesDifferentlyCasedRegistry(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	require.NoError(t, loginNpm(mustURL(t, "https://AR.Example.com/"), "tok1"))
	require.NoError(t, loginNpm(mustURL(t, "https://ar.example.com/"), "tok2"))

	got, err := os.ReadFile(npmrcPath(home))
	require.NoError(t, err)
	content := string(got)

	assert.Equal(t, 1, strings.Count(content, "_authToken="))
	assert.Contains(t, content, "//ar.example.com/:_authToken=tok2")
}

func TestLoginNpm_PreservesUnrelatedContent(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "registry=https://registry.npmjs.org/\n"
	require.NoError(t, os.WriteFile(npmrcPath(home), []byte(fixture), 0o600))

	require.NoError(t, loginNpm(mustURL(t, "https://registry.example.com/"), "tok1"))

	got, err := os.ReadFile(npmrcPath(home))
	require.NoError(t, err)
	content := string(got)

	assert.Contains(t, content, "registry=https://registry.npmjs.org/")
	assert.Contains(t, content, "//registry.example.com/:_authToken=tok1")
}

func TestLoginNpm_UpdatesExistingEntryInPlace(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	require.NoError(t, loginNpm(mustURL(t, "https://registry.example.com/"), "tok1"))
	require.NoError(t, loginNpm(mustURL(t, "https://registry.example.com/"), "tok2"))

	got, err := os.ReadFile(npmrcPath(home))
	require.NoError(t, err)
	content := string(got)

	assert.Equal(t, 1, strings.Count(content, "//registry.example.com/:_authToken="), "must not duplicate the auth token line")
	assert.Contains(t, content, "//registry.example.com/:_authToken=tok2")
	assert.NotContains(t, content, "//registry.example.com/:_authToken=tok1")
}

func TestLoginNpm_DifferentRegistryAddsSeparateEntry_NoPrefixCrossMatch(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	// "registry.example.com" is a string-prefix of
	// "registry.example.com.other" -- they must be treated as distinct
	// hosts, not matched against each other.
	require.NoError(t, loginNpm(mustURL(t, "https://registry.example.com/"), "tok1"))
	require.NoError(t, loginNpm(mustURL(t, "https://registry.example.com.other/"), "tok2"))

	got, err := os.ReadFile(npmrcPath(home))
	require.NoError(t, err)
	content := string(got)

	assert.Equal(t, 1, strings.Count(content, "//registry.example.com/:_authToken="))
	assert.Equal(t, 1, strings.Count(content, "//registry.example.com.other/:_authToken="))
	assert.Contains(t, content, "//registry.example.com/:_authToken=tok1")
	assert.Contains(t, content, "//registry.example.com.other/:_authToken=tok2")

	// Updating the first registry's token must not disturb the second.
	require.NoError(t, loginNpm(mustURL(t, "https://registry.example.com/"), "tok1-updated"))

	got, err = os.ReadFile(npmrcPath(home))
	require.NoError(t, err)
	content = string(got)

	assert.Equal(t, 1, strings.Count(content, "//registry.example.com/:_authToken="))
	assert.Contains(t, content, "//registry.example.com/:_authToken=tok1-updated")
	assert.Contains(t, content, "//registry.example.com.other/:_authToken=tok2")
}

// TestLoginNpm_LeavesACommentedEntryAlone pins that a commented-out entry is
// not refreshed in place. npm reads ";" and "#" as comment markers, so writing
// the token there would leave npm with no credential after a login that
// reported success. The match is a line prefix, which already excludes both,
// and this keeps it that way.
func TestLoginNpm_LeavesACommentedEntryAlone(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := ";//registry.example.com/:_authToken=commented-out\n" +
		"#//registry.example.com/:_authToken=also-commented-out\n"
	require.NoError(t, os.WriteFile(npmrcPath(home), []byte(fixture), 0o600))

	require.NoError(t, loginNpm(mustURL(t, "https://registry.example.com/"), "tok1"))

	got, err := os.ReadFile(npmrcPath(home))
	require.NoError(t, err)
	content := string(got)

	assert.Contains(t, content, ";//registry.example.com/:_authToken=commented-out")
	assert.Contains(t, content, "#//registry.example.com/:_authToken=also-commented-out")

	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	assert.Equal(t, "//registry.example.com/:_authToken=tok1", lines[len(lines)-1], "the live entry is appended instead")
}

// TestLoginNpm_KeepsThePathPercentEncoded pins that the key keeps the path's
// percent-encoding. npm builds its lookup key from the URL's pathname, which is
// not decoded, and walks it up one whole segment at a time, so a decoded
// spelling of an encoded project path is a key npm never looks for: the request
// goes out anonymous and 401s at install time, with a green check at login
// time.
func TestLoginNpm_KeepsThePathPercentEncoded(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	require.NoError(t, loginNpm(mustURL(t, "https://gl.example.com/api/v4/projects/group%2Fproj/packages/npm/"), "tok1"))

	got, err := os.ReadFile(npmrcPath(home))
	require.NoError(t, err)

	assert.Equal(t, "//gl.example.com/api/v4/projects/group%2Fproj/packages/npm/:_authToken=tok1\n", string(got))
}

// TestLoginNpm_RefreshMatchesAnIndentedEntry pins that an indented entry is
// refreshed rather than duplicated. npm's ini parser trims every key before
// matching it, so the indented line is live: appending next to it would leave a
// superseded token in the file and add a line per login.
func TestLoginNpm_RefreshMatchesAnIndentedEntry(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "  //registry.example.com/:_authToken=old\n"
	require.NoError(t, os.WriteFile(npmrcPath(home), []byte(fixture), 0o600))

	require.NoError(t, loginNpm(mustURL(t, "https://registry.example.com/"), "tok1"))

	got, err := os.ReadFile(npmrcPath(home))
	require.NoError(t, err)

	assert.Equal(t, "  //registry.example.com/:_authToken=tok1\n", string(got), "the entry is refreshed in place, keeping its indentation")
}

// TestLoginNpm_HonoursNpmConfigUserconfig pins that the token lands where npm
// reads it: npm_config_userconfig overrides ~/.npmrc, so writing the default
// path on a machine that sets it leaves the credential in a file npm never
// opens.
func TestLoginNpm_HonoursNpmConfigUserconfig(t *testing.T) {
	for _, name := range []string{"npm_config_userconfig", "NPM_CONFIG_USERCONFIG"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			setHome(t, home)

			userconfig := filepath.Join(t.TempDir(), "npm", "config")
			t.Setenv(name, userconfig)

			require.NoError(t, loginNpm(mustURL(t, "https://registry.example.com/"), "tok1"))

			got, err := os.ReadFile(userconfig)
			require.NoError(t, err)
			assert.Equal(t, "//registry.example.com/:_authToken=tok1\n", string(got))

			_, err = os.Stat(npmrcPath(home))
			assert.ErrorIs(t, err, os.ErrNotExist, "nothing may be written to ~/.npmrc when the variable is set")
		})
	}
}
