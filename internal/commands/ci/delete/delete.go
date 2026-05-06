package delete

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

const (
	FlagDryRun    = "dry-run"
	FlagOlderThan = "older-than"
	FlagPage      = "page"
	FlagPaginate  = "paginate"
	FlagPerPage   = "per-page"
	FlagSource    = "source"
	FlagStatus    = "status"
)

var (
	pipelineStatuses = []string{"running", "pending", "success", "failed", "canceled", "skipped", "created", "manual"}
	pipelineSources  = []string{
		"api", "chat", "external", "external_pull_request_event", "merge_request_event",
		"ondemand_dast_scan", "ondemand_dast_validation", "parent_pipeline", "pipeline",
		"push", "schedule", "security_orchestration_policy", "trigger", "web", "webide",
	}
)

func NewCmdDelete(f cmdutils.Factory) *cobra.Command {
	pipelineDeleteCmd := &cobra.Command{
		Use:   "delete <id> [flags]",
		Short: `Delete CI/CD pipelines.`,
		Long: heredoc.Docf(`
			You can delete by ID, or filter by status, source, or age. Use %[1]s--dry-run%[1]s to preview
			the pipelines that would be deleted.
		`, "`"),
		Example: heredoc.Doc(`
			glab ci delete 34
			glab ci delete 12,34,2
			glab ci delete --source=api
			glab ci delete --status=failed
			glab ci delete --older-than 24h
			glab ci delete --older-than 24h --status=failed`),
		Args: func(cmd *cobra.Command, args []string) error {
			olderThanDuration, _ := cmd.Flags().GetDuration(FlagOlderThan)
			status, _ := cmd.Flags().GetString(FlagStatus)
			source, _ := cmd.Flags().GetString(FlagSource)

			if olderThanDuration == 0 && status == "" && source == "" {
				return cobra.ExactArgs(1)(cmd, args)
			}

			if len(args) > 0 {
				return fmt.Errorf("either a status filter or a pipeline ID must be passed, but not both.")
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
			if len(args) == 1 {
				pipelineIDs, err = parseRawPipelineIDs(args[0])
				if err != nil {
					return err
				}

				return runDeletion(pipelineIDs, dryRunMode, f.IO().StdOut, c, client, repo)
			}

			paginate, _ := cmd.Flags().GetBool(FlagPaginate)

			pipelineIDs, err = listPipelineIDs(client, repo.FullName(), paginate, optsFromFlags(cmd.Flags()))
			if err != nil {
				return err
			}

			return runDeletion(pipelineIDs, dryRunMode, f.IO().StdOut, c, client, repo)
		},
	}

	SetupCommandFlags(pipelineDeleteCmd.Flags())

	return pipelineDeleteCmd
}

func SetupCommandFlags(flags *pflag.FlagSet) {
	flags.BoolP(FlagDryRun, "", false, "Simulate process, but do not delete anything.")
	flags.StringP(FlagStatus, "s", "", fmt.Sprintf("Delete pipelines by status: %s.", strings.Join(pipelineStatuses, ", ")))
	flags.String(FlagSource, "", fmt.Sprintf("Filter pipelines by source: %s.", strings.Join(pipelineSources, ", ")))
	flags.Duration(FlagOlderThan, 0, "Filter pipelines older than the given duration. Valid units: h, m, s, ms, us, ns.")
	flags.BoolP(FlagPaginate, "", false, "Make additional HTTP requests to fetch all pages of projects before cloning. Respects '--per-page'.")
	flags.IntP(FlagPage, "", 0, "Page number.")
	flags.IntP(FlagPerPage, "", 0, "Number of items to list per page.")
}

func optsFromFlags(flags *pflag.FlagSet) *gitlab.ListProjectPipelinesOptions {
	opts := &gitlab.ListProjectPipelinesOptions{}
	page, _ := flags.GetInt(FlagPage)
	perPage, _ := flags.GetInt(FlagPerPage)

	if perPage != 0 {
		opts.PerPage = int64(perPage)
	}
	if page != 0 {
		opts.Page = int64(page)
	}

	source, _ := flags.GetString(FlagSource)
	status, _ := flags.GetString(FlagStatus)
	olderThanDuration, _ := flags.GetDuration(FlagOlderThan)

	if source != "" {
		opts.Source = new(source)
	}

	if status != "" {
		opts.Status = new(gitlab.BuildStateValue(status))
	}

	if olderThanDuration != 0 {
		opts.UpdatedBefore = new(time.Now().Add(-olderThanDuration))
	}

	return opts
}

func parseRawPipelineIDs(rawPipelineIDs string) ([]int, error) {
	var inputPipelineIDs []int
	for stringID := range strings.SplitSeq(rawPipelineIDs, ",") {
		id, err := strconv.Atoi(stringID)
		if err != nil {
			return nil, err
		}
		inputPipelineIDs = append(inputPipelineIDs, id)
	}

	return inputPipelineIDs, nil
}

func runDeletion(pipelineIDs []int, dryRunMode bool, w io.Writer, c *iostreams.ColorPalette, apiClient *gitlab.Client, repo glrepo.Interface) error {
	for _, id := range pipelineIDs {
		if dryRunMode {
			fmt.Fprintf(w, "%s Pipeline #%d will be deleted.\n", c.DotWarnIcon(), id)
			continue
		}

		_, err := apiClient.Pipelines.DeletePipeline(repo.FullName(), int64(id))
		if err != nil {
			return err
		}

		fmt.Fprintf(w, "%s Pipeline #%d deleted successfully.\n", c.RedCheck(), id)
	}
	fmt.Println()

	return nil
}

func listPipelineIDs(apiClient *gitlab.Client, repoName string, paginate bool, opts *gitlab.ListProjectPipelinesOptions) ([]int, error) {
	var pipelineIDs []int

	hasRemaining := true
	for hasRemaining {
		pipes, resp, err := apiClient.Pipelines.ListProjectPipelines(repoName, opts)
		if err != nil {
			return pipelineIDs, err
		}

		for _, item := range pipes {
			pipelineIDs = append(pipelineIDs, int(item.ID))
		}

		opts.Page = resp.NextPage
		hasRemaining = paginate && resp.CurrentPage != resp.TotalPages
	}

	return pipelineIDs, nil
}
