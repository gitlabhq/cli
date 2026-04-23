// This package contains the old `glab pipeline ci` command which has been deprecated
// in favour of the `glab ci` command.
// This package is kept for backward compatibility but prints a deprecation warning
package legacyci

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	ciLintCmd "gitlab.com/gitlab-org/cli/internal/commands/ci/lint"
	ciTraceCmd "gitlab.com/gitlab-org/cli/internal/commands/ci/trace"
	ciViewCmd "gitlab.com/gitlab-org/cli/internal/commands/ci/view"
)

func NewCmdCI(f cmdutils.Factory) *cobra.Command {
	pipelineCICmd := &cobra.Command{
		Use:   "ci <command> [flags]",
		Short: `Work with GitLab CI/CD pipelines and jobs.`,
		Example: heredoc.Doc(`
			$ glab pipeline ci trace
		`),
	}

	pipelineCICmd.AddCommand(ciTraceCmd.NewCmdTrace(f))
	pipelineCICmd.AddCommand(ciViewCmd.NewCmdView(f))
	pipelineCICmd.AddCommand(ciLintCmd.NewCmdLint(f))
	pipelineCICmd.Deprecated = "This command is deprecated. All the commands under it has been moved to `ci` or `pipeline` command. See https://gitlab.com/gitlab-org/cli/issues/372 for more info."
	pipelineCICmd.Hidden = true
	return pipelineCICmd
}
