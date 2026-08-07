package status

import (
	"fmt"
	"time"

	"charm.land/huh/v2"
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/gosuri/uilive"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/ci/ciutils"
	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

type options struct {
	outputFormat string

	io           *iostreams.IOStreams
	gitlabClient func() (*gitlab.Client, error)
	baseRepo     func() (glrepo.Interface, error)
	branch       func() (string, error)
}

func NewCmdStatus(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
		baseRepo:     f.BaseRepo,
		branch:       f.Branch,
	}

	pipelineStatusCmd := &cobra.Command{
		Use:     "status [flags]",
		Short:   `View CI/CD pipeline status.`,
		Aliases: []string{"stats"},
		Long: heredoc.Docf(`
			Defaults to the current branch.

			Use %[1]s--live%[1]s for real-time updates. Use %[1]s--compact%[1]s for a condensed view.
		`, "`"),
		Example: heredoc.Doc(`
			# View the pipeline status in real time
			glab ci status --live

			# Wait to return until the pipeline is finished, and provide output without a prompt.
			glab ci status --wait

			# A more compact view
			glab ci status --compact

			# Get the pipeline for the main branch
			glab ci status --branch=main

			# Get the pipeline for the current branch
			glab ci status`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error
			c := opts.io.Color()

			client, err := opts.gitlabClient()
			if err != nil {
				return err
			}

			branch, _ := cmd.Flags().GetString("branch")
			live, _ := cmd.Flags().GetBool("live")
			compact, _ := cmd.Flags().GetBool("compact")
			wait, _ := cmd.Flags().GetBool("wait")

			if opts.outputFormat == "json" && (live || compact || wait) {
				return fmt.Errorf("--output json cannot be used with --live, --wait, or --compact flags")
			}

			if wait {
				live = true
				opts.io.SetPrompt("true")
			}

			repo, err := opts.baseRepo()
			if err != nil {
				return err
			}
			repoName := repo.FullName()
			dbg.Debug("Repository:", repoName)

			// Get the correct branch name using the utility function
			branch = ciutils.GetBranch(branch, func() (string, error) {
				return opts.branch()
			}, repo, client)
			dbg.Debug("Using branch:", branch)

			// Use fallback logic for robust pipeline lookup
			runningPipeline, err := ciutils.GetPipelineWithFallback(cmd.Context(), client, repoName, branch, opts.io)
			if err != nil {
				if opts.outputFormat != "json" {
					redCheck := c.Red("✘")
					opts.io.LogInfof("%s %v\n", redCheck, err)
				}
				return err
			}

			// For JSON output, fetch jobs once and return the data
			if opts.outputFormat == "json" {
				jobs, err := gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.Job, *gitlab.Response, error) {
					return client.Jobs.ListPipelineJobs(repoName, runningPipeline.ID, &gitlab.ListJobsOptions{ListOptions: gitlab.ListOptions{PerPage: 100}}, p, gitlab.WithContext(cmd.Context()))
				})
				if err != nil {
					return err
				}
				output := map[string]any{
					"pipeline": runningPipeline,
					"jobs":     jobs,
				}
				return opts.io.PrintJSON(output)
			}

			writer := uilive.New()

			ctx := cmd.Context()

			// Set up ticker for live updates if needed
			var ticker *time.Ticker
			if live {
				ticker = time.NewTicker(3 * time.Second)
				defer ticker.Stop()
			}

			// start listening for updates and render
			writer.Start()
			defer writer.Stop()
		loop:
			for {
				jobs, err := gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.Job, *gitlab.Response, error) {
					return client.Jobs.ListPipelineJobs(repoName, runningPipeline.ID, &gitlab.ListJobsOptions{ListOptions: gitlab.ListOptions{PerPage: 100}}, p, gitlab.WithContext(ctx))
				})
				if err != nil {
					if ctx.Err() != nil {
						break
					}
					return err
				}
				for _, job := range jobs {
					end := time.Now()
					if job.FinishedAt != nil {
						end = *job.FinishedAt
					}
					var duration string
					if job.StartedAt != nil {
						duration = utils.FmtDuration(end.Sub(*job.StartedAt))
					} else {
						duration = "not started"
					}
					var status string
					switch s := job.Status; s {
					case string(gitlab.Failed):
						if job.AllowFailure {
							status = c.Yellow(s)
						} else {
							status = c.Red(s)
						}
					case string(gitlab.Success):
						status = c.Green(s)
					default:
						status = c.Gray(s)
					}
					if compact {
						fmt.Fprintf(writer, "(%s) • %s [%s]\n", status, job.Name, job.Stage) //nolint:forbidigo // writer is a uilive live-updating writer, not IOStreams
					} else {
						fmt.Fprintf(writer, "(%s) • %s\t%s\t\t%s\n", status, c.Gray(duration), job.Stage, job.Name) //nolint:forbidigo // writer is a uilive live-updating writer, not IOStreams
					}
				}

				if !compact {
					fmt.Fprintf(writer.Newline(), "\n%s\n", runningPipeline.WebURL) //nolint:forbidigo // writer is a uilive live-updating writer, not IOStreams
					fmt.Fprintf(writer.Newline(), "SHA: %s\n", runningPipeline.SHA) //nolint:forbidigo // writer is a uilive live-updating writer, not IOStreams
				}
				fmt.Fprintf(writer.Newline(), "Pipeline state: %s\n\n", runningPipeline.Status) //nolint:forbidigo // writer is a uilive live-updating writer, not IOStreams

				inProgress := isLivePollableStatus(runningPipeline.Status)

				// When the current pipeline is canceled (e.g. auto-canceled because a
				// rebase triggered a new pipeline), check whether a newer pipeline now
				// exists for the branch and continue monitoring it instead of exiting.
				if !inProgress && runningPipeline.Status == string(gitlab.Canceled) && live {
					if latest, lookupErr := ciutils.GetPipelineWithFallback(ctx, client, repoName, branch, opts.io); lookupErr == nil && latest != nil && latest.ID != runningPipeline.ID {
						runningPipeline = latest
						inProgress = isLivePollableStatus(latest.Status)
						// Skip the redundant refresh below by waiting for the next
						// tick and restarting the loop, which will re-render jobs
						// for the new pipeline and then fall into the normal
						// live-update path.
						if inProgress {
							select {
							case <-ctx.Done():
								break loop
							case <-ticker.C:
							}
							continue
						}
					}
				}

				if inProgress && live {
					// Use fallback logic for live updates
					updatedPipeline, err := ciutils.GetPipelineWithFallback(ctx, client, repoName, branch, opts.io)
					if err != nil {
						// Final fallback: refresh current pipeline by ID
						updatedPipeline, _, err = client.Pipelines.GetPipeline(repoName, runningPipeline.ID, gitlab.WithContext(ctx))
						if err != nil {
							if ctx.Err() != nil {
								break loop
							}
							return err
						}
					}
					runningPipeline = updatedPipeline

					// Wait between updates, but allow cancellation
					select {
					case <-ctx.Done():
						break loop
					case <-ticker.C:
					}
				} else if opts.io.IsInteractive() {
					var answer string
					selector := huh.NewSelect[string]().
						Title("Choose an action:").
						Options(
							huh.NewOption("View logs", "View logs"),
							huh.NewOption("Retry", "Retry"),
							huh.NewOption("Exit", "Exit"),
						).
						Value(&answer)
					if err := opts.io.Run(ctx, selector); err != nil {
						if ctx.Err() != nil {
							break
						}
						return err
					}
					switch answer {
					case "View logs":
						return ciutils.TraceJob(ctx, &ciutils.JobInputs{
							Branch: branch,
						}, &ciutils.JobOptions{
							Repo:       repo,
							Client:     client,
							IO:         opts.io,
							BranchFunc: opts.branch,
						})
					case "Retry":
						_, _, err := client.Pipelines.RetryPipelineBuild(repoName, runningPipeline.ID)
						if err != nil {
							if ctx.Err() != nil {
								break loop
							}
							return err
						}
						updatedPipeline, err := ciutils.GetPipelineWithFallback(ctx, client, repoName, branch, opts.io)
						if err != nil {
							// Fallback: refresh by pipeline ID if MR lookup fails
							updatedPipeline, _, err = client.Pipelines.GetPipeline(repoName, runningPipeline.ID, gitlab.WithContext(ctx))
							if err != nil {
								if ctx.Err() != nil {
									break loop
								}
								return err
							}
						}
						runningPipeline = updatedPipeline
					default:
						break loop
					}
				} else {
					break
				}
			}
			// Only show "Exiting..." message if cancelled via Ctrl+C
			if ctx.Err() != nil {
				fmt.Fprintln(writer.Newline(), "Exiting...") //nolint:forbidigo // writer is a uilive live-updating writer, not IOStreams
			}
			if runningPipeline.Status == string(gitlab.Failed) {
				return cmdutils.SilentError
			}
			return nil
		},
	}

	pipelineStatusCmd.Flags().BoolP("live", "l", false, "Show status in real time until the pipeline ends.")
	pipelineStatusCmd.Flags().BoolP("compact", "c", false, "Show status in compact format.")
	pipelineStatusCmd.Flags().BoolP("wait", "w", false, "Wait to return until the pipeline is finished, and provide output without a prompt.")
	pipelineStatusCmd.Flags().StringP("branch", "b", "", "Check pipeline status for a branch. Defaults to the current branch.")
	cmdutils.EnableJSONOutput(pipelineStatusCmd, opts.io, &opts.outputFormat, "Format output as: text, json. Note: JSON output is not compatible with --live, --wait, or --compact flags.")

	return pipelineStatusCmd
}

// isLivePollableStatus reports whether a pipeline status represents an
// in-progress state that --live should keep polling. Terminal statuses
// (success, failed, skipped, canceled) and statuses requiring user action
// (manual) are excluded — they don't progress on their own.
func isLivePollableStatus(status string) bool {
	switch status {
	case string(gitlab.Created), string(gitlab.WaitingForResource), string(gitlab.Preparing),
		string(gitlab.Pending), string(gitlab.Running), string(gitlab.Scheduled):
		return true
	}
	return false
}
