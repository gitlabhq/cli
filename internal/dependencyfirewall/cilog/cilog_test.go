//go:build !integration

package cilog

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
)

func TestLoadMissingReturnsEmptyLog(t *testing.T) {
	t.Parallel()
	l, err := Load(t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, 1, l.SchemaVersion)
	assert.Empty(t, l.Entries)
}

func TestAppendDedupsByKeyAndSaveRoundTrips(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	l := New("npm install foo")
	l.Append(verdict.Entry{Package: "foo", Version: "1.0.0", Verdict: verdict.Blocked})
	l.Append(verdict.Entry{Package: "foo", Version: "1.0.0", Verdict: verdict.Blocked})
	l.Append(verdict.Entry{Package: "bar", Version: "2.0.0", Verdict: verdict.Warning})
	require.Len(t, l.Entries, 2)

	require.NoError(t, Save(dir, l))

	got, err := Load(dir)
	require.NoError(t, err)
	require.Len(t, got.Entries, 2)
	assert.Equal(t, "npm install foo", got.Session.Command)
}

func TestLoadMalformedJSONReturnsError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := Path(dir)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))

	l, err := Load(dir)
	require.Error(t, err)
	assert.Nil(t, l)
}

func TestSaveTightensPreExistingLooseLog(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes not enforced on Windows")
	}
	// A co-tenant on a shared runner could pre-plant a world-writable
	// ci-log.json; Save must rewrite it 0o600 so the summary gate cannot be
	// tampered with. This exercises the owner-only property end-to-end through
	// Save (not just fsx.WriteOwnerOnly in isolation).
	dir := t.TempDir()
	path := Path(dir)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0o666))

	require.NoError(t, Save(dir, New("npm install foo")))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestPath(t *testing.T) {
	t.Parallel()
	assert.Equal(t, filepath.Join("base", ".gitlab", "df", "ci-log.json"), Path("base"))
}
