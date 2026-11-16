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
// GitLab, so an agent has no use for them.
var notMCPTools = []string{
	"check-update",
	"completion",
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
