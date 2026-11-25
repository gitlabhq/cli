//go:build !integration

package toolcallmatcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/policysnapshot"
)

type policyVector struct {
	Command   string `json:"command"`
	Operation string `json:"operation"`
	Path      string `json:"path"`
	Server    string `json:"server"`
	Tool      string `json:"tool"`
	Input     string `json:"input"`
	Matches   bool   `json:"matches"`
}

type policyVectors struct {
	Target          string         `json:"target"`
	BlockedPatterns []string       `json:"blocked_patterns"`
	Operations      []string       `json:"operations"`
	PathPatterns    []string       `json:"path_patterns"`
	ServerPatterns  []string       `json:"server_patterns"`
	ToolPatterns    []string       `json:"tool_patterns"`
	InputPatterns   []string       `json:"input_patterns"`
	Vectors         []policyVector `json:"vectors"`
}

func (vectors policyVectors) rule() policysnapshot.Rule {
	return policysnapshot.Rule{
		Target:          vectors.Target,
		BlockedPatterns: vectors.BlockedPatterns,
		Operations:      vectors.Operations,
		PathPatterns:    vectors.PathPatterns,
		ServerPatterns:  vectors.ServerPatterns,
		ToolPatterns:    vectors.ToolPatterns,
		InputPatterns:   vectors.InputPatterns,
	}
}

func (vector policyVector) subject() string {
	switch {
	case vector.Command != "":
		return vector.Command
	case vector.Path != "":
		return vector.Operation + " " + vector.Path
	default:
		return vector.Server + " " + vector.Tool + " " + vector.Input
	}
}

func readJSON(tb testing.TB, path string, into any) {
	tb.Helper()
	encoded, err := os.ReadFile(path)
	require.NoError(tb, err)
	require.NoError(tb, json.Unmarshal(encoded, into))
}

func readVectors(tb testing.TB, path string) policyVectors {
	tb.Helper()
	var vectors policyVectors
	readJSON(tb, path, &vectors)
	if vectors.Target == "" {
		vectors.Target = TargetCommand
	}
	return vectors
}

func vectorPaths(tb testing.TB) []string {
	tb.Helper()
	paths, err := filepath.Glob("testdata/*policy_*_vectors.json")
	require.NoError(tb, err)
	require.NotEmpty(tb, paths)
	return paths
}

// needsNormalisation are the vectors that a call matches only once it is
// normalised. Command and path normalisation, in the next merge requests of
// this stack, cover them.
var needsNormalisation = map[string]struct{}{
	`policy_8_vectors.json: bash -c "npm publish"`:               {},
	"policy_8_vectors.json: env NODE_ENV=production npm publish": {},
	"file_policy_3_vectors.json: read ~/.ssh/id_rsa":             {},
}

func TestDecide_MatchesTheVectors(t *testing.T) {
	t.Parallel()

	for _, path := range vectorPaths(t) {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			vectors := readVectors(t, path)
			matcher, skipped := Compile(snapshotWith(policyWith("Vectors", "enforce", "block", "Blocked", vectors.rule())))
			require.Empty(t, skipped)

			for _, vector := range vectors.Vectors {
				key := name + ": " + vector.subject()
				if _, skip := needsNormalisation[key]; skip {
					continue
				}
				call := Call{
					Target:        vectors.Target,
					Commands:      []string{vector.Command},
					FileOperation: vector.Operation,
					FilePaths:     []string{vector.Path},
					MCPServer:     vector.Server,
					MCPTool:       vector.Tool,
					MCPInput:      vector.Input,
				}
				denied := matcher.Decide(call).Decision == DecisionDeny
				assert.Equal(t, vector.Matches, denied, key)
			}
		})
	}
}

func TestCleanPath_CleansAsGitLabDoes(t *testing.T) {
	t.Parallel()
	var cleaning struct {
		Cases []struct {
			Path    string `json:"path"`
			Cleaned string `json:"cleaned"`
		} `json:"cases"`
	}
	readJSON(t, "testdata/path_cleaning_cases.json", &cleaning)
	require.NotEmpty(t, cleaning.Cases)

	for _, pathCase := range cleaning.Cases {
		assert.Equal(t, pathCase.Cleaned, cleanPath(pathCase.Path), "cleaning %q", pathCase.Path)
	}
}
