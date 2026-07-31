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

func Save(baseDir string, l *Log) error {
	return fsx.WriteJSONFile(Path(baseDir), l)
}
