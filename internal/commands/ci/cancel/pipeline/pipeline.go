package pipeline

import (
	"fmt"
	"io"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

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
		Use:   "pipeline <id> [flags]",
		Short: `Cancel CI/CD pipelines.`,
		Long: heredoc.Docf(`
			Use %[1]s--dry-run%[1]s to list pipelines that would be canceled without making changes.
		`, "`"),
		Example: heredoc.Doc(`
			glab ci cancel pipeline 1504182795
			glab ci cancel pipeline 1504182795,1504182796
			glab ci cancel pipeline "1504182795 1504182796"
			glab ci cancel pipeline 1504182795,1504182796 --dry-run`),
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return fmt.Errorf("You must pass a pipeline ID.")
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
			return runCancelation(pipelineIDs, dryRunMode, f.IO().StdOut, c, client, repo)
		},
	}

	SetupCommandFlags(pipelineCancelCmd.Flags())
	return pipelineCancelCmd
}

func SetupCommandFlags(flags *pflag.FlagSet) {
	flags.BoolP(FlagDryRun, "", false, "Simulates process, but does not cancel anything.")
}

func runCancelation(
	pipelineIDs []int,
	dryRunMode bool,
	w io.Writer,
	c *iostreams.ColorPalette,
	apiClient *gitlab.Client,
	repo glrepo.Interface,
) error {
	for _, id := range pipelineIDs {
		if dryRunMode {
			fmt.Fprintf(w, "%s Pipeline #%d will be canceled.\n", c.DotWarnIcon(), id)
		} else {
			pid, err := repo.Project(apiClient)
			if err != nil {
				return err
			}
			_, _, err = apiClient.Pipelines.CancelPipelineBuild(pid.ID, int64(id))
			if err != nil {
				return err
			}
			fmt.Fprintf(w, "%s Pipeline #%d is canceled successfully.\n", c.RedCheck(), id)
		}
	}
	fmt.Println()

	return nil
}
