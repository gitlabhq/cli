package update

import (
	"context"
	"strconv"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	io        *iostreams.IOStreams
	apiClient func(repoHost string) (*api.Client, error)
	baseRepo  func() (glrepo.Interface, error)

	runnerID int64
	pause    *bool
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
		baseRepo:  f.BaseRepo,
	}

	cmd := &cobra.Command{
		Use:   "update <runner-id>",
		Short: "Update a runner.",
		Args:  cobra.ExactArgs(1),
		Long: heredoc.Doc(`
			Updates settings for a GitLab CI/CD runner.

			Use flags to update the runner configuration, such as the pause state.

			Prerequisites:

			- Maintainer or Owner role for project runners.
			- Owner role for group runners.
			- Administrator access for instance runners.
		`),
		Example: heredoc.Doc(`
			# Pause a runner
			glab runner update <runner-id> --pause

			# Unpause a runner
			glab runner update <runner-id> --unpause

			# Pause a runner in a specific project
			glab runner update <runner-id> --pause -R owner/repo`),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(args); err != nil {
				return err
			}

			return opts.run(cmd.Context())
		},
	}

	cmdutils.NewBoolFlagPair(cmd, &opts.pause, "pause", "Pause the runner.", "unpause", "Resume a paused runner.")
	cmd.MarkFlagsOneRequired("pause", "unpause")
	return cmd
}

func (o *options) complete(args []string) error {
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return cmdutils.WrapError(err, "invalid runner ID")
	}

	o.runnerID = id
	return nil
}

func (o *options) run(ctx context.Context) error {
	c, err := o.apiClient("")
	if err != nil {
		return err
	}
	client := c.Lab()
	updateOpts := o.buildUpdateOptions()
	return o.updateRunnerDetails(ctx, client, updateOpts)
}

func (o *options) buildUpdateOptions() *gitlab.UpdateRunnerDetailsOptions {
	opts := &gitlab.UpdateRunnerDetailsOptions{}
	if o.pause != nil {
		opts.Paused = o.pause
	}
	return opts
}

func (o *options) updateRunnerDetails(ctx context.Context, client *gitlab.Client, updateOpts *gitlab.UpdateRunnerDetailsOptions) error {
	details, _, err := client.Runners.UpdateRunnerDetails(o.runnerID, updateOpts, gitlab.WithContext(ctx))
	if err != nil {
		return cmdutils.WrapError(err, "failed to update runner")
	}

	if updateOpts.Paused != nil {
		if details.Paused {
			o.io.LogInfof("Runner %d has been paused and will not accept new jobs.\n", o.runnerID)
		} else {
			o.io.LogInfof("Runner %d has been unpaused and is now accepting jobs.\n", o.runnerID)
		}
	}
	return nil
}
