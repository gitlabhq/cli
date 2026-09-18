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

// notMCPTools are commands that manage the local CLI rather than act on
// GitLab. An agent has no use for them, and "mcp serve" in particular would
// offer an MCP server a tool for starting an MCP server.
//
// The MCP server lives under this package, so its own tests cannot build the
// real command tree without an import cycle. This asserts on the annotations
// that drive registration instead.
var notMCPTools = []string{
	"check-update",
	"completion",
	"mcp serve",
	"version",
	"whatsnew",
}

func TestCommandsExcludedFromMCP(t *testing.T) {
	ios, _, _, _ := cmdtest.TestIOStreams()
	rootCmd := NewCmdRoot(cmdutils.NewFactory(ios, false, config.NewBlankConfig(), api.BuildInfo{}))

	for _, path := range notMCPTools {
		t.Run(path, func(t *testing.T) {
			cmd := findCommand(t, rootCmd, path)
			assert.Equal(t, "true", cmd.Annotations[mcpannotations.Exclude],
				"%q must stay out of the MCP tool catalog", path)
		})
	}
}

func findCommand(t *testing.T, root *cobra.Command, path string) *cobra.Command {
	t.Helper()

	cmd := root
	for name := range strings.FieldsSeq(path) {
		found := false
		for _, sub := range cmd.Commands() {
			if sub.Name() == name {
				cmd, found = sub, true
				break
			}
		}
		if !found {
			t.Fatalf("command %q not found while resolving %q", name, path)
		}
	}
	return cmd
}
