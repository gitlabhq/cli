package delete

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

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
	force        bool
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
	}

	cmd := &cobra.Command{
		Use:   "delete <controller-id> [flags]",
		Short: `Delete a scope from a runner controller. (EXPERIMENTAL)`,
		Long: heredoc.Docf(`
			Deletes a scope from a runner controller. This is an administrator-only feature.

			Use one of the following flags to specify the scope type:

			- --instance: Remove an instance-level scope from the runner controller.
			- --runner <id>: Remove a runner-level scope for a specific runner. Multiple IDs
			  can be comma-separated or specified by repeating the flag.
			%s`, text.ExperimentalString),
		Args: cobra.ExactArgs(1),
		Example: heredoc.Doc(`
			# Remove an instance-level scope from runner controller 42 (with confirmation)
			glab runner-controller scope delete 42 --instance

			# Remove an instance-level scope without confirmation
			glab runner-controller scope delete 42 --instance --force

			# Remove a runner-level scope for runner 5 from runner controller 42
			glab runner-controller scope delete 42 --runner 5 --force

			# Remove runner-level scopes for multiple runners
			glab runner-controller scope delete 42 --runner 5 --runner 10 --force
			glab runner-controller scope delete 42 --runner 5,10 --force`),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(args); err != nil {
				return err
			}
			if err := opts.validate(cmd.Context()); err != nil {
				return err
			}
			return opts.run(cmd.Context())
		},
	}

	fl := cmd.Flags()
	fl.BoolVar(&opts.instance, "instance", false, "Remove an instance-level scope.")
	fl.Int64SliceVar(&opts.runnerIDs, "runner", nil, "Remove a runner-level scope for the specified runner ID. Multiple IDs can be comma-separated or specified by repeating the flag.")
	fl.BoolVarP(&opts.force, "force", "f", false, "Skip confirmation prompt.")

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

func (o *options) validate(ctx context.Context) error {
	if !o.force {
		if !o.io.PromptEnabled() {
			return cmdutils.FlagError{Err: errors.New("--force required when not running interactively")}
		}

		var confirmMsg string
		switch {
		case o.instance:
			confirmMsg = fmt.Sprintf("Remove instance-level scope from runner controller %d?", o.controllerID)
		default:
			ids := make([]string, 0, len(o.runnerIDs))
			for _, id := range o.runnerIDs {
				ids = append(ids, strconv.FormatInt(id, 10))
			}
			confirmMsg = fmt.Sprintf("Remove runner-level scope for runner(s) %s from runner controller %d?", strings.Join(ids, ", "), o.controllerID)
		}

		var confirmed bool
		err := o.io.Confirm(ctx, &confirmed, confirmMsg)
		if err != nil {
			return err
		}

		if !confirmed {
			return cmdutils.CancelError()
		}
	}
	return nil
}

func (o *options) run(ctx context.Context) error {
	apiClient, err := o.apiClient("")
	if err != nil {
		return err
	}
	client := apiClient.Lab()

	switch {
	case o.instance:
		_, err = client.RunnerControllerScopes.RemoveRunnerControllerInstanceScope(o.controllerID, gitlab.WithContext(ctx))
		if err != nil {
			return cmdutils.WrapError(err, "failed to remove instance-level scope")
		}
		o.io.LogInfof("Removed instance-level scope from runner controller %d\n", o.controllerID)
	default:
		for _, runnerID := range o.runnerIDs {
			_, err = client.RunnerControllerScopes.RemoveRunnerControllerRunnerScope(o.controllerID, runnerID, gitlab.WithContext(ctx))
			if err != nil {
				return cmdutils.WrapError(err, fmt.Sprintf("failed to remove runner-level scope for runner %d", runnerID))
			}
			o.io.LogInfof("Removed runner-level scope for runner %d from runner controller %d\n", runnerID, o.controllerID)
		}
	}

	return nil
}
