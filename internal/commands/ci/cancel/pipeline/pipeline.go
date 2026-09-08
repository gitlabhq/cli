package pipeline

import (
	"fmt"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/ci/ciutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

const (
	FlagDryRun = "dry-run"
)

func NewCmdCancel(f cmdutils.Factory) *cobra.Command {
	pipelineCancelCmd := &cobra.Command{
		Use:   "pipeline <id> [<id>...] [flags]",
		Short: `Cancel CI/CD pipelines.`,
		Long: heredoc.Docf(`
		Cancels one or more running CI/CD pipelines by ID. You can pass
		multiple pipeline IDs as separate arguments, in a comma-separated
		list, or in a quoted space-separated list.

		To preview which pipelines would be canceled without making changes,
		use %[1]s--dry-run%[1]s.
		`, "`"),
		Example: heredoc.Doc(`
			# Cancel a single pipeline
			glab ci cancel pipeline 1504182795

			# Cancel multiple pipelines, comma-separated
			glab ci cancel pipeline 1504182795,1504182796

			# Cancel multiple pipelines, space-separated in quotes
			glab ci cancel pipeline "1504182795 1504182796"

			# Preview which pipelines would be canceled
			glab ci cancel pipeline 1504182795,1504182796 --dry-run
		`),
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return fmt.Errorf("you must pass a pipeline ID")
			}

			return nil
		},
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error
			c := f.IO().Color()
			client, err := f.GitLabClient()
			if err != nil {
				return err
			}

			repo, err := f.BaseRepo()
			if err != nil {
				return err
			}
			dryRunMode, _ := cmd.Flags().GetBool(FlagDryRun)

			var pipelineIDs []int

			pipelineIDs, err = ciutils.IDsFromArgs(args)
			if err != nil {
				return err
			}
			return runCancelation(f.IO(), pipelineIDs, dryRunMode, c, client, repo)
		},
	}

	SetupCommandFlags(pipelineCancelCmd.Flags())
	return pipelineCancelCmd
}

func SetupCommandFlags(flags *pflag.FlagSet) {
	flags.BoolP(FlagDryRun, "", false, "Show which pipelines would be canceled, without canceling them.")
}

func runCancelation(
	io *iostreams.IOStreams,
	pipelineIDs []int,
	dryRunMode bool,
	c *iostreams.ColorPalette,
	apiClient *gitlab.Client,
	repo glrepo.Interface,
) error {
	for _, id := range pipelineIDs {
		if dryRunMode {
			io.LogInfof("%s Pipeline #%d will be canceled.\n", c.DotWarnIcon(), id)
		} else {
			pid, err := repo.Project(apiClient)
			if err != nil {
				return err
			}
			_, _, err = apiClient.Pipelines.CancelPipelineBuild(pid.ID, int64(id))
			if err != nil {
				return err
			}
			io.LogInfof("%s Pipeline #%d is canceled successfully.\n", c.RedCheck(), id)
		}
	}
	fmt.Println()

	return nil
}
