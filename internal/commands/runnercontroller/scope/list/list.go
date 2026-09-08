package list

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
		Use:   "list <controller-id> [flags]",
		Short: `List scopes for a runner controller. (EXPERIMENTAL)`,
		Long: heredoc.Docf(`
			Scopes can be for the instance (applies to all runners) or runner-level
			(applies to specific runners).
			%s`, text.ExperimentalString),
		Args: cobra.ExactArgs(1),
		Example: heredoc.Doc(`
			# List all scopes for runner controller 42
			glab runner-controller scope list 42

			# List scopes as JSON
			glab runner-controller scope list 42 --output json`),
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

	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)

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

	scopes, _, err := client.RunnerControllerScopes.ListRunnerControllerScopes(o.controllerID, gitlab.WithContext(ctx))
	if err != nil {
		return cmdutils.WrapError(err, "failed to list runner controller scopes")
	}

	switch o.outputFormat {
	case "json":
		return o.io.PrintJSON(scopes)
	default:
		return o.printTable(scopes)
	}
}

func (o *options) printTable(scopes *gitlab.RunnerControllerScopes) error {
	c := o.io.Color()

	if len(scopes.InstanceLevelScopings) == 0 && len(scopes.RunnerLevelScopings) == 0 {
		o.io.LogInfof("No scopes configured for runner controller %d\n", o.controllerID)
		return nil
	}

	table := tableprinter.NewTablePrinter()
	table.AddRow(c.Bold("Scope Type"), c.Bold("Runner ID"), c.Bold("Created At"), c.Bold("Updated At"))
	for _, s := range scopes.InstanceLevelScopings {
		table.AddRow("instance", "-", s.CreatedAt, s.UpdatedAt)
	}
	for _, s := range scopes.RunnerLevelScopings {
		table.AddRow("runner", s.RunnerID, s.CreatedAt, s.UpdatedAt)
	}
	o.io.LogInfof("%s", table.Render())
	return nil
}
