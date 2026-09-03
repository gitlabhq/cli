package cilog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/fsx"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
)

type Session struct {
	StartedAt string `json:"startedAt"`
	Command   string `json:"command"`
}

type Log struct {
	SchemaVersion int             `json:"schemaVersion"`
	Session       Session         `json:"session"`
	Entries       []verdict.Entry `json:"entries"`

	seen map[string]struct{}
}

func Path(baseDir string) string {
	return filepath.Join(baseDir, ".gitlab", "df", "ci-log.json")
}

// LockPath is the sidecar advisory-lock file guarding the Load -> Append ->
// Save sequence against concurrent "glab df run" invocations in one job. It is
// kept separate from the log file so the lock never conflicts with Save's
// whole-file atomic rename, which replaces the log's inode.
func LockPath(baseDir string) string {
	return filepath.Join(baseDir, ".gitlab", "df", "ci-log.lock")
}

func New(command string) *Log {
	return &Log{
		SchemaVersion: 1,
		Session:       Session{StartedAt: time.Now().UTC().Format(time.RFC3339), Command: command},
		seen:          map[string]struct{}{},
	}
}

func Load(baseDir string) (*Log, error) {
	l := &Log{SchemaVersion: 1, seen: map[string]struct{}{}}
	raw, err := os.ReadFile(Path(baseDir))
	if os.IsNotExist(err) {
		return l, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, l); err != nil {
		return nil, err
	}
	l.seen = map[string]struct{}{}
	for _, e := range l.Entries {
		l.seen[e.Key()] = struct{}{}
	}
	return l, nil
}

func (l *Log) Append(e verdict.Entry) {
	if l.seen == nil {
		l.seen = map[string]struct{}{}
	}
	if _, ok := l.seen[e.Key()]; ok {
		return
	}
	if e.Timestamp == "" {
		e.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	l.seen[e.Key()] = struct{}{}
	l.Entries = append(l.Entries, e)
}

// Save writes the CI log atomically with owner-only (0o600) permissions.
// The log records which packages a shared CI runner requested and how the
// firewall ruled on each; ci-summary reads it back to set the job's exit
// code. A co-tenant who can rewrite the log could flip that gate, so the
// file must not be left group/world-writable even when an earlier run (or a
// pre-planted file) created it with a looser mode. WriteOwnerOnly enforces
// 0o600 on every rewrite; WriteJSONFile cannot, because renameio.WriteFile
// preserves a pre-existing target's mode.
func Save(baseDir string, l *Log) error {
	path := Path(baseDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(l, "", "  ") //nolint:forbidigo // serializing the CI log to disk, not stdout output
	if err != nil {
		return err
	}
	return fsx.WriteOwnerOnly(path, append(raw, '\n'))
}
