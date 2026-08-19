package config

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	editCmd "gitlab.com/gitlab-org/cli/internal/commands/config/edit"
	getCmd "gitlab.com/gitlab-org/cli/internal/commands/config/get"
	pathCmd "gitlab.com/gitlab-org/cli/internal/commands/config/path"
	setCmd "gitlab.com/gitlab-org/cli/internal/commands/config/set"
)

func NewCmdConfig(f cmdutils.Factory) *cobra.Command {
	var isGlobal bool

	configCmd := &cobra.Command{
		Use:   "config [flags]",
		Short: `Manage glab settings.`,
		Long: heredoc.Docf(`Manage key/value strings.

		Current respected settings:

		- %[1]sbranch_prefix%[1]s: Prefix used by %[1]sglab stack%[1]s when naming generated branches. Defaults to the current user's username (from %[1]sos/user.Current%[1]s), falling back to %[1]sglab-stack%[1]s if unavailable.
		- %[1]sbrowser%[1]s: If unset, uses the default browser. Override with environment variable %[1]s$BROWSER%[1]s.
		- %[1]scheck_update%[1]s: If true, notifies of new versions of glab. Defaults to %[1]strue%[1]s. Override with environment variable %[1]s$GLAB_CHECK_UPDATE%[1]s.
		- %[1]sdisplay_hyperlinks%[1]s: If %[1]sfalse%[1]s, disables hyperlinks in terminal output. Defaults to %[1]strue%[1]s. Override with environment variable %[1]s$FORCE_HYPERLINKS%[1]s.
		- %[1]sduo_cli_auto_download%[1]s: If %[1]strue%[1]s, automatically downloads the Duo CLI binary without prompting.
		- %[1]sduo_cli_auto_run%[1]s: If %[1]strue%[1]s, automatically runs GitLab Duo CLI without prompting.
		- %[1]seditor%[1]s: If unset, uses the default editor. Override with environment variable %[1]s$EDITOR%[1]s.
		- %[1]sgit_protocol%[1]s: Protocol used for Git operations. Supported values: %[1]sssh%[1]s, %[1]shttps%[1]s. Defaults to %[1]sssh%[1]s.
		- %[1]sglab_pager%[1]s: Your desired pager command to use, such as %[1]sless -R%[1]s.
		- %[1]sglamour_style%[1]s: Your desired Markdown renderer style. Options are dark, light, notty. Custom styles are available using [glamour](https://github.com/charmbracelet/glamour#styles).
		- %[1]shost%[1]s: If unset, defaults to %[1]shttps://gitlab.com%[1]s.
		- %[1]sno_prompt%[1]s: If %[1]strue%[1]s, disables interactive prompts. Defaults to %[1]sfalse%[1]s. Override with environment variable %[1]s$NO_PROMPT%[1]s.
		- %[1]snotify_skill_updates%[1]s: If %[1]strue%[1]s, shows a notice when an installed agent skill has updates available. Defaults to %[1]strue%[1]s. Override with environment variable %[1]s$GLAB_NOTIFY_SKILL_UPDATES%[1]s.
		- %[1]sorbit_local_auto_download%[1]s: If %[1]strue%[1]s, automatically downloads the Orbit local CLI binary without prompting.
		- %[1]sorbit_local_auto_run%[1]s: If %[1]strue%[1]s, automatically runs Orbit local CLI without prompting.
		- %[1]sremote_alias%[1]s: Name of the %[1]sgit remote%[1]s that points at the GitLab repository. Used to resolve which remote to operate against when multiple are configured.
		- %[1]sshow_whats_new%[1]s: If true, shows a one-time post-upgrade banner pointing at %[1]sglab whatsnew%[1]s when a new version is detected. Defaults to %[1]strue%[1]s. Override with environment variable %[1]s$GLAB_SHOW_WHATS_NEW%[1]s.
		- %[1]stelemetry%[1]s: If %[1]sfalse%[1]s, disables sending usage data to your GitLab instance. Defaults to %[1]strue%[1]s. Override with environment variable %[1]s$GLAB_SEND_TELEMETRY%[1]s.
		- %[1]stoken%[1]s: Your GitLab access token. Defaults to environment variables.
		- %[1]svisual%[1]s: Takes precedence over %[1]seditor%[1]s. If unset, uses the default editor. Override with environment variable %[1]s$VISUAL%[1]s.

		Configuration file locations follow the XDG Base Directory specification.
		For the full search order and platform-specific paths, see [configuration](https://gitlab.com/gitlab-org/cli#configuration).
		`, "`"),
		Aliases: []string{"conf"},
	}

	configCmd.Flags().BoolVarP(&isGlobal, "global", "g", false, "Use global config file.")

	configCmd.AddCommand(getCmd.NewCmdGet(f))
	configCmd.AddCommand(setCmd.NewCmdSet(f))
	configCmd.AddCommand(editCmd.NewCmdEdit(f))
	configCmd.AddCommand(pathCmd.NewCmd(f))

	return configCmd
}
