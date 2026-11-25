//go:build !integration

package policysnapshot

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const encodedSnapshot = `{"schema_version":2,"generated_at":"2026-11-11T05:01:06Z","project":{"id":2,"full_path":"group/project"},
"policies":[{"id":12,"version":3,"name":"No publishing","mode":"enforce","action":null,"message":"Blocked by policy 'No publishing'",
"rules":[{"rule_index":1,"target":"command","blocked_patterns":["npm\\s+publish"]},
{"rule_index":2,"target":"file","operations":["read"],"path_patterns":["(^|/)\\.env$"]}]}],
"skipped_rules":[{"policy_id":12,"rule_index":0,"type":"custom"}]}`

func pathFor(t *testing.T, stateDir string) string {
	t.Helper()
	path, err := PathFor(stateDir, "gitlab.example.com", "group/project")
	require.NoError(t, err)
	return path
}

func TestPathFor_NestsTheProjectPathUnderTheHost(t *testing.T) {
	t.Parallel()
	root := string(filepath.Separator) + "glab-cli"

	path, err := PathFor(root, "gitlab.example.com", "group/sub/project")

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "tool-call-snapshots", "gitlab.example.com", "group", "sub", "project.json"), path)
}

func TestPathFor_IgnoresCaseAndEncodesAPort(t *testing.T) {
	t.Parallel()
	root := string(filepath.Separator) + "glab-cli"

	path, err := PathFor(root, "GitLab.Example.com:8443", "Group/Sub/Project")

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "tool-call-snapshots", "gitlab.example.com_8443", "group", "sub", "project.json"), path)
	assert.Equal(t, filepath.Join(root, "tool-call-snapshots"), Dir(root))
}

func TestPathFor_RefusesAPathOutsideTheSnapshotDirectory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		host    string
		project string
	}{
		{name: "a project path that climbs out", host: "gitlab.example.com", project: "../../../etc/passwd"},
		{name: "a host with a path that climbs out", host: "gitlab.example.com/../..", project: "etc/passwd"},
		{name: "a host that climbs out", host: "..", project: "group/project"},
		{name: "a project path that climbs into another host", host: "a.example.com", project: "../gitlab.com/group/project"},
		{name: "a project path that climbs out from deeper down", host: "a.example.com", project: "group/../../gitlab.com/group/project"},
		{name: "an absolute project path", host: "a.example.com", project: "/gitlab.com/group/project"},
		{name: "a host that is the current folder", host: ".", project: "gitlab.com/group/project"},
		{name: "a host with a backslash", host: `a.example.com\gitlab.com`, project: "group/project"},
		{name: "a host with a slash", host: "a.example.com/gitlab.com", project: "group/project"},
		{name: "an empty host", host: "", project: "group/project"},
		{name: "an empty project path", host: "gitlab.example.com", project: ""},
		{name: "a project path that names its parent group", host: "gitlab.example.com", project: "group/project/.."},
		{name: "a project path with a trailing slash", host: "gitlab.example.com", project: "group/project/"},
		{name: "a project path with a doubled slash", host: "gitlab.example.com", project: "group//project"},
		{name: "a project path that is the current folder", host: "gitlab.example.com", project: "."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := PathFor(t.TempDir(), tt.host, tt.project)

			assert.ErrorContains(t, err, "invalid project")
		})
	}
}

func TestRead_ParsesTheServerResponse(t *testing.T) {
	t.Parallel()
	path := pathFor(t, t.TempDir())
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(encodedSnapshot), 0o600))

	snapshot, err := Read(path)

	require.NoError(t, err)
	require.Len(t, snapshot.Policies, 1)
	assert.Empty(t, snapshot.Policies[0].Action)
	require.Len(t, snapshot.Policies[0].Rules, 2)
	assert.Equal(t, []string{`npm\s+publish`}, snapshot.Policies[0].Rules[0].BlockedPatterns)
	assert.Equal(t, []string{"read"}, snapshot.Policies[0].Rules[1].Operations)
	assert.Equal(t, []string{`(^|/)\.env$`}, snapshot.Policies[0].Rules[1].PathPatterns)
	assert.Equal(t, []SkippedRule{{PolicyID: 12, RuleIndex: 0, Type: "custom"}}, snapshot.SkippedRules)
}

func TestParse_RejectsAnotherSchemaVersion(t *testing.T) {
	t.Parallel()

	_, err := Parse([]byte(`{"schema_version":3}`))

	require.ErrorIs(t, err, ErrSchemaVersion)
	assert.ErrorContains(t, err, "unsupported schema version 3, this glab reads 2")
}

func TestRead_FailsWithoutASnapshot(t *testing.T) {
	t.Parallel()

	_, err := Read(pathFor(t, t.TempDir()))

	require.ErrorIs(t, err, fs.ErrNotExist)
	assert.ErrorContains(t, err, "could not read the policy snapshot")
}

func TestOlderThan(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		generatedAt string
		want        bool
	}{
		{name: "generated an hour ago", generatedAt: "2026-11-18T11:00:00Z", want: false},
		{name: "generated two days ago", generatedAt: "2026-11-16T12:00:00Z", want: true},
		{name: "no generation time", generatedAt: "", want: true},
		{name: "a generation time that is not RFC 3339", generatedAt: "yesterday", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			snapshot := &Snapshot{GeneratedAt: tt.generatedAt}

			assert.Equal(t, tt.want, snapshot.OlderThan(24*time.Hour, now))
		})
	}
}
