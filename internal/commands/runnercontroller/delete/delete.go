package delete

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	io        *iostreams.IOStreams
	apiClient func(repoHost string) (*api.Client, error)

	id    int64
	force bool
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
	}

	cmd := &cobra.Command{
		Use:   "delete <id> [flags]",
		Short: `Delete a runner controller. (EXPERIMENTAL)`,
		Args:  cobra.ExactArgs(1),
		Example: heredoc.Doc(`
			# Delete a runner controller (with confirmation prompt)
			glab runner-controller delete 42

			# Delete a runner controller without confirmation
			glab runner-controller delete 42 --force`),
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

	cmd.Flags().BoolVarP(&opts.force, "force", "f", false, "Skip confirmation prompt. (default false)")

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

func (o *options) validate(ctx context.Context) error {
	if !o.force {
		if !o.io.PromptEnabled() {
			return cmdutils.FlagError{Err: errors.New("--force required when not running interactively")}
		}

		var confirmed bool
		err := o.io.Confirm(ctx, &confirmed, fmt.Sprintf("Delete runner controller %d?", o.id))
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

	_, err = client.RunnerControllers.DeleteRunnerController(o.id, gitlab.WithContext(ctx))
	if err != nil {
		return cmdutils.WrapError(err, "failed to delete runner controller")
	}

	o.io.LogInfof("Deleted runner controller %d\n", o.id)
	return nil
}
