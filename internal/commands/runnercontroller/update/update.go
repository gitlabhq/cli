package update

import (
	"context"
	"strconv"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type options struct {
	io        *iostreams.IOStreams
	apiClient func(repoHost string) (*api.Client, error)

	id           int64
	description  string
	state        gitlab.RunnerControllerStateValue
	outputFormat string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
	}

	cmd := &cobra.Command{
		Use:   "update <id> [flags]",
		Short: `Update a runner controller. (EXPERIMENTAL)`,
		Long: heredoc.Docf(`
			You must specify at least one of %[1]s--description%[1]s or %[1]s--state%[1]s.
		`, "`") + text.ExperimentalString,
		Args: cobra.ExactArgs(1),
		Example: heredoc.Doc(`
			# Update a runner controller's description
			glab runner-controller update 42 --description "Updated description"

			# Update a runner controller's state
			glab runner-controller update 42 --state enabled

			# Update both description and state
			glab runner-controller update 42 --description "Production" --state enabled`),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(args); err != nil {
				return err
			}
			return opts.run(cmd.Context())
		},
	}

	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)

	fl := cmd.Flags()
	fl.StringVarP(&opts.description, "description", "d", "", "Description of the runner controller.")
	fl.Var(
		cmdutils.NewEnumValue([]gitlab.RunnerControllerStateValue{
			gitlab.RunnerControllerStateDisabled,
			gitlab.RunnerControllerStateEnabled,
			gitlab.RunnerControllerStateDryRun,
		}, "", &opts.state),
		"state",
		"State of the runner controller: disabled, enabled, dry_run.",
	)

	cmd.MarkFlagsOneRequired("description", "state")

	return cmd
}

func (o *options) complete(args []string) error {
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return cmdutils.WrapError(err, "invalid runner controller ID")
	}
	o.id = id
	return nil
}

func (o *options) run(ctx context.Context) error {
	apiClient, err := o.apiClient("")
	if err != nil {
		return err
	}
	client := apiClient.Lab()

	updateOpts := &gitlab.UpdateRunnerControllerOptions{}
	if o.description != "" {
		updateOpts.Description = new(o.description)
	}
	if o.state != "" {
		updateOpts.State = new(o.state)
	}

	controller, _, err := client.RunnerControllers.UpdateRunnerController(o.id, updateOpts, gitlab.WithContext(ctx))
	if err != nil {
		return cmdutils.WrapError(err, "failed to update runner controller")
	}

	switch o.outputFormat {
	case "json":
		return o.io.PrintJSON(controller)
	default:
		o.io.LogInfof("Updated runner controller %d\n", controller.ID)
		return nil
	}
}
