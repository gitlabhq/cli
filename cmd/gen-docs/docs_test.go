package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedRaw(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func seedGenerated(t *testing.T, path string) {
	t.Helper()
	seedRaw(t, path, "---\ntitle: stale\n---\n\n<!--\n"+generatedMarker+"\n-->\n\nStale content.\n")
}

// TestGenWebDocsPrunesAndDocuments exercises the full generate flow: available
// commands are documented, deprecated and hidden commands are excluded, orphaned
// generated pages are pruned (with their empty directories), and non-generated
// content is preserved.
func TestGenWebDocsPrunesAndDocuments(t *testing.T) {
	root := &cobra.Command{Use: "glab"}
	normal := &cobra.Command{Use: "normal", Short: "Normal command", Run: func(*cobra.Command, []string) {}}
	parent := &cobra.Command{Use: "parent", Short: "Parent command"}
	child := &cobra.Command{Use: "child", Short: "Child command", Run: func(*cobra.Command, []string) {}}
	deprecated := &cobra.Command{
		Use:        "deprecated",
		Short:      "Deprecated command",
		Deprecated: "use 'glab parent child' instead.",
		Run:        func(*cobra.Command, []string) {},
	}
	secret := &cobra.Command{Use: "secret", Short: "Secret command", Hidden: true, Run: func(*cobra.Command, []string) {}}
	parent.AddCommand(child, deprecated, secret)
	root.AddCommand(normal, parent)

	dir := t.TempDir()
	// Pre-seed: an orphaned generated page, an orphaned page that is the only
	// file in its directory, a hand-maintained file, and an image.
	seedGenerated(t, filepath.Join(dir, "parent", "removed.md"))
	seedGenerated(t, filepath.Join(dir, "gone", "gone.md"))
	seedRaw(t, filepath.Join(dir, "keepme.md"), "Hand-maintained, no marker.\n")
	seedRaw(t, filepath.Join(dir, "img", "logo.png"), "PNG")

	require.NoError(t, genWebDocs(root, dir))

	// Documented pages exist.
	assert.FileExists(t, filepath.Join(dir, "_index.md"))
	assert.FileExists(t, filepath.Join(dir, "commands.md"))
	assert.FileExists(t, filepath.Join(dir, "normal", "_index.md"))
	assert.FileExists(t, filepath.Join(dir, "parent", "_index.md"))
	assert.FileExists(t, filepath.Join(dir, "parent", "child.md"))

	// Deprecated command is not documented.
	assert.NoFileExists(t, filepath.Join(dir, "parent", "deprecated.md"))

	// Hidden command is not documented.
	assert.NoFileExists(t, filepath.Join(dir, "parent", "secret.md"))

	// Orphaned generated page is pruned; its emptied directory is removed.
	assert.NoFileExists(t, filepath.Join(dir, "parent", "removed.md"))
	assert.NoDirExists(t, filepath.Join(dir, "gone"))

	// Non-generated content is preserved.
	assert.FileExists(t, filepath.Join(dir, "keepme.md"))
	assert.FileExists(t, filepath.Join(dir, "img", "logo.png"))
}

// TestGenCommandsPage checks that the commands landing page lists the available
// top-level commands, excludes hidden and deprecated ones, and carries the
// generated marker so it is rewritten on every run.
func TestGenCommandsPage(t *testing.T) {
	root := &cobra.Command{Use: "glab"}
	normal := &cobra.Command{Use: "normal", Short: "Normal command", Run: func(*cobra.Command, []string) {}}
	deprecated := &cobra.Command{Use: "deprecated", Short: "d", Deprecated: "use 'glab normal' instead.", Run: func(*cobra.Command, []string) {}}
	secret := &cobra.Command{Use: "secret", Short: "s", Hidden: true, Run: func(*cobra.Command, []string) {}}
	root.AddCommand(normal, deprecated, secret)

	dir := t.TempDir()
	require.NoError(t, genCommandsPage(root, dir))

	page, err := os.ReadFile(filepath.Join(dir, "commands.md"))
	require.NoError(t, err)
	assert.Contains(t, string(page), generatedMarker)
	assert.Contains(t, string(page), "- [`glab normal`](normal/_index.md)\n")
	assert.NotContains(t, string(page), "glab deprecated")
	assert.NotContains(t, string(page), "glab secret")
}

// TestGenRootCommandDocsPointsAtCommandsPage checks that the root page links to
// the commands landing page instead of repeating the list.
func TestGenRootCommandDocsPointsAtCommandsPage(t *testing.T) {
	root := &cobra.Command{Use: "glab"}
	root.AddCommand(&cobra.Command{Use: "normal", Short: "Normal command", Run: func(*cobra.Command, []string) {}})

	dir := t.TempDir()
	require.NoError(t, genRootCommandDocs(root, dir))

	page, err := os.ReadFile(filepath.Join(dir, "_index.md"))
	require.NoError(t, err)
	assert.Contains(t, string(page), "For the list of top-level `glab` commands, see [Commands](commands.md).")
	assert.NotContains(t, string(page), "- [`glab normal`](normal/_index.md)")
}

func TestGenNavExcludesHiddenAndDeprecated(t *testing.T) {
	root := &cobra.Command{Use: "glab"}
	parent := &cobra.Command{Use: "parent", Short: "Parent command"}
	normal := &cobra.Command{Use: "normal", Short: "n", Run: func(*cobra.Command, []string) {}}
	deprecated := &cobra.Command{Use: "deprecated", Short: "d", Deprecated: "use x.", Run: func(*cobra.Command, []string) {}}
	secret := &cobra.Command{Use: "secret", Short: "s", Hidden: true, Run: func(*cobra.Command, []string) {}}
	parent.AddCommand(normal, deprecated, secret)
	root.AddCommand(parent)

	navPath := filepath.Join(t.TempDir(), "nav.yaml")
	require.NoError(t, genNav(root, navPath))

	nav, err := os.ReadFile(navPath)
	require.NoError(t, err)
	assert.Contains(t, string(nav), "glab parent normal")
	assert.NotContains(t, string(nav), "glab parent deprecated")
	assert.NotContains(t, string(nav), "glab parent secret")
}
