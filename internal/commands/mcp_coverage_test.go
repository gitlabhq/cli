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

// TestEveryRunnableCommandHasMCPAnnotation fails when a runnable command
// lacks an MCP annotation, so every command keeps a read/write
// classification. It checks presence, not correctness of choice.
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
		// Parents with a RunE (for example, `mr note`) count too.
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
