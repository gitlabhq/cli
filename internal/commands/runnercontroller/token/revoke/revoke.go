package revoke

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

	controllerID int64
	tokenID      int64
	force        bool
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
	}

	cmd := &cobra.Command{
		Use:   "revoke <controller-id> <token-id> [flags]",
		Short: `Revoke a token from a runner controller. (EXPERIMENTAL)`,
		Args:  cobra.ExactArgs(2),
		Example: heredoc.Doc(`
			# Revoke token 1 from runner controller 42 (with confirmation prompt)
			glab runner-controller token revoke 42 1

			# Revoke without confirmation
			glab runner-controller token revoke 42 1 --force`),
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
	controllerID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return cmdutils.WrapError(err, "invalid runner controller ID")
	}
	o.controllerID = controllerID

	tokenID, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		return cmdutils.WrapError(err, "invalid token ID")
	}
	o.tokenID = tokenID
	return nil
}

func (o *options) validate(ctx context.Context) error {
	if !o.force {
		if !o.io.PromptEnabled() {
			return cmdutils.FlagError{Err: errors.New("--force required when not running interactively")}
		}
		var confirmed bool
		err := o.io.Confirm(ctx, &confirmed, fmt.Sprintf("Revoke token %d from runner controller %d?", o.tokenID, o.controllerID))
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

	_, err = client.RunnerControllerTokens.RevokeRunnerControllerToken(o.controllerID, o.tokenID, gitlab.WithContext(ctx))
	if err != nil {
		return cmdutils.WrapError(err, "failed to revoke runner controller token")
	}

	o.io.LogInfof("Revoked token %d from runner controller %d\n", o.tokenID, o.controllerID)
	return nil
}
