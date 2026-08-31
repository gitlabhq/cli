//go:build !integration

package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
	"go.yaml.in/yaml/v3"
)

// persistedFile returns the contents of a config file written into dir by a
// dir-backed Config, for tests that assert on what was persisted.
func persistedFile(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	require.NoError(t, err)
	return string(data)
}

func Test_BlankConfig_RoundTripsAndExposesSchemaKeys(t *testing.T) {
	root := NewBlankRoot()
	cfg := NewConfig(root)

	// The blank config must marshal to valid YAML that parses back to an
	// equivalent tree.
	out, err := yaml.Marshal(root)
	require.NoError(t, err)
	reparsed, err := parseConfigData(out)
	require.NoError(t, err)
	roundTripped, err := yaml.Marshal(reparsed)
	require.NoError(t, err)
	assert.Equal(t, string(out), string(roundTripped))

	// Hosts and aliases sections must be reachable.
	hosts, err := cfg.Hosts()
	require.NoError(t, err)
	assert.NotEmpty(t, hosts, "blank config should seed at least one host")

	aliases, err := cfg.Aliases()
	require.NoError(t, err)
	assert.NotEmpty(t, aliases.All(), "blank config should seed default aliases")

	// Defaults declared in KeySchema must surface through Get.
	gitProto, err := cfg.Get("", "git_protocol")
	require.NoError(t, err)
	assert.Equal(t, "ssh", gitProto)
}

func Test_fileConfig_Set(t *testing.T) {
	dir := t.TempDir()
	seedFile(t, dir, "config.yml", `---
git_protocol: ssh
editor: vim
hosts:
  gitlab.com:
    token:
    git_protocol: https
    username: user
`)

	c, err := ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)

	assert.NoError(t, c.Set("", "editor", "nano"))
	assert.NoError(t, c.Set("gitlab.com", "git_protocol", "ssh"))
	assert.NoError(t, c.Set("example.com", "username", "testUser"))
	assert.NoError(t, c.Set("gitlab.com", "username", "hubot"))
	assert.NoError(t, c.WriteAll())

	expected := heredoc.Doc(`
git_protocol: ssh
editor: nano
hosts:
    gitlab.com:
        token:
        git_protocol: ssh
        username: hubot
    example.com:
        username: testUser
`)
	assert.Equal(t, expected, persistedFile(t, dir, "config.yml"))
}

func Test_fileConfig_Set_WritesAliasToCanonicalKey(t *testing.T) {
	dir := t.TempDir()
	seedFile(t, dir, "config.yml", `---
editor: vim
`)

	c, err := ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)

	require.NoError(t, c.Set("", "visual", "nano"))
	require.NoError(t, c.WriteAll())

	assert.Equal(t, "editor: nano\n", persistedFile(t, dir, "config.yml"))
}

func Test_fileConfig_Set_Empty_Removes(t *testing.T) {
	dir := t.TempDir()
	seedFile(t, dir, "config.yml", `---
git_protocol: ssh
editor: vim
hosts:
  gitlab.com:
    token: foobar
    git_protocol: https
    username: user
`)

	c, err := ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)

	assert.NoError(t, c.Set("", "editor", ""))
	assert.NoError(t, c.Set("gitlab.com", "token", ""))
	assert.NoError(t, c.WriteAll())

	expected := heredoc.Doc(`
git_protocol: ssh
hosts:
    gitlab.com:
        git_protocol: https
        username: user
`)
	assert.Equal(t, expected, persistedFile(t, dir, "config.yml"))
}

func Test_defaultConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := NewBlankConfigInDir(dir)
	require.NoError(t, cfg.Write())
	_, statErr := os.Stat(filepath.Join(dir, "aliases.yml"))
	assert.True(t, os.IsNotExist(statErr), "Write() must not persist the aliases file")

	proto, err := cfg.Get("", "git_protocol")
	require.NoError(t, err)
	assert.Equal(t, "ssh", proto)

	editor, err := cfg.Get("", "editor")
	require.NoError(t, err)
	assert.Equal(t, os.Getenv("EDITOR"), editor)

	aliases, err := cfg.Aliases()
	require.NoError(t, err)
	assert.Len(t, aliases.All(), 2)
	expansion, _ := aliases.Get("co")
	assert.Equal(t, "mr checkout", expansion)
}

func Test_getFromKeyring(t *testing.T) {
	c := NewBlankConfig()

	// Ensure host exists and its token is empty
	err := c.Set("gitlab.com", "token", "")
	require.NoError(t, err)
	err = c.Write()
	require.NoError(t, err)

	keyring.MockInit()
	token, _, err := c.GetWithSource("gitlab.com", "token", false)
	require.NoError(t, err)
	assert.Empty(t, token)

	err = keyring.Set("glab:gitlab.com", "", "glpat-1234")
	require.NoError(t, err)

	token, _, err = c.GetWithSource("gitlab.com", "token", false)

	require.NoError(t, err)
	assert.Equal(t, "glpat-1234", token)
}

func Test_KeyringAvailable(t *testing.T) {
	t.Run("returns true when a keyring backend works", func(t *testing.T) {
		keyring.MockInit()
		assert.True(t, KeyringAvailable())
	})

	t.Run("returns false when the keyring backend errors", func(t *testing.T) {
		keyring.MockInitWithError(errors.New("no keyring"))
		t.Cleanup(keyring.MockInit)
		assert.False(t, KeyringAvailable())
	})
}

func Test_Set_SwitchingKeyringToFileRemovesStaleEntry(t *testing.T) {
	// Clear CI vars so the file-mode keyring cleanup is exercised (it is skipped
	// in CI, and the suite itself may run there).
	t.Setenv("GITLAB_CI", "")
	t.Setenv("CI", "")
	keyring.MockInit()

	c := NewBlankConfig()

	// Start in keyring mode with a token stored in the keyring.
	require.NoError(t, c.Set("gitlab.com", "use_keyring", "true"))
	require.NoError(t, c.Set("gitlab.com", "token", "glpat-keyring"))
	got, err := keyring.Get("glab:gitlab.com:token", "")
	require.NoError(t, err)
	require.Equal(t, "glpat-keyring", got)

	// Switch to file storage and write a new token.
	require.NoError(t, c.Set("gitlab.com", "use_keyring", "false"))
	require.NoError(t, c.Set("gitlab.com", "token", "glpat-file"))

	// The stale keyring entry must be gone (no orphaned secret).
	_, err = keyring.Get("glab:gitlab.com:token", "")
	require.Error(t, err)

	// The token now resolves from the file.
	v, _ := c.Get("gitlab.com", "token")
	assert.Equal(t, "glpat-file", v)
}

func Test_Set_ClearRemovesKeyringEntry(t *testing.T) {
	t.Setenv("GITLAB_CI", "")
	t.Setenv("CI", "")
	keyring.MockInit()

	c := NewBlankConfig()
	require.NoError(t, c.Set("gitlab.com", "use_keyring", "true"))
	require.NoError(t, c.Set("gitlab.com", "token", "glpat-x"))

	// Clearing the value deletes the keyring entry.
	require.NoError(t, c.Set("gitlab.com", "token", ""))
	_, err := keyring.Get("glab:gitlab.com:token", "")
	require.Error(t, err)
}

func Test_InCI(t *testing.T) {
	t.Run("false or unparseable values", func(t *testing.T) {
		// strconv.ParseBool rejects "yes"/"on"/"2", which we treat as not-CI.
		for _, v := range []string{"", "false", "0", "FALSE", "f", "yes", "on", "2"} {
			t.Setenv("GITLAB_CI", "")
			t.Setenv("CI", v)
			assert.Falsef(t, InCI(), "CI=%q should not count as CI", v)
		}
	})

	t.Run("truthy values", func(t *testing.T) {
		for _, v := range []string{"true", "True", "TRUE", "1", "t", "T"} {
			t.Setenv("GITLAB_CI", "")
			t.Setenv("CI", v)
			assert.Truef(t, InCI(), "CI=%q should count as CI", v)
		}
	})

	t.Run("GITLAB_CI is honored independently", func(t *testing.T) {
		t.Setenv("CI", "")
		t.Setenv("GITLAB_CI", "true")
		assert.True(t, InCI())
	})
}

func Test_SetStringValue_ResetsNullTagOnBlankEntry(t *testing.T) {
	// A key written with no value parses as a null-tagged scalar.
	var root yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte("token:\n"), &root))
	cm := ConfigMap{Root: root.Content[0]}

	require.NoError(t, cm.SetStringValue("token", "secret-value"))

	out, err := yaml.Marshal(root.Content[0])
	require.NoError(t, err)
	// The value must not be written as a null-tagged scalar (which would leave
	// it in the file yet decode as empty).
	assert.NotContains(t, string(out), "!!null")
	assert.Contains(t, string(out), "secret-value")

	// It decodes as a proper string after a round-trip through disk.
	var reloaded yaml.Node
	require.NoError(t, yaml.Unmarshal(out, &reloaded))
	var s string
	require.NoError(t, reloaded.Content[0].Content[1].Decode(&s))
	assert.Equal(t, "secret-value", s)
}

func Test_GetWithSource_SurfacesKeyringReadError(t *testing.T) {
	keyring.MockInitWithError(errors.New("access denied"))
	t.Cleanup(keyring.MockInit)

	c := NewBlankConfig()
	require.NoError(t, c.Set("gitlab.com", "use_keyring", "true"))

	_, _, err := c.GetWithSource("gitlab.com", "token", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "keyring")
}

func Test_Set_SurfacesKeyringWriteError(t *testing.T) {
	keyring.MockInitWithError(errors.New("exit status 161"))
	t.Cleanup(keyring.MockInit)

	c := NewBlankConfig()
	require.NoError(t, c.Set("gitlab.com", "use_keyring", "true"))

	err := c.Set("gitlab.com", "token", "secret-value")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "keyring", "error must name the keyring as the failing subsystem")
	assert.Contains(t, err.Error(), "gitlab.com", "error must name the host")
	assert.Contains(t, err.Error(), "token", "error must name the key")
	assert.Contains(t, err.Error(), "exit status 161", "underlying cause must be preserved")
	assert.NotContains(t, err.Error(), "secret-value", "credential must not be echoed into the error")
}

func Test_GetWithSource_KeyringNotFoundIsNotAnError(t *testing.T) {
	keyring.MockInit()

	c := NewBlankConfig()
	require.NoError(t, c.Set("gitlab.com", "use_keyring", "true"))

	val, _, err := c.GetWithSource("gitlab.com", "token", false)
	require.NoError(t, err)
	assert.Empty(t, val)
}

func Test_config_Get_NotFoundError(t *testing.T) {
	cfg := NewBlankConfig()

	local, err := cfg.Local()
	require.NoError(t, err)
	require.NotNil(t, local)

	_, err = local.FindEntry("git_protocol")
	require.Error(t, err)
	assert.True(t, isNotFoundError(err))
}

func TestCustomHeader_ResolvedValue_MissingEnvVar(t *testing.T) {
	// Ensure the environment variable doesn't exist
	os.Unsetenv("NONEXISTENT_VAR")

	header := CustomHeader{
		Name:         "X-Test-Header",
		ValueFromEnv: "NONEXISTENT_VAR",
	}

	value, err := header.ResolvedValue()
	require.Error(t, err)
	require.Empty(t, value)
	require.Contains(t, err.Error(), "environment variable \"NONEXISTENT_VAR\" for header \"X-Test-Header\" is not set or empty")
}

func TestCustomHeader_ResolvedValue_EmptyEnvVar(t *testing.T) {
	// Set environment variable to empty string
	t.Setenv("EMPTY_VAR", "")

	header := CustomHeader{
		Name:         "X-Test-Header",
		ValueFromEnv: "EMPTY_VAR",
	}

	value, err := header.ResolvedValue()
	require.Error(t, err)
	require.Empty(t, value)
	require.Contains(t, err.Error(), "environment variable \"EMPTY_VAR\" for header \"X-Test-Header\" is not set or empty")
}

func TestResolveCustomHeaders_MissingEnvVar(t *testing.T) {
	// Ensure the environment variable doesn't exist
	os.Unsetenv("MISSING_SECRET")

	configYAML := `
hosts:
  gitlab.com:
    custom_headers:
      - name: Cf-Access-Client-Secret
        valueFromEnv: MISSING_SECRET
`

	cfg := NewFromString(configYAML)
	headers, err := ResolveCustomHeaders(cfg, "gitlab.com")

	require.Error(t, err)
	require.Nil(t, headers)
	require.Contains(t, err.Error(), "failed to resolve header \"Cf-Access-Client-Secret\"")
	require.Contains(t, err.Error(), "environment variable \"MISSING_SECRET\" for header \"Cf-Access-Client-Secret\" is not set or empty")
}

func TestCustomHeader_ResolvedValue_Command(t *testing.T) {
	t.Setenv("GLAB_CUSTOM_HEADER_HELPER", "1")
	t.Setenv("GLAB_CUSTOM_HEADER_HELPER_OUTPUT", "  Bearer generated-token\n")

	header := CustomHeader{
		Name:             "Proxy-Authorization",
		ValueFromCommand: customHeaderHelperCommand(),
	}

	value, err := header.ResolvedValue()
	require.NoError(t, err)
	assert.Equal(t, "Bearer generated-token", value)
}

func TestCustomHeader_ResolvedValue_CommandErrorIncludesStderr(t *testing.T) {
	t.Setenv("GLAB_CUSTOM_HEADER_HELPER", "1")
	t.Setenv("GLAB_CUSTOM_HEADER_HELPER_ERROR", "no active credentials")

	header := CustomHeader{
		Name:             "Proxy-Authorization",
		ValueFromCommand: customHeaderHelperCommand(),
	}

	value, err := header.ResolvedValue()
	require.Error(t, err)
	assert.Empty(t, value)
	assert.Contains(t, err.Error(), "no active credentials")
}

func TestCustomHeader_ResolvedValue_CommandTimesOut(t *testing.T) {
	t.Setenv("GLAB_CUSTOM_HEADER_HELPER", "1")
	t.Setenv("GLAB_CUSTOM_HEADER_HELPER_SLEEP", "1s")

	value, err := resolveCustomHeaderCommandWithTimeout(customHeaderHelperCommand(), 10*time.Millisecond)
	require.Error(t, err)
	assert.Empty(t, value)
	assert.Contains(t, err.Error(), "command timed out after 10ms")
}

func TestCustomHeader_ResolvedValue_CommandTimeoutKillsGrandchild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh -c wrapping is a Unix-specific pattern")
	}

	marker := filepath.Join(t.TempDir(), "ran-to-completion")
	t.Setenv("GLAB_CUSTOM_HEADER_HELPER", "1")
	t.Setenv("GLAB_CUSTOM_HEADER_HELPER_SLEEP", "500ms")
	t.Setenv("GLAB_CUSTOM_HEADER_HELPER_MARKER_AFTER_SLEEP", marker)

	// Mirrors the README's documented pattern for pipelines: the process
	// glab spawns directly is `sh`, and the real work happens in a child
	// of that shell.
	command := "sh -c " + strconv.Quote(customHeaderHelperCommand())

	value, err := resolveCustomHeaderCommandWithTimeout(command, 50*time.Millisecond)
	require.Error(t, err)
	assert.Empty(t, value)
	assert.Contains(t, err.Error(), "command timed out after 50ms")

	// Give the grandchild long enough to have finished its sleep and
	// written the marker, if it survived the timeout.
	time.Sleep(1 * time.Second)
	_, err = os.Stat(marker)
	assert.True(t, os.IsNotExist(err), "grandchild process should have been killed along with its parent shell")
}

func TestResolveCustomHeaders_CachesCommandValue(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "calls")
	t.Setenv("GLAB_CUSTOM_HEADER_HELPER", "1")
	t.Setenv("GLAB_CUSTOM_HEADER_HELPER_OUTPUT", "Bearer generated-token")
	t.Setenv("GLAB_CUSTOM_HEADER_HELPER_MARKER", marker)

	cfg := NewFromString(fmt.Sprintf(`
hosts:
  gitlab.com:
    custom_headers:
      - name: Proxy-Authorization
        valueFromCommand: %s
`, strconv.Quote(customHeaderHelperCommand())))

	first, err := ResolveCustomHeaders(cfg, "gitlab.com")
	require.NoError(t, err)
	second, err := ResolveCustomHeaders(cfg, "gitlab.com")
	require.NoError(t, err)

	assert.Equal(t, "Bearer generated-token", first["Proxy-Authorization"])
	assert.Equal(t, first, second)
	calls, err := os.ReadFile(marker)
	require.NoError(t, err)
	assert.Equal(t, "x", string(calls), "the configured command should run once")
}

func TestGetCustomHeaders_RequiresExactlyOneValueSource(t *testing.T) {
	tests := []struct {
		name   string
		fields string
	}{
		{name: "no value source", fields: ""},
		{name: "value and environment", fields: "        value: direct\n        valueFromEnv: TOKEN"},
		{name: "environment and command", fields: "        valueFromEnv: TOKEN\n        valueFromCommand: token-helper"},
		{name: "value and command", fields: "        value: direct\n        valueFromCommand: token-helper"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := NewFromString("hosts:\n  gitlab.com:\n    custom_headers:\n      - name: X-Test\n" + tc.fields + "\n")

			_, err := ResolveCustomHeaders(cfg, "gitlab.com")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "exactly one of 'value', 'valueFromEnv', or 'valueFromCommand'")
		})
	}
}

func customHeaderHelperCommand() string {
	return strconv.Quote(os.Args[0]) + " -test.run=^TestCustomHeaderCommandHelper$"
}

func TestCustomHeaderCommandHelper(t *testing.T) {
	if os.Getenv("GLAB_CUSTOM_HEADER_HELPER") != "1" {
		return
	}

	if marker := os.Getenv("GLAB_CUSTOM_HEADER_HELPER_MARKER"); marker != "" {
		file, err := os.OpenFile(marker, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			os.Exit(2)
		}
		if _, err := file.WriteString("x"); err != nil {
			os.Exit(2)
		}
		if err := file.Close(); err != nil {
			os.Exit(2)
		}
	}
	if message := os.Getenv("GLAB_CUSTOM_HEADER_HELPER_ERROR"); message != "" {
		if _, err := fmt.Fprint(os.Stderr, message); err != nil {
			os.Exit(2)
		}
		os.Exit(1)
	}
	if delay := os.Getenv("GLAB_CUSTOM_HEADER_HELPER_SLEEP"); delay != "" {
		duration, err := time.ParseDuration(delay)
		if err != nil {
			os.Exit(2)
		}
		time.Sleep(duration)
	}

	if marker := os.Getenv("GLAB_CUSTOM_HEADER_HELPER_MARKER_AFTER_SLEEP"); marker != "" {
		if err := os.WriteFile(marker, []byte("x"), 0o600); err != nil {
			os.Exit(2)
		}
	}

	if _, err := fmt.Fprint(os.Stdout, os.Getenv("GLAB_CUSTOM_HEADER_HELPER_OUTPUT")); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestConfig_parseHosts_NoHosts(t *testing.T) {
	t.Parallel()

	cfg := &fileConfig{}
	// Create empty hosts node
	emptyHostsNode := &yaml.Node{Kind: yaml.MappingNode}

	_, err := cfg.parseHosts(emptyHostsNode)

	assert.True(t, isNotFoundError(err))
}

func TestConfig_parseHosts_YAMLAnchor(t *testing.T) {
	t.Parallel()

	configYAML := heredoc.Doc(`
		hosts:
		  gitlab.com: &gl
		    token: glpat-secret
		    user: testuser
		  gitlab.com:443: *gl
	`)

	var root yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(configYAML), &root))

	cfg := &fileConfig{ConfigMap: ConfigMap{Root: root.Content[0]}}

	// The anchored host must resolve its values.
	anchoredToken, err := cfg.Get("gitlab.com", "token")
	require.NoError(t, err)
	assert.Equal(t, "glpat-secret", anchoredToken)

	anchoredUser, err := cfg.Get("gitlab.com", "user")
	require.NoError(t, err)
	assert.Equal(t, "testuser", anchoredUser)

	// The aliased host must resolve the same values as the anchored one.
	aliasedToken, err := cfg.Get("gitlab.com:443", "token")
	require.NoError(t, err)
	assert.Equal(t, anchoredToken, aliasedToken)
}

func Test_SetKeyring_StoresTokenInKeyringAndSetsIndicator(t *testing.T) {
	dir := t.TempDir()
	keyring.MockInit()
	cfg := NewBlankConfigInDir(dir)

	// Enable keyring mode
	err := cfg.Set("gitlab.com", "use_keyring", "true")
	require.NoError(t, err)

	// Set a token - should go to keyring
	err = cfg.Set("gitlab.com", "token", "glpat-secret-token")
	require.NoError(t, err)

	// Verify token is stored in keyring with new key format
	storedToken, err := keyring.Get("glab:gitlab.com:token", "")
	require.NoError(t, err)
	assert.Equal(t, "glpat-secret-token", storedToken)

	// Verify use_keyring indicator is set in config
	useKeyring, err := cfg.Get("gitlab.com", "use_keyring")
	require.NoError(t, err)
	assert.Equal(t, "true", useKeyring)

	// Verify token is NOT in config (removed/empty)
	err = cfg.Write()
	require.NoError(t, err)
	configContent := persistedFile(t, dir, "config.yml")
	assert.NotContains(t, configContent, "glpat-secret-token", "Token should not be in plaintext config")
	assert.Contains(t, configContent, "use_keyring: true")
}

func Test_SetKeyring_OAuth2RefreshToken(t *testing.T) {
	dir := t.TempDir()
	keyring.MockInit()
	cfg := NewBlankConfigInDir(dir)

	// Enable keyring mode
	err := cfg.Set("gitlab.com", "use_keyring", "true")
	require.NoError(t, err)

	// Set a refresh token - should go to keyring
	err = cfg.Set("gitlab.com", "oauth2_refresh_token", "refresh-secret-token")
	require.NoError(t, err)

	// Verify refresh token is stored in keyring with new key format
	storedToken, err := keyring.Get("glab:gitlab.com:oauth2_refresh_token", "")
	require.NoError(t, err)
	assert.Equal(t, "refresh-secret-token", storedToken)

	// Verify use_keyring indicator is set in config
	useKeyring, err := cfg.Get("gitlab.com", "use_keyring")
	require.NoError(t, err)
	assert.Equal(t, "true", useKeyring)

	// Verify refresh token is NOT in config
	err = cfg.Write()
	require.NoError(t, err)
	configContent := persistedFile(t, dir, "config.yml")
	assert.NotContains(t, configContent, "refresh-secret-token", "Refresh token should not be in plaintext config")
}

func Test_GetWithSource_RetrievesFromKeyringWhenUseKeyringSet(t *testing.T) {
	dir := t.TempDir()
	seedFile(t, dir, "config.yml", heredoc.Doc(`
		---
		hosts:
		  gitlab.com:
		    use_keyring: "true"
		    is_oauth2: true
	`))

	keyring.MockInit()

	// Store token in keyring with new key format
	err := keyring.Set("glab:gitlab.com:token", "", "glpat-from-keyring")
	require.NoError(t, err)

	// Store refresh token in keyring with new key format
	err = keyring.Set("glab:gitlab.com:oauth2_refresh_token", "", "refresh-from-keyring")
	require.NoError(t, err)

	cfg, err := ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)

	// Retrieve token - should come from keyring, not config
	token, source, err := cfg.GetWithSource("gitlab.com", "token", false)
	require.NoError(t, err)
	assert.Equal(t, "glpat-from-keyring", token)
	assert.Equal(t, "keyring", source)

	// Retrieve refresh token - should come from keyring
	refreshToken, source, err := cfg.GetWithSource("gitlab.com", "oauth2_refresh_token", false)
	require.NoError(t, err)
	assert.Equal(t, "refresh-from-keyring", refreshToken)
	assert.Equal(t, "keyring", source)
}

func Test_GetWithSource_KeyringEnabledButTokenMissingReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	seedFile(t, dir, "config.yml", `---
hosts:
  gitlab.com:
    use_keyring: "true"
`)

	keyring.MockInit()
	// Don't store any token in keyring

	cfg, err := ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)

	// A genuinely missing keyring entry is not an error: it is treated like an
	// unset value so callers can fall back to re-authentication rather than
	// failing with a confusing keyring error. (Real keyring failures, such as a
	// denied or locked keyring, are surfaced instead — see
	// Test_GetWithSource_SurfacesKeyringReadError.)
	token, _, err := cfg.GetWithSource("gitlab.com", "token", false)
	require.NoError(t, err)
	assert.Empty(t, token)
}

func Test_SetKeyring_JobToken(t *testing.T) {
	keyring.MockInit()
	cfg := NewBlankConfig()

	// Enable keyring mode
	err := cfg.Set("gitlab.com", "use_keyring", "true")
	require.NoError(t, err)

	// Set a job token - should go to keyring
	err = cfg.Set("gitlab.com", "job_token", "job-token-value")
	require.NoError(t, err)

	// Verify job token is stored in keyring with new key format
	storedToken, err := keyring.Get("glab:gitlab.com:job_token", "")
	require.NoError(t, err)
	assert.Equal(t, "job-token-value", storedToken)

	// Verify use_keyring indicator is set
	useKeyring, err := cfg.Get("gitlab.com", "use_keyring")
	require.NoError(t, err)
	assert.Equal(t, "true", useKeyring)
}

func Test_SetKeyring_CleansUpExistingPlaintextToken(t *testing.T) {
	dir := t.TempDir()
	seedFile(t, dir, "config.yml", `---
hosts:
  gitlab.com:
    token: glpat-old-plaintext-token
`)

	keyring.MockInit()
	cfg, err := ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)

	// Enable keyring mode
	err = cfg.Set("gitlab.com", "use_keyring", "true")
	require.NoError(t, err)

	// Set token - should go to keyring and remove plaintext token from config
	err = cfg.Set("gitlab.com", "token", "glpat-new-keyring-token")
	require.NoError(t, err)

	err = cfg.Write()
	require.NoError(t, err)

	// Verify old plaintext token is removed from config
	configContent := persistedFile(t, dir, "config.yml")
	assert.NotContains(t, configContent, "glpat-old-plaintext-token")
	assert.NotContains(t, configContent, "glpat-new-keyring-token")
	assert.Contains(t, configContent, "use_keyring: \"true\"")

	// Verify new token is in keyring with new key format
	storedToken, err := keyring.Get("glab:gitlab.com:token", "")
	require.NoError(t, err)
	assert.Equal(t, "glpat-new-keyring-token", storedToken)
}

func Test_extractSubfolderFromURL(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{
			name:     "URL with subfolder",
			url:      "https://example.com/gitlab",
			expected: "gitlab",
		},
		{
			name:     "URL with nested subfolder",
			url:      "https://example.com/tools/gitlab",
			expected: "tools/gitlab",
		},
		{
			name:     "URL without subfolder",
			url:      "https://example.com",
			expected: "",
		},
		{
			name:     "URL with only slash",
			url:      "https://example.com/",
			expected: "",
		},
		{
			name:     "URL with trailing slash",
			url:      "https://example.com/gitlab/",
			expected: "gitlab",
		},
		{
			name:     "URL with port and subfolder",
			url:      "https://example.com:3000/gitlab",
			expected: "gitlab",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractSubfolderFromURL(tt.url)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func Test_GetFromEnvWithSource_CI_Subfolder(t *testing.T) {
	t.Run("subfolder from GITLAB_SUBFOLDER", func(t *testing.T) {
		t.Setenv("GITLAB_SUBFOLDER", "mysubfolder")
		t.Setenv("GLAB_ENABLE_CI_AUTOLOGIN", "")
		t.Setenv("GITLAB_CI", "")

		value, source := GetFromEnvWithSource("subfolder")
		assert.Equal(t, "mysubfolder", value)
		assert.Equal(t, "GITLAB_SUBFOLDER", source)
	})

	t.Run("subfolder from CI_SERVER_URL with path", func(t *testing.T) {
		t.Setenv("GITLAB_SUBFOLDER", "")
		t.Setenv("CI_SERVER_URL", "https://example.com/gitlab")
		t.Setenv("GLAB_ENABLE_CI_AUTOLOGIN", "true")
		t.Setenv("GITLAB_CI", "true")

		value, source := GetFromEnvWithSource("subfolder")
		assert.Equal(t, "gitlab", value)
		assert.Equal(t, "CI_SERVER_URL", source)
	})

	t.Run("subfolder from CI_SERVER_URL without path", func(t *testing.T) {
		t.Setenv("GITLAB_SUBFOLDER", "")
		t.Setenv("CI_SERVER_URL", "https://example.com")
		t.Setenv("GLAB_ENABLE_CI_AUTOLOGIN", "true")
		t.Setenv("GITLAB_CI", "true")

		value, source := GetFromEnvWithSource("subfolder")
		assert.Empty(t, value)
		assert.Empty(t, source)
	})

	t.Run("GITLAB_SUBFOLDER takes precedence over CI_SERVER_URL", func(t *testing.T) {
		t.Setenv("GITLAB_SUBFOLDER", "explicit")
		t.Setenv("CI_SERVER_URL", "https://example.com/gitlab")
		t.Setenv("GLAB_ENABLE_CI_AUTOLOGIN", "true")
		t.Setenv("GITLAB_CI", "true")

		value, source := GetFromEnvWithSource("subfolder")
		assert.Equal(t, "explicit", value)
		assert.Equal(t, "GITLAB_SUBFOLDER", source)
	})
}

func Test_GetFromEnvWithSource_CI_SSHHost(t *testing.T) {
	t.Run("ssh_host from GITLAB_SSH_HOST", func(t *testing.T) {
		t.Setenv("GITLAB_SSH_HOST", "ssh.example.com")
		t.Setenv("GLAB_ENABLE_CI_AUTOLOGIN", "")
		t.Setenv("GITLAB_CI", "")

		value, source := GetFromEnvWithSource("ssh_host")
		assert.Equal(t, "ssh.example.com", value)
		assert.Equal(t, "GITLAB_SSH_HOST", source)
	})

	t.Run("ssh_host from CI_SERVER_SHELL_SSH_HOST without port", func(t *testing.T) {
		t.Setenv("GITLAB_SSH_HOST", "")
		t.Setenv("CI_SERVER_SHELL_SSH_HOST", "git.example.com")
		t.Setenv("CI_SERVER_SHELL_SSH_PORT", "")
		t.Setenv("GLAB_ENABLE_CI_AUTOLOGIN", "true")
		t.Setenv("GITLAB_CI", "true")

		value, source := GetFromEnvWithSource("ssh_host")
		assert.Equal(t, "git.example.com", value)
		assert.Equal(t, "CI_SERVER_SHELL_SSH_HOST", source)
	})

	t.Run("ssh_host from CI_SERVER_SHELL_SSH_HOST with custom port", func(t *testing.T) {
		t.Setenv("GITLAB_SSH_HOST", "")
		t.Setenv("CI_SERVER_SHELL_SSH_HOST", "git.example.com")
		t.Setenv("CI_SERVER_SHELL_SSH_PORT", "2222")
		t.Setenv("GLAB_ENABLE_CI_AUTOLOGIN", "true")
		t.Setenv("GITLAB_CI", "true")

		value, source := GetFromEnvWithSource("ssh_host")
		assert.Equal(t, "git.example.com:2222", value)
		assert.Equal(t, "CI_SERVER_SHELL_SSH_HOST", source)
	})

	t.Run("ssh_host from CI_SERVER_SHELL_SSH_HOST with default port 22", func(t *testing.T) {
		t.Setenv("GITLAB_SSH_HOST", "")
		t.Setenv("CI_SERVER_SHELL_SSH_HOST", "git.example.com")
		t.Setenv("CI_SERVER_SHELL_SSH_PORT", "22")
		t.Setenv("GLAB_ENABLE_CI_AUTOLOGIN", "true")
		t.Setenv("GITLAB_CI", "true")

		value, source := GetFromEnvWithSource("ssh_host")
		assert.Equal(t, "git.example.com", value)
		assert.Equal(t, "CI_SERVER_SHELL_SSH_HOST", source)
	})

	t.Run("GITLAB_SSH_HOST takes precedence over CI_SERVER_SHELL_SSH_HOST", func(t *testing.T) {
		t.Setenv("GITLAB_SSH_HOST", "ssh.example.com:3000")
		t.Setenv("CI_SERVER_SHELL_SSH_HOST", "git.example.com")
		t.Setenv("CI_SERVER_SHELL_SSH_PORT", "2222")
		t.Setenv("GLAB_ENABLE_CI_AUTOLOGIN", "true")
		t.Setenv("GITLAB_CI", "true")

		value, source := GetFromEnvWithSource("ssh_host")
		assert.Equal(t, "ssh.example.com:3000", value)
		assert.Equal(t, "GITLAB_SSH_HOST", source)
	})
}
