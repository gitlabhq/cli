// Package policysnapshot reads the per-project snapshot of AI
// tool-call policies that GitLab compiles, which glab matches tool calls
// against without calling GitLab.
package policysnapshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SchemaVersion is the only GitLab snapshot schema this glab reads. Another
// schema could change what a field means, so it is refused rather than guessed.
const SchemaVersion = 2

type Snapshot struct {
	SchemaVersion int           `json:"schema_version"`
	GeneratedAt   string        `json:"generated_at"`
	Project       Project       `json:"project"`
	Policies      []Policy      `json:"policies"`
	SkippedRules  []SkippedRule `json:"skipped_rules"`
}

type Project struct {
	ID       int64  `json:"id"`
	FullPath string `json:"full_path"`
}

type Policy struct {
	ID      uint64 `json:"id"`
	Version int    `json:"version"`
	Name    string `json:"name"`
	Mode    string `json:"mode"`
	// Action is block, ask_user, or empty for a policy that does neither,
	// which glab ignores.
	Action  string `json:"action"`
	Message string `json:"message"`
	Rules   []Rule `json:"rules"`
}

// Rule holds the fields of every target; only those of its Target are set.
type Rule struct {
	RuleIndex       int      `json:"rule_index"`
	Target          string   `json:"target"`
	BlockedPatterns []string `json:"blocked_patterns"`
	Operations      []string `json:"operations"`
	PathPatterns    []string `json:"path_patterns"`
	ServerPatterns  []string `json:"server_patterns"`
	ToolPatterns    []string `json:"tool_patterns"`
	InputPatterns   []string `json:"input_patterns"`
}

// SkippedRule is a rule GitLab could not compile to patterns, such as a
// custom Rego rule, which glab therefore cannot enforce.
type SkippedRule struct {
	PolicyID  uint64 `json:"policy_id"`
	RuleIndex int    `json:"rule_index"`
	Type      string `json:"type"`
}

var ErrSchemaVersion = errors.New("policy snapshot has an unsupported schema version")

// Dir is the folder in stateDir, which callers fill with gaig.StateDir(),
// that holds the snapshots of every project.
func Dir(stateDir string) string {
	return filepath.Join(stateDir, "tool-call-snapshots")
}

// PathFor keys the snapshot by host and project path, because the per-call
// hook knows only the Git remote and must not call GitLab to learn the id.
// Both are lower-cased, because GitLab matches them without case, and the
// colon before a port becomes an underscore, which Windows allows in a
// file name. It refuses a host or path that would leave Dir, since both come
// from a remote URL that anyone can write.
func PathFor(stateDir, host, projectFullPath string) (string, error) {
	hostDir := strings.ToLower(strings.ReplaceAll(host, ":", "_"))
	project := filepath.FromSlash(strings.ToLower(projectFullPath))
	// Each part is checked on its own, because filepath.Join cleans the
	// joined path, so a project path that climbs out of its host could
	// still land in the folder of another host. A path that cleaning would
	// change is refused rather than cleaned, so it cannot name another
	// project on the same host.
	if !isSingleElement(hostDir) || !isCleanLocal(project) {
		return "", fmt.Errorf("invalid project %q on host %q", projectFullPath, host)
	}
	return filepath.Join(Dir(stateDir), hostDir, project+".json"), nil
}

func isSingleElement(name string) bool {
	return !strings.ContainsAny(name, `/\`) && isCleanLocal(name)
}

func isCleanLocal(name string) bool {
	return name != "." && filepath.IsLocal(name) && filepath.Clean(name) == name
}

func Parse(encoded []byte) (*Snapshot, error) {
	snapshot := &Snapshot{}
	if err := json.Unmarshal(encoded, snapshot); err != nil {
		return nil, fmt.Errorf("could not parse the policy snapshot: %w", err)
	}
	if snapshot.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("%w %d, this glab reads %d", ErrSchemaVersion, snapshot.SchemaVersion, SchemaVersion)
	}
	return snapshot, nil
}

// Read wraps the error from os.ReadFile, so a caller can tell a missing
// snapshot apart with errors.Is(err, fs.ErrNotExist).
func Read(path string) (*Snapshot, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read the policy snapshot: %w", err)
	}
	return Parse(encoded)
}

// OlderThan also reports true when GeneratedAt is missing or not RFC 3339,
// because then nothing shows that the snapshot is recent.
func (s *Snapshot) OlderThan(age time.Duration, now time.Time) bool {
	generated, err := time.Parse(time.RFC3339, s.GeneratedAt)
	return err != nil || now.Sub(generated) > age
}
