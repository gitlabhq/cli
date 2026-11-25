//go:build !integration

package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/adrg/xdg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	"gitlab.com/gitlab-org/cli/test"
)

func eq(t *testing.T, got any, expected any) {
	t.Helper()
	if !reflect.DeepEqual(got, expected) {
		t.Errorf("expected: %v, got: %v", expected, got)
	}
}

// setupTestHome creates a temporary home directory and sets up environment
// variables for cross-platform testing (HOME for Unix/macOS, USERPROFILE for Windows).
// It also reloads the XDG paths to pick up the new environment.
func setupTestHome(t *testing.T) string {
	t.Helper()
	test.ClearEnvironmentVariables(t)

	homeDir := t.TempDir()
	// Set both HOME (Unix/macOS) and USERPROFILE (Windows) for cross-platform compatibility
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	return homeDir
}

// setupXDGHome creates a temporary XDG config directory and sets XDG_CONFIG_HOME.
// It reloads XDG paths after setting the environment variable.
func setupXDGHome(t *testing.T) string {
	t.Helper()

	xdgConfigHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgConfigHome)
	xdg.Reload()

	return xdgConfigHome
}

func Test_parseConfig(t *testing.T) {
	test.ClearEnvironmentVariables(t)
	dir := t.TempDir()
	seedFile(t, dir, "config.yml", `---
hosts:
  gitlab.com:
    username: monalisa
    token: OTOKEN
aliases:
`)

	config, err := ParseConfig(filepath.Join(dir, "config.yml"))
	eq(t, err, nil)
	username, err := config.Get("gitlab.com", "username")
	eq(t, err, nil)
	eq(t, username, "monalisa")
	token, err := config.Get("gitlab.com", "token")
	eq(t, err, nil)
	eq(t, token, "OTOKEN")
}

func Test_parseConfig_multipleHosts(t *testing.T) {
	test.ClearEnvironmentVariables(t)
	dir := t.TempDir()
	seedFile(t, dir, "config.yml", `---
hosts:
  gitlab.example.com:
    username: wrongusername
    token: NOTTHIS
  gitlab.com:
    username: monalisa
    token: OTOKEN
`)

	config, err := ParseConfig(filepath.Join(dir, "config.yml"))
	eq(t, err, nil)
	username, err := config.Get("gitlab.com", "username")
	eq(t, err, nil)
	eq(t, username, "monalisa")
	token, err := config.Get("gitlab.com", "token")
	eq(t, err, nil)
	eq(t, token, "OTOKEN")
}

func Test_parseConfig_Hosts(t *testing.T) {
	test.ClearEnvironmentVariables(t)
	dir := t.TempDir()
	seedFile(t, dir, "config.yml", `---
hosts:
  gitlab.com:
    username: monalisa
    token: OTOKEN
`)

	config, err := ParseConfig(filepath.Join(dir, "config.yml"))
	eq(t, err, nil)
	username, err := config.Get("gitlab.com", "username")
	eq(t, err, nil)
	eq(t, username, "monalisa")
	token, err := config.Get("gitlab.com", "token")
	eq(t, err, nil)
	eq(t, token, "OTOKEN")
}

func Test_parseConfig_Local(t *testing.T) {
	test.ClearEnvironmentVariables(t)
	dir := t.TempDir()
	seedFile(t, dir, "config.yml", `---
git_protocol: ssh
editor: vim
local:
  git_protocol: https
  editor: nano
`)

	config, err := ParseConfig(filepath.Join(dir, "config.yml"))
	eq(t, err, nil)
	gitProtocol, err := config.Get("", "git_protocol")
	eq(t, err, nil)
	eq(t, gitProtocol, "https")
	// A program to run is never taken from the local section.
	editor, err := config.Get("", "editor")
	eq(t, err, nil)
	eq(t, editor, "vim")
}

func Test_Get_configReadSequence(t *testing.T) {
	test.ClearEnvironmentVariables(t)

	dir := t.TempDir()
	seedFile(t, dir, "config.yml", `---
git_protocol: ssh
editor: vim
browser: mozilla
local:
  git_protocol: https
  editor:
  browser: chrome
`)
	t.Setenv("BROWSER", "opera")

	config, err := ParseConfig(filepath.Join(dir, "config.yml"))
	eq(t, err, nil)
	gitProtocol, err := config.Get("", "git_protocol")
	eq(t, err, nil)
	eq(t, gitProtocol, "https")
	token, err := config.Get("", "editor")
	eq(t, err, nil)
	eq(t, token, "vim")
	browser, err := config.Get("", "browser")
	eq(t, err, nil)
	eq(t, browser, "opera")
	l, _ := config.Local()
	t.Log(l.All())
}

func Test_parseConfig_AliasesFile(t *testing.T) {
	dir := t.TempDir()
	seedFile(t, dir, "config.yml", "")
	seedFile(t, dir, "aliases.yml", `---
ci: pipeline ci
co: mr checkout
`)
	config, err := ParseConfig(filepath.Join(dir, "config.yml"))
	eq(t, err, nil)
	aliases, err := config.Aliases()
	eq(t, err, nil)
	a, isAlias := aliases.Get("ci")
	eq(t, isAlias, true)
	eq(t, a, "pipeline ci")
	b, isAlias := aliases.Get("co")
	eq(t, isAlias, true)
	eq(t, b, "mr checkout")
	eq(t, len(aliases.All()), 2)
}

func Test_parseConfig_hostFallback(t *testing.T) {
	dir := t.TempDir()
	seedFile(t, dir, "config.yml", `---
git_protocol: ssh
hosts:
  gitlab.com:
    username: monalisa
    token: OTOKEN
  gitlab.example.com:
    username: wrongusername
    token: NOTTHIS
    git_protocol: https
`)
	config, err := ParseConfig(filepath.Join(dir, "config.yml"))
	eq(t, err, nil)
	val, err := config.Get("gitlab.example.com", "git_protocol")
	eq(t, err, nil)
	eq(t, val, "https")
	val, err = config.Get("gitlab.com", "git_protocol")
	eq(t, err, nil)
	eq(t, val, "ssh")
	val, err = config.Get("nonexist.io", "git_protocol")
	eq(t, err, nil)
	eq(t, val, "ssh")
}

func Test_parseConfigFile(t *testing.T) {
	tests := []struct {
		contents string
		wantsErr bool
	}{
		{
			contents: "",
			wantsErr: true,
		},
		{
			contents: " ",
			wantsErr: false,
		},
		{
			contents: "\n",
			wantsErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("contents: %q", tt.contents), func(t *testing.T) {
			dir := t.TempDir()
			// Empty contents models a missing file (which must error), so only
			// write the file when there is actual content.
			if tt.contents != "" {
				seedFile(t, dir, "config.yml", tt.contents)
			}
			_, yamlRoot, err := ParseConfigFile(filepath.Join(dir, "config.yml"))
			if tt.wantsErr != (err != nil) {
				t.Fatalf("got error: %v", err)
			}
			if tt.wantsErr {
				return
			}
			assert.Equal(t, yaml.MappingNode, yamlRoot.Content[0].Kind)
			assert.Empty(t, yamlRoot.Content[0].Content)
		})
	}
}

func Test_ParseConfigFilePermissions(t *testing.T) {
	tests := map[string]struct {
		permissions int
		wantErr     bool
	}{
		"bad permissions": {
			permissions: 0o755,
			wantErr:     true,
		},
		"normal permissions": {
			permissions: 0o600,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tempDir := t.TempDir()
			configFile := filepath.Join(tempDir, "config.yml")

			err := os.WriteFile(
				configFile,
				[]byte("---\nhost: https://gitlab.mycompany.global"),
				os.FileMode(tt.permissions),
			)
			require.NoError(t, err)

			_, err = ParseConfig(configFile)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func Test_parseConfigHostEnv(t *testing.T) {
	t.Setenv("GITLAB_URI", "https://gitlab.mycompany.env")

	dir := t.TempDir()
	seedFile(t, dir, "config.yml", `---
host: https://gitlab.mycompany.global
local:
  host: https://gitlab.mycompany.local
`)
	config, err := ParseConfig(filepath.Join(dir, "config.yml"))
	eq(t, err, nil)

	val, err := config.Get("", "host")
	eq(t, err, nil)
	eq(t, val, "https://gitlab.mycompany.env")
}

func Test_parseConfigHostLocal(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	seedFile(t, dir, "config.yml", `---
host: https://gitlab.mycompany.global
local:
  host: https://gitlab.mycompany.local
`)
	config, err := ParseConfig(filepath.Join(dir, "config.yml"))
	eq(t, err, nil)

	val, err := config.Get("", "host")
	eq(t, err, nil)
	eq(t, val, "https://gitlab.mycompany.local")
}

func Test_parseConfigHostGlobal(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	seedFile(t, dir, "config.yml", `---
host: https://gitlab.mycompany.org
`)
	config, err := ParseConfig(filepath.Join(dir, "config.yml"))
	eq(t, err, nil)

	val, err := config.Get("", "host")
	eq(t, err, nil)
	eq(t, val, "https://gitlab.mycompany.org")
}

func Test_SearchConfigFile_UserConfig(t *testing.T) {
	_ = setupTestHome(t)

	// Create a temporary user config directory
	userConfigDir := t.TempDir()
	userConfigFile := filepath.Join(userConfigDir, "glab-cli", "config.yml")

	// Create config file
	err := os.MkdirAll(filepath.Dir(userConfigFile), 0o750)
	require.NoError(t, err)
	err = os.WriteFile(userConfigFile, []byte("test: user"), 0o600)
	require.NoError(t, err)

	// Set XDG_CONFIG_HOME to temp directory
	t.Setenv("XDG_CONFIG_HOME", userConfigDir)
	xdg.Reload()

	// SearchConfigFile should find the user config
	foundPath, err := SearchConfigFile()
	require.NoError(t, err)
	assert.Equal(t, userConfigFile, foundPath)
}

func Test_SearchConfigFile_SystemConfig(t *testing.T) {
	_ = setupTestHome(t)

	// Create temporary directories for system and user configs
	userConfigDir := t.TempDir()
	systemConfigDir := t.TempDir()
	systemConfigFile := filepath.Join(systemConfigDir, "glab-cli", "config.yml")

	// Create ONLY system config (no user config)
	err := os.MkdirAll(filepath.Dir(systemConfigFile), 0o750)
	require.NoError(t, err)
	err = os.WriteFile(systemConfigFile, []byte("test: system"), 0o600)
	require.NoError(t, err)

	// Set XDG environment variables
	t.Setenv("XDG_CONFIG_HOME", userConfigDir)
	t.Setenv("XDG_CONFIG_DIRS", systemConfigDir)
	xdg.Reload()

	// SearchConfigFile should find the system config
	foundPath, err := SearchConfigFile()
	require.NoError(t, err)
	assert.Equal(t, systemConfigFile, foundPath)
}

func Test_SearchConfigFile_Precedence(t *testing.T) {
	_ = setupTestHome(t)

	// Create temporary directories
	userConfigDir := t.TempDir()
	systemConfigDir := t.TempDir()
	userConfigFile := filepath.Join(userConfigDir, "glab-cli", "config.yml")
	systemConfigFile := filepath.Join(systemConfigDir, "glab-cli", "config.yml")

	// Create both user and system configs
	err := os.MkdirAll(filepath.Dir(userConfigFile), 0o750)
	require.NoError(t, err)
	err = os.WriteFile(userConfigFile, []byte("test: user"), 0o600)
	require.NoError(t, err)

	err = os.MkdirAll(filepath.Dir(systemConfigFile), 0o750)
	require.NoError(t, err)
	err = os.WriteFile(systemConfigFile, []byte("test: system"), 0o600)
	require.NoError(t, err)

	// Set XDG environment variables
	t.Setenv("XDG_CONFIG_HOME", userConfigDir)
	t.Setenv("XDG_CONFIG_DIRS", systemConfigDir)
	xdg.Reload()

	// SearchConfigFile should prefer user config over system config
	foundPath, err := SearchConfigFile()
	require.NoError(t, err)
	assert.Equal(t, userConfigFile, foundPath)
}

func Test_SearchConfigFile_NotFound(t *testing.T) {
	_ = setupTestHome(t)

	// Use empty temp directories (no config files)
	userConfigDir := t.TempDir()
	systemConfigDir := t.TempDir()

	t.Setenv("XDG_CONFIG_HOME", userConfigDir)
	t.Setenv("XDG_CONFIG_DIRS", systemConfigDir)
	xdg.Reload()

	// SearchConfigFile should return an error
	_, err := SearchConfigFile()
	require.Error(t, err)
}

func Test_SearchConfigFile_GLABConfigDirOverride(t *testing.T) {
	test.ClearEnvironmentVariables(t)

	// Create temp directories
	glabConfigDir := t.TempDir()
	userConfigDir := t.TempDir()
	glabConfigFile := filepath.Join(glabConfigDir, "config.yml")
	userConfigFile := filepath.Join(userConfigDir, "glab-cli", "config.yml")

	// Create both GLAB_CONFIG_DIR and user configs
	err := os.WriteFile(glabConfigFile, []byte("test: glab"), 0o600)
	require.NoError(t, err)

	err = os.MkdirAll(filepath.Dir(userConfigFile), 0o750)
	require.NoError(t, err)
	err = os.WriteFile(userConfigFile, []byte("test: user"), 0o600)
	require.NoError(t, err)

	// Set both environment variables
	t.Setenv("GLAB_CONFIG_DIR", glabConfigDir)
	t.Setenv("XDG_CONFIG_HOME", userConfigDir)

	// GLAB_CONFIG_DIR should take precedence
	foundPath, err := SearchConfigFile()
	require.NoError(t, err)
	assert.Equal(t, glabConfigFile, foundPath)
}

func Test_SearchConfigFile_GLABConfigDirNotFound(t *testing.T) {
	test.ClearEnvironmentVariables(t)

	// Use empty GLAB_CONFIG_DIR
	glabConfigDir := t.TempDir()
	t.Setenv("GLAB_CONFIG_DIR", glabConfigDir)

	// Should return os.ErrNotExist, not search XDG paths
	_, err := SearchConfigFile()
	require.ErrorIs(t, err, os.ErrNotExist)
}

func Test_ConfigFile_XDGCompliance(t *testing.T) {
	_ = setupTestHome(t)

	userConfigDir := setupXDGHome(t)

	configFile := ConfigFile()
	expectedPath := filepath.Join(userConfigDir, "glab-cli", "config.yml")

	assert.Equal(t, expectedPath, configFile)
}

func Test_ConfigDir_XDGCompliance(t *testing.T) {
	_ = setupTestHome(t)

	userConfigDir := setupXDGHome(t)

	configDir := ConfigDir()
	expectedPath := filepath.Join(userConfigDir, "glab-cli")

	assert.Equal(t, expectedPath, configDir)
}

func Test_ConfigFile_PureFunction(t *testing.T) {
	_ = setupTestHome(t)
	_ = setupXDGHome(t)

	configFile := ConfigFile()

	// Should always return a path without side effects
	assert.NotEmpty(t, configFile)
	assert.Contains(t, configFile, "glab-cli")
	assert.Contains(t, configFile, "config.yml")
	assert.True(t, filepath.IsAbs(configFile), "ConfigFile should return an absolute path")

	// The directory should NOT be created (pure function)
	configDir := filepath.Dir(configFile)
	_, err := os.Stat(configDir)
	assert.True(t, os.IsNotExist(err), "ConfigFile should not create directories")
}

func Test_ConfigDir_LegacyBackwardCompatibility(t *testing.T) {
	homeDir := setupTestHome(t)

	// Create legacy config directory and file
	legacyDir := filepath.Join(homeDir, ".config", "glab-cli")
	err := os.MkdirAll(legacyDir, 0o750)
	require.NoError(t, err)

	legacyConfigFile := filepath.Join(legacyDir, "config.yml")
	err = os.WriteFile(legacyConfigFile, []byte("test: legacy"), 0o600)
	require.NoError(t, err)

	// Even though XDG_CONFIG_HOME might point elsewhere, if legacy config exists,
	// ConfigDir should return the legacy location
	_ = setupXDGHome(t)

	configDir := ConfigDir()

	// Should use legacy location since the config file exists there
	assert.Equal(t, legacyDir, configDir)
}

func Test_ConfigDir_NewInstallation(t *testing.T) {
	_ = setupTestHome(t)

	// Set XDG_CONFIG_HOME to a new location (no legacy config exists)
	xdgConfigHome := setupXDGHome(t)

	configDir := ConfigDir()

	// Should use XDG location since no legacy config exists
	expectedDir := filepath.Join(xdgConfigHome, "glab-cli")
	assert.Equal(t, expectedDir, configDir)
}

func Test_SearchConfigFile_LegacyLocation(t *testing.T) {
	homeDir := setupTestHome(t)

	// Create legacy config
	legacyDir := filepath.Join(homeDir, ".config", "glab-cli")
	err := os.MkdirAll(legacyDir, 0o750)
	require.NoError(t, err)

	legacyConfigFile := filepath.Join(legacyDir, "config.yml")
	err = os.WriteFile(legacyConfigFile, []byte("test: legacy"), 0o600)
	require.NoError(t, err)

	// Set XDG_CONFIG_HOME to a different location
	_ = setupXDGHome(t)

	// SearchConfigFile should find the legacy config first
	foundPath, err := SearchConfigFile()
	require.NoError(t, err)
	assert.Equal(t, legacyConfigFile, foundPath)
}

func Test_checkForDuplicateConfigs_SymlinkSameFile(t *testing.T) {
	// This test simulates Fedora Silverblue where /home is a symlink to /var/home.
	// Both paths point to the same file, so no duplicate warning should be printed.
	test.ClearEnvironmentVariables(t)

	// Create a base directory structure: baseDir/var/home/testuser
	baseDir := t.TempDir()
	realHome := filepath.Join(baseDir, "var", "home", "testuser")
	err := os.MkdirAll(realHome, 0o750)
	require.NoError(t, err)

	// Create a symlink: baseDir/home -> baseDir/var/home
	symlinkHome := filepath.Join(baseDir, "home")
	err = os.Symlink(filepath.Join(baseDir, "var", "home"), symlinkHome)
	require.NoError(t, err)

	// Create config file via the real path
	realConfigDir := filepath.Join(realHome, ".config", "glab-cli")
	err = os.MkdirAll(realConfigDir, 0o750)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(realConfigDir, "config.yml"), []byte("test: value"), 0o600)
	require.NoError(t, err)

	// Set HOME to the real path (simulating what os.UserHomeDir returns on Silverblue)
	t.Setenv("HOME", realHome)
	t.Setenv("USERPROFILE", realHome)

	// Set XDG_CONFIG_HOME to the symlinked path (simulating XDG resolution)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(symlinkHome, "testuser", ".config"))
	xdg.Reload()

	// Both paths resolve to the same file, just accessed via different paths
	var buf bytes.Buffer
	checkForDuplicateConfigs(&buf)

	assert.NotContains(t, buf.String(), "Multiple config files found",
		"Should not warn about duplicates when paths are symlinks to the same file")
}

func Test_checkForDuplicateConfigs_RealDuplicates(t *testing.T) {
	// This test verifies that we still warn when there are genuinely different config files
	test.ClearEnvironmentVariables(t)

	homeDir := t.TempDir()
	xdgDir := t.TempDir()

	// Create legacy config
	legacyConfigDir := filepath.Join(homeDir, ".config", "glab-cli")
	err := os.MkdirAll(legacyConfigDir, 0o750)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(legacyConfigDir, "config.yml"), []byte("test: legacy"), 0o600)
	require.NoError(t, err)

	// Create XDG config (different file)
	xdgConfigDir := filepath.Join(xdgDir, "glab-cli")
	err = os.MkdirAll(xdgConfigDir, 0o750)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(xdgConfigDir, "config.yml"), []byte("test: xdg"), 0o600)
	require.NoError(t, err)

	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	t.Setenv("XDG_CONFIG_HOME", xdgDir)
	xdg.Reload()

	var buf bytes.Buffer
	checkForDuplicateConfigs(&buf)

	assert.Contains(t, buf.String(), "Multiple config files found",
		"Should warn about duplicates when config files are genuinely different")
}

// sharedConfigEnv points glab at a shared configuration directory outside the
// test HOME, with the user's own XDG directory inside it, which is how a real
// machine is laid out. It returns (sharedDir, userDir).
//
// xdg.Reload is undone on cleanup: t.Setenv restores the environment but not
// the paths adrg/xdg already parsed from it, so without this a later test in
// the same binary inherits these directories.
func sharedConfigEnv(t *testing.T) (string, string) {
	t.Helper()
	home := setupTestHome(t)

	sharedDir := t.TempDir() // outside HOME, like /etc/xdg
	userDir := filepath.Join(home, ".config-xdg")
	require.NoError(t, os.MkdirAll(userDir, 0o750))

	t.Setenv("GLAB_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", userDir)
	t.Setenv("XDG_CONFIG_DIRS", sharedDir)
	xdg.Reload()
	t.Cleanup(func() { xdg.Reload() })

	return sharedDir, userDir
}

// writeConfigAt writes contents to <dir>/glab-cli/<name> with exactly perm.
func writeConfigAt(t *testing.T, dir, name, contents string, perm os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, "glab-cli", name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(contents), perm))
	// WriteFile is subject to the umask, so set the mode explicitly.
	require.NoError(t, os.Chmod(path, perm))
	return path
}

func TestHasSafeSharedPerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not enforced on Windows")
	}

	assert.True(t, HasSafeSharedPerms(0o644))
	assert.True(t, HasSafeSharedPerms(0o600))
	assert.False(t, HasSafeSharedPerms(0o664))
	assert.False(t, HasSafeSharedPerms(0o666))
}

func TestParseConfigFile_Permissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not enforced on Windows")
	}

	// A shared file has to be readable by every user, so the read mode is not
	// checked. The write bits still are: a config can run a command through
	// custom_headers.valueFromCommand, so one another account can write is
	// code execution.
	for _, tc := range []struct {
		mode     os.FileMode
		accepted bool
	}{
		{0o644, true},
		{0o600, true},
		{0o664, false},
		{0o666, false},
		{0o646, false},
	} {
		t.Run(fmt.Sprintf("shared mode %o", tc.mode), func(t *testing.T) {
			shared, _ := sharedConfigEnv(t)
			path := writeConfigAt(t, shared, "config.yml", "editor: vim\n", tc.mode)

			_, root, err := ParseConfigFile(path)
			if tc.accepted {
				require.NoError(t, err)
				assert.NotNil(t, root)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), "not to be writable by group or others")
		})
	}

	t.Run("a credential in a world-readable shared file is read", func(t *testing.T) {
		shared, _ := sharedConfigEnv(t)
		path := writeConfigAt(t, shared, "config.yml", "hosts:\n  gitlab.com:\n    token: glpat-xxx\n", 0o644)

		_, root, err := ParseConfigFile(path)
		require.NoError(t, err)
		assert.NotNil(t, root)
	})

	t.Run("a shared aliases.yml does not fail the whole CLI", func(t *testing.T) {
		// parseConfig propagates a non-ErrNotExist error from aliases.yml, so
		// holding it to the user rule fails every glab command, not just aliases.
		shared, _ := sharedConfigEnv(t)
		writeConfigAt(t, shared, "config.yml", "editor: vim\n", 0o644)
		writeConfigAt(t, shared, "aliases.yml", "token: personal-access-token list\n", 0o644)

		_, err := ParseDefaultConfig()
		require.NoError(t, err)
	})

	t.Run("a shared config is readable with HOME unset", func(t *testing.T) {
		// Containers, systemd units and cron jobs scrub the environment, and
		// they are the setups that rely on a system-wide config.
		shared, _ := sharedConfigEnv(t)
		path := writeConfigAt(t, shared, "config.yml", "editor: vim\n", 0o644)
		t.Setenv("HOME", "")

		_, _, err := ParseConfigFile(path)
		require.NoError(t, err)
	})

	t.Run("GLAB_CONFIG_DIR pointing at a shared directory stays shared", func(t *testing.T) {
		// Pointing a fleet at a provisioned config must not reimpose 600.
		shared, _ := sharedConfigEnv(t)
		path := writeConfigAt(t, shared, "config.yml", "editor: vim\n", 0o644)
		t.Setenv("GLAB_CONFIG_DIR", filepath.Join(shared, "glab-cli"))

		_, _, err := ParseConfigFile(path)
		require.NoError(t, err)
	})

	t.Run("a symlinked shared directory is still shared", func(t *testing.T) {
		// filepath.Abs alone would compare the link unequal to its target and
		// fall back to the 600 rule.
		shared, _ := sharedConfigEnv(t)
		writeConfigAt(t, shared, "config.yml", "editor: vim\n", 0o644)

		link := filepath.Join(t.TempDir(), "link")
		require.NoError(t, os.Symlink(shared, link))

		_, _, err := ParseConfigFile(filepath.Join(link, "glab-cli", "config.yml"))
		require.NoError(t, err)
	})

	t.Run("a user config is still held to 600", func(t *testing.T) {
		_, user := sharedConfigEnv(t)
		path := writeConfigAt(t, user, "config.yml", "editor: vim\n", 0o644)

		_, _, err := ParseConfigFile(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "glab requires 600")
	})

	t.Run("an unreadable user config keeps the actionable error", func(t *testing.T) {
		// Statting the open handle instead would report "permission denied"
		// rather than the chmod advice.
		_, user := sharedConfigEnv(t)
		path := writeConfigAt(t, user, "config.yml", "editor: vim\n", 0o200)

		_, _, err := ParseConfigFile(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "chmod 600")
	})

	t.Run("the legacy directory is a user config even when listed in ConfigDirs", func(t *testing.T) {
		// On macOS adrg/xdg puts $HOME/.config in ConfigDirs, and that is glab's
		// own legacy user directory, so membership alone would drop the 600 rule
		// for exactly the file it protects.
		home := setupTestHome(t)
		legacy := filepath.Join(home, ".config")
		require.NoError(t, os.MkdirAll(legacy, 0o750))

		t.Setenv("GLAB_CONFIG_DIR", "")
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config-xdg"))
		t.Setenv("XDG_CONFIG_DIRS", legacy)
		xdg.Reload()
		t.Cleanup(func() { xdg.Reload() })

		require.Contains(t, xdg.ConfigDirs, legacy, "precondition: the legacy dir is in ConfigDirs")

		path := writeConfigAt(t, legacy, "config.yml", "editor: vim\n", 0o644)

		_, _, err := ParseConfigFile(path)
		require.Error(t, err, "a user's own config must keep the 600 rule")
		assert.Contains(t, err.Error(), "glab requires 600")
	})
}
