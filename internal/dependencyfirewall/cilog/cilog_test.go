//go:build !integration

package cilog

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
)

func TestLoadMissingReturnsEmptyLog(t *testing.T) {
	l, err := Load(t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, 1, l.SchemaVersion)
	assert.Empty(t, l.Entries)
}

func TestAppendDedupsByKeyAndSaveRoundTrips(t *testing.T) {
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
	dir := t.TempDir()
	path := Path(dir)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))

	l, err := Load(dir)
	require.Error(t, err)
	assert.Nil(t, l)
}

func TestPath(t *testing.T) {
	assert.Equal(t, filepath.Join("base", ".gitlab", "df", "ci-log.json"), Path("base"))
}
