package create

import (
	"context"

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
		Use:   "create [flags]",
		Short: `Create a runner controller. (EXPERIMENTAL)`,
		Long: heredoc.Doc(`
			You must have administrator access. The runner controller can be
			created in a disabled state for testing before you enable the runner controller.
		`) + text.ExperimentalString,
		Args: cobra.NoArgs,
		Example: heredoc.Doc(`
			# Create a runner controller with default settings
			glab runner-controller create

			# Create a runner controller with a description
			glab runner-controller create --description "My controller"

			# Create an enabled runner controller
			glab runner-controller create --description "Production" --state enabled`),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
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

	return cmd
}

func (o *options) run(ctx context.Context) error {
	apiClient, err := o.apiClient("")
	if err != nil {
		return err
	}
	client := apiClient.Lab()

	createOpts := &gitlab.CreateRunnerControllerOptions{}
	if o.description != "" {
		createOpts.Description = new(o.description)
	}
	if o.state != "" {
		createOpts.State = new(o.state)
	}

	controller, _, err := client.RunnerControllers.CreateRunnerController(createOpts, gitlab.WithContext(ctx))
	if err != nil {
		return cmdutils.WrapError(err, "failed to create runner controller")
	}

	switch o.outputFormat {
	case "json":
		return o.io.PrintJSON(controller)
	default:
		o.io.LogInfof("Created runner controller %d\n", controller.ID)
		return nil
	}
}
