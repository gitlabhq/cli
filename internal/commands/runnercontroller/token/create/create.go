package create

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

	controllerID int64
	description  string
	outputFormat string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
	}

	cmd := &cobra.Command{
		Use:   "create <controller-id> [flags]",
		Short: `Create a token for a runner controller. (EXPERIMENTAL)`,
		Long: heredoc.Docf(`
			Store the token value securely before closing the terminal. You cannot
			retrieve the token again.
			%s`, text.ExperimentalString),
		Args: cobra.ExactArgs(1),
		Example: heredoc.Doc(`
			# Create a token for runner controller 42
			glab runner-controller token create 42

			# Create a token with a description
			glab runner-controller token create 42 --description "production"

			# Create a token and output as JSON
			glab runner-controller token create 42 --output json`),
		Annotations: map[string]string{
			mcpannotations.Exclude: "true",
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
	fl.StringVarP(&opts.description, "description", "d", "", "Description of the token.")

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

	createOpts := &gitlab.CreateRunnerControllerTokenOptions{}
	if o.description != "" {
		createOpts.Description = new(o.description)
	}

	token, _, err := client.RunnerControllerTokens.CreateRunnerControllerToken(o.controllerID, createOpts, gitlab.WithContext(ctx))
	if err != nil {
		return cmdutils.WrapError(err, "failed to create runner controller token")
	}

	switch o.outputFormat {
	case "json":
		return o.io.PrintJSON(token)
	default:
		c := o.io.Color()
		o.io.LogInfof("Created token %d for runner controller %d\n", token.ID, o.controllerID)
		o.io.LogInfof("Token: %s\n", token.Token)
		o.io.LogInfof("\n%s\n", c.Yellow("Warning: Save this token. You cannot view it again."))
		return nil
	}
}
