//go:build !integration

package setup

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	governPackage = "gitlab.com/gitlab-org/cli/internal/commands/govern"
	setupPackage  = governPackage + "/setup"
)

// Setup is reached through its package, or through the govern package, which
// registers it under glab minder and the legacy glab govern. These checks
// don't see a command that runs glab itself, in process or as a subprocess.
func TestOnlyTheCommandTreeReachesSetup(t *testing.T) {
	root := moduleRoot(t)
	importers := importersOf(t, root, governPackage, setupPackage)

	assert.Equal(t, []string{"internal/commands/govern/govern.go"}, importers[setupPackage],
		"only the user may run glab minder setup, so no other command may call it")
	assert.ElementsMatch(t, []string{"internal/commands/minder/minder.go", "internal/commands/root.go"}, importers[governPackage],
		"only the command tree may register the governance commands")
}

// importersOf returns, for each of packages, the non-test Go files under root
// that import it, relative to root and with forward slashes.
func importersOf(t *testing.T, root string, packages ...string) map[string][]string {
	t.Helper()
	importers := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// The go tool ignores these too. In CI the module cache is under .go.
			name := d.Name()
			if path != root && (name == "vendor" || name == "testdata" || name == "node_modules" ||
				strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			for _, want := range packages {
				if p == want {
					rel, err := filepath.Rel(root, path)
					require.NoError(t, err)
					importers[want] = append(importers[want], filepath.ToSlash(rel))
				}
			}
		}
		return nil
	})
	require.NoError(t, err)
	return importers
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "go.mod not found")
		dir = parent
	}
}
