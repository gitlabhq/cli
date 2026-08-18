package duo

import (
	"fmt"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	duoAskCmd "gitlab.com/gitlab-org/cli/internal/commands/duo/ask"
	duoCLICmd "gitlab.com/gitlab-org/cli/internal/commands/duo/cli"
)

func NewCmd(f cmdutils.Factory) *cobra.Command {
	cliCmd := duoCLICmd.NewCmd(f)

	duoCmd := &cobra.Command{
		Use:   "duo [command]",
		Short: "Work with GitLab Duo.",
		Long: heredoc.Docf(`
			Use the GitLab Duo Agent Platform in your terminal. Ask GitLab Duo questions about your codebase and use it to autonomously perform actions on your behalf.

			%[1]sglab duo cli%[1]s installs and runs the GitLab Duo CLI (%[1]sduo%[1]s) binary. %[1]sglab%[1]s handles authentication, so you sign in only once with %[1]sglab auth login%[1]s.

			The GitLab Duo CLI requires GitLab 19.2 or later, or GitLab 18.11 to 19.1 with beta and experimental features turned on. For all prerequisites and usage, see %[1]sglab duo cli --help%[1]s.
		`, "`"),
		Example: heredoc.Doc(`
			glab duo cli --install
			glab duo cli
			glab duo cli run --goal "Fix the failing tests in this project"`),
		// DisableFlagParsing so the redirect message below can reproduce the
		// user's full invocation — FParseErrWhitelist would strip unknown
		// flags before RunE sees them.
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, a := range args {
				if a == "--help" || a == "-h" {
					return cmd.Help()
				}
			}
			if len(args) == 0 {
				return cmd.Help()
			}
			return fmt.Errorf(
				"unknown command %q for %q.\n\n"+
					"The GitLab Duo CLI runs as %q. Try %q, or run %q for the full command reference",
				args[0], cmd.CommandPath(),
				"glab duo cli",
				"glab duo cli "+strings.Join(args, " "),
				"glab duo cli --help",
			)
		},
	}

	duoCmd.AddCommand(duoAskCmd.NewCmdAsk(f))
	duoCmd.AddCommand(cliCmd)

	return duoCmd
}
