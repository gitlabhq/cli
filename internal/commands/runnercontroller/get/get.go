package get

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
	"gitlab.com/gitlab-org/cli/internal/tableprinter"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type options struct {
	io        *iostreams.IOStreams
	apiClient func(repoHost string) (*api.Client, error)

	controllerID int64
	outputFormat string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
	}

	cmd := &cobra.Command{
		Use:   "get <controller-id> [flags]",
		Short: `Get details of a runner controller. (EXPERIMENTAL)`,
		Long: heredoc.Docf(`
			Retrieves details of a single runner controller, including its
			connection status. This is an administrator-only feature.
			%s`, text.ExperimentalString),
		Args: cobra.ExactArgs(1),
		Example: heredoc.Doc(`
			# Get runner controller with ID 42
			glab runner-controller get 42

			# Get runner controller as JSON
			glab runner-controller get 42 --output json`),
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

	fl := cmd.Flags()
	fl.VarP(cmdutils.NewEnumValue([]string{"text", "json"}, "text", &opts.outputFormat), "output", "F", "Format output as: text, json.")

	cmdutils.AddJQFlag(cmd, f.IO())
	return cmd
}

func (o *options) complete(args []string) error {
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return cmdutils.WrapError(err, "invalid runner controller ID")
	}
	o.controllerID = id
	return nil
}

func (o *options) run(ctx context.Context) error {
	apiClient, err := o.apiClient("")
	if err != nil {
		return err
	}
	client := apiClient.Lab()

	controller, _, err := client.RunnerControllers.GetRunnerController(o.controllerID, gitlab.WithContext(ctx))
	if err != nil {
		return cmdutils.WrapError(err, "failed to get runner controller")
	}

	switch o.outputFormat {
	case "json":
		return o.io.PrintJSON(controller)
	default:
		return o.printDetails(controller)
	}
}

func (o *options) printDetails(controller *gitlab.RunnerControllerDetails) error {
	c := o.io.Color()
	table := tableprinter.NewTablePrinter()
	table.AddRow(c.Bold("ID"), controller.ID)
	table.AddRow(c.Bold("Description"), controller.Description)
	table.AddRow(c.Bold("State"), formatState(c, controller.State))
	table.AddRow(c.Bold("Connected"), formatConnected(c, controller.Connected))
	table.AddRow(c.Bold("Created At"), controller.CreatedAt)
	table.AddRow(c.Bold("Updated At"), controller.UpdatedAt)
	o.io.LogInfof("%s", table.Render())
	return nil
}

func formatState(c *iostreams.ColorPalette, state gitlab.RunnerControllerStateValue) string {
	switch state {
	case gitlab.RunnerControllerStateDisabled:
		return c.Gray(string(state))
	case gitlab.RunnerControllerStateDryRun:
		return c.Yellow(string(state))
	case gitlab.RunnerControllerStateEnabled:
		return c.Green(string(state))
	default:
		return string(state)
	}
}

func formatConnected(c *iostreams.ColorPalette, connected bool) string {
	if connected {
		return c.Green("yes")
	}
	return c.Gray("no")
}
