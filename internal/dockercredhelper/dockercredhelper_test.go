//go:build !integration

package dockercredhelper

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFakeGlab drops an executable file named "glab" (or "glab.exe" on
// Windows) into dir, so exec.LookPath("glab") succeeds against a PATH
// containing dir, without depending on a real glab build.
func writeFakeGlab(t *testing.T, dir string) {
	t.Helper()

	name := "glab"
	if runtime.GOOS == "windows" {
		name = "glab.exe"
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755))
}

func TestInstall_WritesExecutableShimNextToGlab(t *testing.T) {
	binDir := t.TempDir()
	writeFakeGlab(t, binDir)
	t.Setenv("PATH", binDir)

	path, err := Install()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(binDir, FullName), path)

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "#!/bin/sh -eu\nglab auth docker-helper \"$@\"\n", string(content))

	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes not enforced on Windows")
	}
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

// TestInstall_ForcesModeOnPreExistingShim covers that re-running Install
// always brings a stale shim's mode back to 0o700, regardless of whatever
// mode a previous version of the file was left at.
func TestInstall_ForcesModeOnPreExistingShim(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes not enforced on Windows")
	}
	binDir := t.TempDir()
	writeFakeGlab(t, binDir)
	t.Setenv("PATH", binDir)

	shimPath := filepath.Join(binDir, FullName)
	require.NoError(t, os.WriteFile(shimPath, []byte("stale\n"), 0o644))

	_, err := Install()
	require.NoError(t, err)

	info, err := os.Stat(shimPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

func TestInstall_GlabNotOnPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	path, err := Install()
	require.Error(t, err)
	assert.Empty(t, path)
}

func TestInstall_UnsupportedOS(t *testing.T) {
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		t.Skip("only exercises the unsupported-OS error path")
	}

	_, err := Install()
	require.ErrorContains(t, err, "is not supported")
	assert.ErrorContains(t, err, runtime.GOOS, "the error should name the unsupported OS")
}

// readDockerConfig returns the parsed config.json from dir.
func readDockerConfig(t *testing.T, dir string) map[string]any {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))
	return parsed
}

// writeDockerConfig seeds dir with a config.json holding body.
func writeDockerConfig(t *testing.T, dir, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600))
}

func credHelpers(t *testing.T, dir string) map[string]any {
	t.Helper()

	helpers, ok := readDockerConfig(t, dir)["credHelpers"].(map[string]any)
	require.True(t, ok, "credHelpers should be present in config.json")
	return helpers
}

func authEntries(t *testing.T, dir string) map[string]any {
	t.Helper()

	auths, ok := readDockerConfig(t, dir)["auths"].(map[string]any)
	require.True(t, ok, "auths should be present in config.json")
	return auths
}

func TestRegister_WritesCredHelpers(t *testing.T) {
	dir := t.TempDir()

	got, err := Register(dir, "registry.example.com", "registry.other.example.com")
	require.NoError(t, err)

	assert.Equal(t, []Registration{
		{Domain: "registry.example.com"},
		{Domain: "registry.other.example.com"},
	}, got)

	helpers := credHelpers(t, dir)
	assert.Equal(t, ShortName, helpers["registry.example.com"])
	assert.Equal(t, ShortName, helpers["registry.other.example.com"])
}

// TestConfigDir_HonorsDockerConfigEnv covers why ConfigDir exists rather than
// passing "" to dockerconfig.Load: writing to the wrong place fails silently,
// because Docker reads a config.json with no entry in it.
func TestConfigDir_HonorsDockerConfigEnv(t *testing.T) {
	home := t.TempDir()
	dockerDir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DOCKER_CONFIG", dockerDir)

	dir, err := ConfigDir()
	require.NoError(t, err)
	assert.Equal(t, dockerDir, dir)
}

// TestConfigDir_FallsBackToDockerHomeDir covers the no-$DOCKER_CONFIG default.
func TestConfigDir_FallsBackToDockerHomeDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DOCKER_CONFIG", "")

	dir, err := ConfigDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".docker"), dir)
}

// TestRegister_RefusesToReplaceAnotherHelper is the guard that matters most:
// credHelpers holds one helper per domain and Docker consults it ahead of every
// other credential source, so overwriting an entry silently takes a registry
// away from whichever tool owns it, and Save discards the old value.
func TestRegister_RefusesToReplaceAnotherHelper(t *testing.T) {
	dir := t.TempDir()
	writeDockerConfig(t, dir, `{"credHelpers":{"registry.example.com":"ecr-login"}}`)

	got, err := Register(dir, "registry.example.com")
	require.Error(t, err)
	assert.Nil(t, got)

	var conflict *ConflictError
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, map[string]string{"registry.example.com": "ecr-login"}, conflict.Helpers)
	assert.Contains(t, err.Error(), "ecr-login")
	assert.Contains(t, err.Error(), filepath.Join(dir, "config.json"))

	assert.Equal(t, "ecr-login", credHelpers(t, dir)["registry.example.com"],
		"the existing helper must be left untouched")
}

// TestRegister_WritesNothingWhenAnyDomainConflicts pins the check-all-first
// ordering: a conflict on the second domain must not leave the first written.
func TestRegister_WritesNothingWhenAnyDomainConflicts(t *testing.T) {
	dir := t.TempDir()
	writeDockerConfig(t, dir, `{"credHelpers":{"claimed.example.com":"gcloud"}}`)

	_, err := Register(dir, "free.example.com", "claimed.example.com")
	require.Error(t, err)

	helpers := credHelpers(t, dir)
	assert.NotContains(t, helpers, "free.example.com", "no domain should have been written")
	assert.Equal(t, "gcloud", helpers["claimed.example.com"])
}

// TestRegister_PreservesUnrelatedEntries guards the success path: writing the
// new domain's entry must not disturb credHelpers or auths entries for
// domains Register was never asked about.
func TestRegister_PreservesUnrelatedEntries(t *testing.T) {
	dir := t.TempDir()
	writeDockerConfig(t, dir, `{
		"credHelpers":{"other.example.com":"ecr-login"},
		"auths":{"another.example.com":{"auth":"dXNlcjpwYXNz"}}
	}`)

	got, err := Register(dir, "new.example.com")
	require.NoError(t, err)
	assert.Equal(t, []Registration{{Domain: "new.example.com"}}, got)

	helpers := credHelpers(t, dir)
	assert.Equal(t, "ecr-login", helpers["other.example.com"], "unrelated helper must survive")
	assert.Equal(t, ShortName, helpers["new.example.com"])

	auths := authEntries(t, dir)
	assert.Contains(t, auths, "another.example.com", "unrelated login must survive")
}

// TestRegister_ReRegisteringGlabIsNotAConflict keeps the command idempotent.
func TestRegister_ReRegisteringGlabIsNotAConflict(t *testing.T) {
	dir := t.TempDir()
	writeDockerConfig(t, dir, `{"credHelpers":{"registry.example.com":"glab"}}`)

	got, err := Register(dir, "registry.example.com")
	require.NoError(t, err)
	assert.Equal(t, []Registration{{Domain: "registry.example.com"}}, got)
}

// TestRegister_ReportsShadowedLogin covers the softer half: an existing
// `docker login` is not destroyed, but Docker stops consulting it.
func TestRegister_ReportsShadowedLogin(t *testing.T) {
	dir := t.TempDir()
	writeDockerConfig(t, dir, `{"auths":{"registry.example.com":{"auth":"dXNlcjpwYXNz"}}}`)

	got, err := Register(dir, "registry.example.com")
	require.NoError(t, err)
	assert.Equal(t, []Registration{{Domain: "registry.example.com", ShadowedLogin: true}}, got)
}

// TestRegister_ReportsShadowedLoginUnderCredsStore is why the code checks
// whether the key exists rather than what it holds: with a credsStore
// configured, `docker login` leaves an empty marker entry and keeps the
// secret in the OS keychain, while `docker logout` removes the key outright.
func TestRegister_ReportsShadowedLoginUnderCredsStore(t *testing.T) {
	dir := t.TempDir()
	writeDockerConfig(t, dir, `{"credsStore":"desktop","auths":{"registry.example.com":{}}}`)

	got, err := Register(dir, "registry.example.com")
	require.NoError(t, err)
	assert.Equal(t, []Registration{{Domain: "registry.example.com", ShadowedLogin: true}}, got)
}

func TestRegister_NoShadowWithoutAnExistingLogin(t *testing.T) {
	dir := t.TempDir()

	got, err := Register(dir, "registry.example.com")
	require.NoError(t, err)
	assert.Equal(t, []Registration{{Domain: "registry.example.com"}}, got)
}
