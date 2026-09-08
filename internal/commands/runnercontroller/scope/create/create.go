package create

import (
	"context"
	"fmt"
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

	controllerID int64
	runnerIDs    []int64
	instance     bool
	outputFormat string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
	}

	cmd := &cobra.Command{
		Use:   "create <controller-id> [flags]",
		Short: `Create a scope for a runner controller. (EXPERIMENTAL)`,
		Long: heredoc.Docf(`
			Creates a scope for a runner controller. This is an administrator-only feature.

			Use one of the following flags to specify the scope type:

			- --instance: Add an instance-level scope, allowing the runner controller
			  to evaluate jobs for all runners in the GitLab instance.
			- --runner <id>: Add a runner-level scope, allowing the runner controller
			  to evaluate jobs for a specific instance-level runner. Multiple IDs can
			  be comma-separated or specified by repeating the flag.
			%s`, text.ExperimentalString),
		Args: cobra.ExactArgs(1),
		Example: heredoc.Doc(`
			# Add an instance-level scope to runner controller 42
			glab runner-controller scope create 42 --instance

			# Add a runner-level scope for runner 5 to runner controller 42
			glab runner-controller scope create 42 --runner 5

			# Add runner-level scopes for multiple runners
			glab runner-controller scope create 42 --runner 5 --runner 10
			glab runner-controller scope create 42 --runner 5,10

			# Add a runner-level scope and output as JSON
			glab runner-controller scope create 42 --runner 5 --output json`),
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
	fl.BoolVar(&opts.instance, "instance", false, "Add an instance-level scope.")
	fl.Int64SliceVar(&opts.runnerIDs, "runner", nil, "Add a runner-level scope for the specified runner ID. Multiple IDs can be comma-separated or specified by repeating the flag.")

	cmd.MarkFlagsMutuallyExclusive("instance", "runner")
	cmd.MarkFlagsOneRequired("instance", "runner")

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

	var results []any

	switch {
	case o.instance:
		scoping, _, err := client.RunnerControllerScopes.AddRunnerControllerInstanceScope(o.controllerID, gitlab.WithContext(ctx))
		if err != nil {
			return cmdutils.WrapError(err, "failed to add instance-level scope")
		}
		results = append(results, scoping)
	default:
		for _, runnerID := range o.runnerIDs {
			scoping, _, err := client.RunnerControllerScopes.AddRunnerControllerRunnerScope(o.controllerID, runnerID, gitlab.WithContext(ctx))
			if err != nil {
				return cmdutils.WrapError(err, fmt.Sprintf("failed to add runner-level scope for runner %d", runnerID))
			}
			results = append(results, scoping)
		}
	}

	if o.outputFormat == "json" {
		return o.io.PrintJSON(results)
	}

	switch {
	case o.instance:
		o.io.LogInfof("Added instance-level scope to runner controller %d\n", o.controllerID)
	default:
		for _, runnerID := range o.runnerIDs {
			o.io.LogInfof("Added runner-level scope for runner %d to runner controller %d\n", runnerID, o.controllerID)
		}
	}
	return nil
}
