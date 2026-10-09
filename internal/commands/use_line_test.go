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

func TestUseIsSingleLine(t *testing.T) {
	ios, _, _, _ := cmdtest.TestIOStreams()
	factory := cmdutils.NewFactory(ios, false, config.NewBlankConfig(), api.BuildInfo{Version: "v1.0.0", Commit: "abcdefgh"})
	root := NewCmdRoot(factory)

	var multiline []string
	walkCommandTree(root, []string{}, func(cmd *cobra.Command, path []string) {
		if strings.Contains(cmd.Use, "\n") {
			multiline = append(multiline, "glab "+strings.Join(path, " "))
		}
	})

	assert.Empty(t, multiline, "Use must be a single line; help renders it as one usage line. Put alternate forms in Long or Example. Multi-line:\n  - %s", strings.Join(multiline, "\n  - "))
}
