//go:build !integration

package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKeySchema_CoversEveryKeyReadByTheCodebase(t *testing.T) {
	t.Parallel()

	walkSourceFiles(t, func(path string, fset *token.FileSet, file *ast.File) {
		imports := importNames(file)

		ast.Inspect(file, func(n ast.Node) bool {
			key, ok := configKeyRead(n, imports)
			if !ok {
				return true
			}
			if findKeyDef(ConfigKeyEquivalence(key)) == nil {
				t.Errorf("%s: config key %q is read here but has no KeyDef in KeySchema; add one to internal/config/schema.go",
					fset.Position(n.Pos()), key)
			}
			return true
		})
	})
}

// GLAB_* is glab's own namespace, so every variable in it that the CLI reads
// should be discoverable. Other prefixes are out of scope: CI_*, TERM, GOPATH
// and friends are probes of someone else's environment.
func TestGlabEnvVars_AreDeclaredOrDocumented(t *testing.T) {
	t.Parallel()

	allowed := schemaEnvVars()
	documented := documentedEnvVars(t)

	walkSourceFiles(t, func(path string, fset *token.FileSet, file *ast.File) {
		ast.Inspect(file, func(n ast.Node) bool {
			name, ok := envVarRead(n)
			if !ok || !strings.HasPrefix(name, "GLAB_") {
				return true
			}
			if _, inSchema := allowed[name]; inSchema {
				return true
			}
			if strings.Contains(documented, name) {
				return true
			}
			t.Errorf("%s: %s is read here but is neither a KeySchema env var nor documented in the help:environment annotation in internal/commands/root.go",
				fset.Position(n.Pos()), name)
			return true
		})
	})
}

func walkSourceFiles(t *testing.T, fn func(path string, fset *token.FileSet, file *ast.File)) {
	t.Helper()

	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join("..", "..", dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "vendor" || d.Name() == "testdata" {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}

			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			fn(path, fset, file)
			return nil
		})
		require.NoError(t, err)
	}
}

func schemaEnvVars() map[string]struct{} {
	out := map[string]struct{}{}
	for _, kd := range KeySchema {
		vars := kd.EnvVars
		if len(vars) == 0 {
			vars = []string{strings.ToUpper(kd.Name)}
		}
		for _, v := range vars {
			out[v] = struct{}{}
		}
	}
	return out
}

func documentedEnvVars(t *testing.T) string {
	t.Helper()

	path := filepath.Join("..", "commands", "root.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	require.NoError(t, err)

	var help strings.Builder
	ast.Inspect(file, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, ok := kv.Key.(*ast.BasicLit)
		if !ok || key.Kind != token.STRING {
			return true
		}
		if name, err := strconv.Unquote(key.Value); err != nil || name != "help:environment" {
			return true
		}
		ast.Inspect(kv.Value, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				help.WriteString(lit.Value)
			}
			return true
		})
		return false
	})

	require.NotEmpty(t, help.String(), "could not find the help:environment annotation in %s", path)
	return help.String()
}

func importNames(file *ast.File) map[string]struct{} {
	out := map[string]struct{}{}
	for _, imp := range file.Imports {
		name := ""
		if imp.Name != nil {
			name = imp.Name.Name
		} else if path, err := strconv.Unquote(imp.Path.Value); err == nil {
			name = path[strings.LastIndex(path, "/")+1:]
		}
		if name != "" {
			out[name] = struct{}{}
			out[strings.TrimPrefix(name, "go-")] = struct{}{}
		}
	}
	return out
}

func configKeyRead(n ast.Node, imports map[string]struct{}) (string, bool) {
	call, ok := n.(*ast.CallExpr)
	if !ok || len(call.Args) < 2 {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	if recv, ok := sel.X.(*ast.Ident); ok {
		if _, isPkg := imports[recv.Name]; isPkg {
			return "", false
		}
	}
	switch sel.Sel.Name {
	case "Get":
		if len(call.Args) != 2 {
			return "", false
		}
	case "GetWithSource":
		if len(call.Args) != 3 {
			return "", false
		}
	default:
		return "", false
	}
	return stringArg(call.Args[1])
}

func envVarRead(n ast.Node) (string, bool) {
	call, ok := n.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	switch sel.Sel.Name {
	case "Getenv", "LookupEnv", "IsEnvVarEnabled":
	default:
		return "", false
	}
	return stringArg(call.Args[0])
}

func stringArg(arg ast.Expr) (string, bool) {
	lit, ok := arg.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return v, true
}
