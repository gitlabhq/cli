package config

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	editCmd "gitlab.com/gitlab-org/cli/internal/commands/config/edit"
	getCmd "gitlab.com/gitlab-org/cli/internal/commands/config/get"
	setCmd "gitlab.com/gitlab-org/cli/internal/commands/config/set"
)

func NewCmdConfig(f cmdutils.Factory) *cobra.Command {
	var isGlobal bool

	configCmd := &cobra.Command{
		Use:   "config [flags]",
		Short: `Manage glab settings.`,
		Long: heredoc.Docf(`Manage key/value strings.

Current respected settings:

- browser: If unset, uses the default browser. Override with environment variable $BROWSER.
- check_update: If true, notifies of new versions of glab. Defaults to true. Override with environment variable $GLAB_CHECK_UPDATE.
- display_hyperlinks: If true, and using a TTY, outputs hyperlinks for issues and merge request lists. Defaults to false.
- editor: If unset, uses the default editor. Override with environment variable $EDITOR.
- glab_pager: Your desired pager command to use, such as 'less -R'.
- glamour_style: Your desired Markdown renderer style. Options are dark, light, notty. Custom styles are available using [glamour](https://github.com/charmbracelet/glamour#styles).
- host: If unset, defaults to %[1]shttps://gitlab.com%[1]s.
- token: Your GitLab access token. Defaults to environment variables.
- visual: Takes precedence over 'editor'. If unset, uses the default editor. Override with environment variable $VISUAL.
`, "`"),
		Aliases: []string{"conf"},
	}

	configCmd.Flags().BoolVarP(&isGlobal, "global", "g", false, "Use global config file. (default false)")

	configCmd.AddCommand(getCmd.NewCmdGet(f))
	configCmd.AddCommand(setCmd.NewCmdSet(f))
	configCmd.AddCommand(editCmd.NewCmdEdit(f))

	return configCmd
}
