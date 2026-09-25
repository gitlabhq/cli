//go:build !integration

package commands

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// TestEveryRunnableCommandHasMCPAnnotation fails when any command the
// MCP server would consider for registration lacks an MCP annotation.
// Missing annotations silently drop the command from the MCP tool
// surface; this catches that drift. It checks presence, not
// correctness of choice.
func TestEveryRunnableCommandHasMCPAnnotation(t *testing.T) {
	ios, _, _, _ := cmdtest.TestIOStreams()
	factory := cmdutils.NewFactory(
		ios,
		false,
		config.NewBlankConfig(),
		api.BuildInfo{Version: "v1.0.0", Commit: "abcdefgh"},
	)
	root := NewCmdRoot(factory)

	var missing []string
	walkCommandTree(root, []string{}, func(cmd *cobra.Command, path []string) {
		// Mirrors registerToolsFromCommands: parents with a RunE (for
		// example, `mr note`) are registered too, and Run-only commands never are.
		if cmd.RunE == nil {
			return
		}
		if !mcpannotations.HasAnnotation(cmd.Annotations) {
			missing = append(missing, "glab "+strings.Join(path, " "))
		}
	})

	assert.Empty(t, missing,
		"every command with a RunE must declare an MCP annotation (Safe, Destructive, "+
			"Interactive, or Exclude). Missing:\n  - %s",
		strings.Join(missing, "\n  - "),
	)
}

// walkCommandTree visits every command below the root. Path excludes
// the binary name.
func walkCommandTree(cmd *cobra.Command, path []string, visit func(*cobra.Command, []string)) {
	if cmd.HasParent() {
		name := strings.Fields(cmd.Use)[0]
		path = append(path, name)
		visit(cmd, path)
	}
	for _, sub := range cmd.Commands() {
		walkCommandTree(sub, path, visit)
	}
}
