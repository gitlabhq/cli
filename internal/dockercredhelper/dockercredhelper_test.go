//go:build !integration

package dockercredhelper

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
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

// unwritableGlabDir returns a directory holding a fake glab that the test user
// cannot write to, standing in for the /usr/bin a .deb or .rpm package
// installs glab into, and for a read-only store such as Nix's.
//
// It skips rather than fakes when the mode cannot be enforced: Windows does
// not apply POSIX modes, and root bypasses them.
func unwritableGlabDir(t *testing.T) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory modes not enforced on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses the directory mode this test relies on")
	}

	dir := t.TempDir()
	writeFakeGlab(t, dir)
	require.NoError(t, os.Chmod(dir, 0o555))
	// t.TempDir's own cleanup needs the write bit back to remove the contents.
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	return dir
}

// sandboxHome points $HOME at a scratch directory and returns the
// ~/.local/bin path under it. The directory is deliberately not created:
// which candidate Install picks must not depend on ~/.local/bin already
// existing, and one test asserts Install creates it.
func sandboxHome(t *testing.T) string {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	return filepath.Join(home, ".local", "bin")
}

func TestInstall_WritesExecutableShimNextToGlab(t *testing.T) {
	binDir := t.TempDir()
	writeFakeGlab(t, binDir)
	t.Setenv("PATH", binDir)

	installed, err := Install()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(binDir, FullName), installed.Path)
	assert.True(t, installed.OnPath)

	content, err := os.ReadFile(installed.Path)
	require.NoError(t, err)
	assert.Equal(t, "#!/bin/sh -eu\nexec glab auth docker-helper \"$@\"\n", string(content))

	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes not enforced on Windows")
	}
	info, err := os.Stat(installed.Path)
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

// The precedence tests below drive candidates and unusable directly rather
// than going through Install. Both are pure, so unlike the chmod-based tests
// further down they run everywhere, including the root CI container where a
// directory mode cannot make a write fail.

func TestCandidates_PrefersGlabsDirectoryThenLocalBin(t *testing.T) {
	localBin := sandboxHome(t)
	t.Setenv("PATH", "/opt/tools/bin"+string(os.PathListSeparator)+"/usr/bin")

	got, err := candidates("/usr/bin")
	require.NoError(t, err)

	assert.Equal(t, []candidate{
		{dir: "/usr/bin", onPath: true},
		{dir: localBin, create: true},
		{dir: "/opt/tools/bin", onPath: true},
	}, got)
}

// TestCandidates_RanksLocalBinAheadOfMachineWideEntries is the ordering that
// keeps two users on a shared host from breaking each other: the 0700 shim in
// a group-writable /usr/local/bin is unusable by everyone but its installer,
// so an off-PATH ~/.local/bin and a warning is the better outcome.
func TestCandidates_RanksLocalBinAheadOfMachineWideEntries(t *testing.T) {
	localBin := sandboxHome(t)
	t.Setenv("PATH", "/usr/bin"+string(os.PathListSeparator)+"/usr/local/bin")

	got, err := candidates("/usr/bin")
	require.NoError(t, err)

	require.Len(t, got, 3)
	assert.Equal(t, candidate{dir: localBin, create: true}, got[1])
	assert.Equal(t, candidate{dir: "/usr/local/bin", onPath: true}, got[2],
		"a machine-wide PATH entry must rank below the user's own directory")
}

// TestCandidates_MarksLocalBinOnPath covers the flag that decides whether the
// caller warns: the same directory, reachable by Docker, must not warn.
func TestCandidates_MarksLocalBinOnPath(t *testing.T) {
	localBin := sandboxHome(t)
	t.Setenv("PATH", "/usr/bin"+string(os.PathListSeparator)+localBin)

	got, err := candidates("/usr/bin")
	require.NoError(t, err)

	require.Len(t, got, 2)
	assert.Equal(t, candidate{dir: localBin, onPath: true, create: true}, got[1])
}

// TestCandidates_DedupesOnCleanedPaths pins that dedup keys on the cleaned
// path: a PATH carrying both spellings of one directory must not attempt the
// same write twice, nor name it twice in the failure.
func TestCandidates_DedupesOnCleanedPaths(t *testing.T) {
	sandboxHome(t)
	t.Setenv("PATH", strings.Join([]string{"/usr/bin", "/usr/bin/", "/usr/./bin"}, string(os.PathListSeparator)))

	got, err := candidates("/usr/bin")
	require.NoError(t, err)

	dirs := make([]string, 0, len(got))
	for _, c := range got {
		dirs = append(dirs, c.dir)
	}
	assert.Equal(t, 1, slices.Index(dirs, "/usr/bin")+1, "/usr/bin should appear once, first")
	assert.NotContains(t, dirs, "/usr/bin/")
}

// TestCandidates_SkipsRelativePathEntries covers the empty PATH segment, which
// means the working directory: honoring it would put the shim wherever glab
// was run from.
func TestCandidates_SkipsRelativePathEntries(t *testing.T) {
	sandboxHome(t)
	t.Setenv("PATH", strings.Join([]string{"/usr/bin", "", "relative/bin", "."}, string(os.PathListSeparator)))

	got, err := candidates("/usr/bin")
	require.NoError(t, err)

	for _, c := range got {
		assert.True(t, filepath.IsAbs(c.dir), "candidate %q should be absolute", c.dir)
	}
}

// TestCandidates_ReportsAnUnresolvableHomeDirectory covers dropping the
// ~/.local/bin candidate: the PATH entries still stand, so it is not fatal,
// but the reason has to reach the caller.
func TestCandidates_ReportsAnUnresolvableHomeDirectory(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("PATH", "/usr/bin")

	got, err := candidates("/usr/bin")
	require.ErrorContains(t, err, "home directory")
	assert.Equal(t, []candidate{{dir: "/usr/bin", onPath: true}}, got,
		"the PATH entry must survive a home-directory failure")
}

// TestUnusable_ClassifiesDirectoryFailures guards the fail-fast branch in
// Install: only an error meaning "this directory will not do" moves on to the
// next candidate. A full disk would fail identically everywhere, so retrying
// it down the whole of PATH would bury the real cause.
func TestUnusable_ClassifiesDirectoryFailures(t *testing.T) {
	assert.True(t, unusable(fs.ErrPermission))
	assert.True(t, unusable(syscall.EROFS))
	assert.True(t, unusable(syscall.ENOTDIR))
	assert.True(t, unusable(fs.ErrNotExist))
	assert.True(t, unusable(fmt.Errorf("wrapped: %w", syscall.EACCES)))

	assert.False(t, unusable(syscall.ENOSPC), "a full disk must fail the install, not skip the directory")
	assert.False(t, unusable(errors.New("some other failure")))
}

// TestPathWarning_NamesTheDirectoryToAdd covers the message both callers
// print verbatim: it is the user's only pointer to a shim Docker cannot
// resolve, so it has to name the directory rather than the script's full path.
func TestPathWarning_NamesTheDirectoryToAdd(t *testing.T) {
	warning := Installation{Path: "/home/u/.local/bin/" + FullName}.PathWarning()

	assert.Contains(t, warning, "/home/u/.local/bin")
	assert.Contains(t, warning, "not on your PATH")
	assert.NotContains(t, warning, "/home/u/.local/bin/"+FullName,
		"the directory to add is the remedy, not the file's own path")
}

// TestWriteShim_ReportsAnUnusableParent uses the blocker-file trick from
// internal/oauth2's unwritableDir: a regular file where a parent directory
// should be makes MkdirAll fail with ENOTDIR, which root cannot bypass, so
// the create path and its error classification are exercised in CI too.
func TestWriteShim_ReportsAnUnusableParent(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))

	err := writeShim(filepath.Join(blocker, "bin", FullName), true)
	require.Error(t, err)
	assert.True(t, unusable(err), "an unusable parent must let Install try the next candidate, got %v", err)
}

// assertShim checks that path holds the shim script at an executable mode,
// so the fallback tests below assert the shim is usable where it landed and
// not merely that a file exists there.
func assertShim(t *testing.T, path string) {
	t.Helper()

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(script), string(content))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

// TestInstall_FallsBackToLocalBinOnPath is the case reported in #8541: glab
// comes from a package that put it in a root-owned directory, so the shim
// cannot go next to it and has to land in the user's own bin directory.
func TestInstall_FallsBackToLocalBinOnPath(t *testing.T) {
	glabDir := unwritableGlabDir(t)
	localBin := sandboxHome(t)
	t.Setenv("PATH", glabDir+string(os.PathListSeparator)+localBin)

	installed, err := Install()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(localBin, FullName), installed.Path)
	assert.True(t, installed.OnPath, "~/.local/bin is on PATH here, so no warning is warranted")
	assertShim(t, installed.Path)
}

// TestInstall_PrefersLocalBinOverAnotherWritablePathEntry is the whole
// install exercising the ordering TestCandidates_RanksLocalBinAheadOfMachineWideEntries
// pins: a writable PATH entry would work without a warning, and is still not
// taken, because a 0700 shim in a directory shared with other users breaks
// both of them.
func TestInstall_PrefersLocalBinOverAnotherWritablePathEntry(t *testing.T) {
	glabDir := unwritableGlabDir(t)
	writableDir := t.TempDir()
	localBin := sandboxHome(t)
	t.Setenv("PATH", glabDir+string(os.PathListSeparator)+writableDir)

	installed, err := Install()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(localBin, FullName), installed.Path)
	assert.False(t, installed.OnPath, "~/.local/bin is off PATH here, so the caller must warn")
	assertShim(t, installed.Path)

	_, statErr := os.Stat(filepath.Join(writableDir, FullName))
	assert.True(t, os.IsNotExist(statErr), "the shared PATH entry must be left alone")
}

// TestInstall_UsesAPathEntryWhenLocalBinIsUnavailable covers the remaining
// use for the other PATH entries: with no home directory to resolve, a
// writable PATH entry is all that is left.
func TestInstall_UsesAPathEntryWhenLocalBinIsUnavailable(t *testing.T) {
	glabDir := unwritableGlabDir(t)
	writableDir := t.TempDir()
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("PATH", glabDir+string(os.PathListSeparator)+writableDir)

	installed, err := Install()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(writableDir, FullName), installed.Path)
	assert.True(t, installed.OnPath)
	assertShim(t, installed.Path)
}

// TestInstall_CreatesLocalBinWhenNothingOnPathIsWritable is the last resort:
// PATH holds nothing this user can write, so the shim goes to the
// conventional per-user bin directory even though Docker cannot resolve it
// until the user adds that directory to PATH.
func TestInstall_CreatesLocalBinWhenNothingOnPathIsWritable(t *testing.T) {
	glabDir := unwritableGlabDir(t)
	localBin := sandboxHome(t)
	t.Setenv("PATH", glabDir)

	installed, err := Install()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(localBin, FullName), installed.Path)
	assert.False(t, installed.OnPath, "the caller has to warn: Docker cannot resolve the shim from here")
	assert.Contains(t, installed.PathWarning(), localBin)
	assertShim(t, installed.Path)
}

// TestInstall_PrefersGlabsDirectoryOverLocalBin pins the precedence that
// keeps an existing install idempotent: when glab's own directory is
// writable, a shim already sitting next to it is updated in place rather
// than orphaned by a second copy appearing in ~/.local/bin.
func TestInstall_PrefersGlabsDirectoryOverLocalBin(t *testing.T) {
	glabDir := t.TempDir()
	writeFakeGlab(t, glabDir)
	localBin := sandboxHome(t)
	require.NoError(t, os.MkdirAll(localBin, 0o755))
	t.Setenv("PATH", glabDir+string(os.PathListSeparator)+localBin)

	installed, err := Install()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(glabDir, FullName), installed.Path)

	_, statErr := os.Stat(filepath.Join(localBin, FullName))
	assert.True(t, os.IsNotExist(statErr), "a second shim must not appear in ~/.local/bin")
}

// TestInstall_NoWritableDirectoryAnywhere covers the only remaining failure:
// the error has to name the directories that refused the write, because the
// user's remedy is to make one of them writable or to put a writable one on
// PATH.
func TestInstall_NoWritableDirectoryAnywhere(t *testing.T) {
	glabDir := unwritableGlabDir(t)
	home := unwritableGlabDir(t)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", glabDir)

	_, err := Install()
	require.ErrorContains(t, err, glabDir)
	assert.ErrorContains(t, err, filepath.Join(home, ".local", "bin"))
}

// TestInstall_ReportsAnUnresolvableHomeDirectory covers the failure with no
// home directory to fall back to: dropping ~/.local/bin from the candidates
// silently would leave the user reading a list of PATH directories with no
// hint that an unset $HOME is why the usual fallback is missing from it.
func TestInstall_ReportsAnUnresolvableHomeDirectory(t *testing.T) {
	glabDir := unwritableGlabDir(t)
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("PATH", glabDir)

	_, err := Install()
	require.ErrorContains(t, err, glabDir)
	assert.ErrorContains(t, err, "home directory")
}

func TestInstall_GlabNotOnPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	installed, err := Install()
	require.Error(t, err)
	assert.Empty(t, installed.Path)
}

func TestInstall_UnsupportedOS(t *testing.T) {
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		t.Skip("only exercises the unsupported-OS error path")
	}

	_, err := Install()
	require.ErrorContains(t, err, "is not supported")
	assert.ErrorContains(t, err, runtime.GOOS, "the error should name the unsupported OS")
}

// TestLocate_ResolvesTheDirectoryInstallWritesInto pins what the returned path
// is for: Install joins FullName onto its directory, so a caller checking
// Locate up front is checking the same lookup that decides where the shim
// lands.
func TestLocate_ResolvesTheDirectoryInstallWritesInto(t *testing.T) {
	binDir := t.TempDir()
	writeFakeGlab(t, binDir)
	t.Setenv("PATH", binDir)

	path, err := Locate()
	require.NoError(t, err)
	assert.Equal(t, binDir, filepath.Dir(path))
}

// TestLocate_AgreesWithInstall pins what a caller that checks Locate up front
// relies on: Install resolves glab through Locate too, so on a supported
// platform it never fails for a reason Locate would have caught first. The
// counterpart of TestSupported_AgreesWithInstall for the other precondition.
func TestLocate_AgreesWithInstall(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	_, locateErr := Locate()
	require.ErrorContains(t, locateErr, "glab")

	_, installErr := Install()
	require.Error(t, installErr)

	if Supported() != nil {
		// Install rejects the platform before it ever looks glab up, so the two
		// errors legitimately differ here.
		assert.ErrorContains(t, installErr, "is not supported")
		return
	}
	assert.Equal(t, locateErr.Error(), installErr.Error(), "Install must surface Locate's error unchanged")
}

// TestSupported_AgreesWithInstall pins that the two never disagree about the
// platform: callers check Supported up front and then rely on Install not
// failing for that reason later.
func TestSupported_AgreesWithInstall(t *testing.T) {
	// An empty PATH makes a supported platform fail Install for the only other
	// reason it can, which must not read as an unsupported platform.
	t.Setenv("PATH", t.TempDir())
	_, installErr := Install()
	require.Error(t, installErr)

	if err := Supported(); err != nil {
		assert.ErrorContains(t, err, runtime.GOOS)
		assert.ErrorContains(t, installErr, "is not supported")
		return
	}
	assert.NotContains(t, installErr.Error(), "is not supported")
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
