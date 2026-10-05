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
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestNoArgsCommandsDisableFileCompletion(t *testing.T) {
	ios, _, _, _ := cmdtest.TestIOStreams()
	factory := cmdutils.NewFactory(ios, false, config.NewBlankConfig(), api.BuildInfo{Version: "v1.0.0", Commit: "abcdefgh"})
	root := NewCmdRoot(factory)

	var missing []string
	walkCommandTree(root, []string{}, func(cmd *cobra.Command, path []string) {
		if !cmd.Runnable() || cmd.Args == nil || cmd.ValidArgsFunction != nil {
			return
		}
		if cmd.Args(cmd, nil) == nil && cmd.Args(cmd, []string{"x"}) != nil {
			missing = append(missing, "glab "+strings.Join(path, " "))
		}
	})

	assert.Empty(t, missing, "commands that take no arguments must set ValidArgsFunction: cobra.NoFileCompletions. Missing:\n  - %s", strings.Join(missing, "\n  - "))
}
