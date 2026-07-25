//go:build !integration

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedFile(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
}

// ParseConfig reads the aliases file from the same directory as the main config
// file, not from a global path.
func Test_ParseConfig_ReadsAliasesFromInstanceDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	seedFile(t, dir, "config.yml", "git_protocol: ssh\n")
	seedFile(t, dir, "aliases.yml", "zzcustom: mr checkout --zz\n")

	cfg, err := ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)

	aliases, err := cfg.Aliases()
	require.NoError(t, err)
	got, ok := aliases.Get("zzcustom")
	assert.True(t, ok, "alias from the instance dir must be loaded")
	assert.Equal(t, "mr checkout --zz", got)
}

// The local config file is merged from an explicit path (production passes the
// git-based path; tests pass a temp file), so the merge is testable without a
// real git repository or process-global CWD state.
func Test_parseConfig_MergesLocalFromExplicitPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	seedFile(t, dir, "config.yml", "git_protocol: ssh\neditor: vim\n")
	seedFile(t, dir, "local.yml", "editor: nano\n")

	cfg, err := parseConfig(filepath.Join(dir, "config.yml"), filepath.Join(dir, "local.yml"))
	require.NoError(t, err)

	// local overrides global for non-host keys (searchENVVars=false keeps this
	// deterministic and parallel-safe regardless of the ambient $EDITOR).
	editor, _, err := cfg.GetWithSource("", "editor", false)
	require.NoError(t, err)
	assert.Equal(t, "nano", editor)

	// keys absent from local fall back to global
	proto, _, err := cfg.GetWithSource("", "git_protocol", false)
	require.NoError(t, err)
	assert.Equal(t, "ssh", proto)
}

// Public ParseConfig does not read a separate (git-based) local config file;
// callers that want local merging pass the path explicitly via parseConfig.
func Test_ParseConfig_DoesNotReadSeparateLocalFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	seedFile(t, dir, "config.yml", "editor: vim\n")

	cfg, err := ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)

	local, err := cfg.Local()
	require.NoError(t, err)
	assert.Empty(t, local.All(), "ParseConfig must not merge a separate local file")
}

// A local config that exists but cannot be parsed must not be swallowed. If it
// is, glab silently runs on the global config only and the user believes their
// per-repository overrides are in effect. The aliases file directly below this
// merge already reports parse errors this way.
func Test_parseConfig_ReportsBrokenLocalConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	seedFile(t, dir, "config.yml", "git_protocol: ssh\neditor: vim\n")
	seedFile(t, dir, "local.yml", "editor: [unclosed\n")

	_, err := parseConfig(filepath.Join(dir, "config.yml"), filepath.Join(dir, "local.yml"))

	require.Error(t, err)
}

// A missing local config is normal: most repositories have no per-repository
// overrides, so it must stay silent.
func Test_parseConfig_AllowsMissingLocalConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	seedFile(t, dir, "config.yml", "editor: vim\n")

	cfg, err := parseConfig(filepath.Join(dir, "config.yml"), filepath.Join(dir, "does-not-exist.yml"))

	require.NoError(t, err)
	editor, _, err := cfg.GetWithSource("", "editor", false)
	require.NoError(t, err)
	assert.Equal(t, "vim", editor)
}
