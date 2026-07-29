//go:build !integration

package ci

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

var tests = []struct {
	name        string
	args        string
	expectedOut string
	expectedErr string
}{
	{
		name:        "when no args should display the help message",
		args:        "",
		expectedOut: "Use \"ci [command] --help\" for more information about a command.\n",
		expectedErr: "Aliases 'pipe' and 'pipeline' are deprecated. Use 'ci' instead.",
	},
}

func TestPipelineCmd(t *testing.T) {
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wantedErr := ""
			if len(test.expectedErr) > 0 {
				wantedErr = test.expectedErr
			}

			ios, _, stdout, stderr := cmdtest.TestIOStreams()
			f := cmdtest.NewTestFactory(ios)

			cmd := NewCmdCI(f)
			cmd.SetOut(stdout)
			cmd.SetErr(stderr)

			err := cmd.Execute()

			if assert.NoErrorf(t, err, "error running `ci %s`: %v", test.args, err) {
				assert.Contains(t, stderr.String(), wantedErr)
				assert.Contains(t, stdout.String(), test.expectedOut)
			}
		})
	}
}

func TestProductionUsesBuildStateConstants(t *testing.T) {
	t.Parallel()

	buildStates := map[string]struct{}{
		string(gitlab.Created): {}, string(gitlab.WaitingForResource): {}, string(gitlab.Preparing): {},
		string(gitlab.Pending): {}, string(gitlab.Running): {}, string(gitlab.Success): {},
		string(gitlab.Failed): {}, string(gitlab.Canceled): {}, string(gitlab.Skipped): {},
		string(gitlab.Manual): {}, string(gitlab.Scheduled): {},
	}

	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fileSet := token.NewFileSet()
		file, err := parser.ParseFile(fileSet, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				return true
			}
			if _, ok := buildStates[value]; ok {
				t.Errorf("%s: use the gitlab.BuildStateValue constant for %q", fileSet.Position(literal.Pos()), value)
			}
			return true
		})
		return nil
	})
	require.NoError(t, err)
}
