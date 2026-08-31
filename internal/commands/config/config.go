package config

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	editCmd "gitlab.com/gitlab-org/cli/internal/commands/config/edit"
	getCmd "gitlab.com/gitlab-org/cli/internal/commands/config/get"
	pathCmd "gitlab.com/gitlab-org/cli/internal/commands/config/path"
	setCmd "gitlab.com/gitlab-org/cli/internal/commands/config/set"
	"gitlab.com/gitlab-org/cli/internal/config/confighelp"
)

func NewCmdConfig(f cmdutils.Factory) *cobra.Command {
	var isGlobal bool

	configCmd := &cobra.Command{
		Use:   "config [flags]",
		Short: `Manage glab settings.`,
		Long: heredoc.Docf(`Manage key/value strings.

		Current respected settings:

		%s

		Configuration file locations follow the XDG Base Directory specification.
		For the full search order and platform-specific paths, see [configuration](https://gitlab.com/gitlab-org/cli#configuration).
		`, confighelp.Settings()),
		Aliases: []string{"conf"},
	}

	configCmd.Flags().BoolVarP(&isGlobal, "global", "g", false, "Use global config file.")

	configCmd.AddCommand(getCmd.NewCmdGet(f))
	configCmd.AddCommand(setCmd.NewCmdSet(f))
	configCmd.AddCommand(editCmd.NewCmdEdit(f))
	configCmd.AddCommand(pathCmd.NewCmd(f))

	return configCmd
}
