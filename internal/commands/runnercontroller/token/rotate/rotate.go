package rotate

import (
	"context"
	"errors"
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
	tokenID      int64
	force        bool
	outputFormat string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
	}

	cmd := &cobra.Command{
		Use:   "rotate <controller-id> <token-id> [flags]",
		Short: `Rotate a token for a runner controller. (EXPERIMENTAL)`,
		Long: heredoc.Docf(`
			Replaces the current token with a new one. Store the new token value
			securely before closing the terminal. You cannot retrieve the token
			again. Prompts for confirmation before rotation. Use %[1]s--force%[1]s
			to skip the confirmation prompt in non-interactive contexts.
		`, "`") + text.ExperimentalString,
		Args: cobra.ExactArgs(2),
		Example: heredoc.Doc(`
			# Rotate token 1 for runner controller 42 (with confirmation prompt)
			glab runner-controller token rotate 42 1

			# Rotate without confirmation
			glab runner-controller token rotate 42 1 --force

			# Rotate and output as JSON
			glab runner-controller token rotate 42 1 --force --output json`),
		Annotations: map[string]string{
			mcpannotations.Exclude: "true",
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

	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)

	fl := cmd.Flags()
	fl.BoolVarP(&opts.force, "force", "f", false, "Skip confirmation prompt.")

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
		err := o.io.Confirm(ctx, &confirmed, fmt.Sprintf("Rotate token %d for runner controller %d?", o.tokenID, o.controllerID))
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

	token, _, err := client.RunnerControllerTokens.RotateRunnerControllerToken(o.controllerID, o.tokenID, gitlab.WithContext(ctx))
	if err != nil {
		return cmdutils.WrapError(err, "failed to rotate runner controller token")
	}

	switch o.outputFormat {
	case "json":
		return o.io.PrintJSON(token)
	default:
		c := o.io.Color()
		o.io.LogInfof("Rotated token %d for runner controller %d\n", token.ID, o.controllerID)
		o.io.LogInfof("Token: %s\n", token.Token)
		o.io.LogInfof("\n%s\n", c.Yellow("Warning: Save this token. You cannot view it again."))
		return nil
	}
}
