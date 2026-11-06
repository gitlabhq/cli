// Package send implements `glab autoflow workflow send`.
package send

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	autoflowrpcv1 "gitlab.com/gitlab-org/protos/gen/go/gitlab/autoflow/rpc/v1"
	autoflowv1 "gitlab.com/gitlab-org/protos/gen/go/gitlab/autoflow/v1"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/api/autoflow"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/autoflow/autoflowutils"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type options struct {
	io        *iostreams.IOStreams
	apiClient func(repoHost string) (*api.Client, error)

	conn           autoflowutils.Connection
	workflowToken  *cmdutils.SecretFile
	channelToken   *cmdutils.SecretFile
	value          string
	idempotencyKey string

	parsedValue *autoflowv1.Value
}

// NewCmd returns the `glab autoflow workflow send` command.
func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
	}

	cmd := &cobra.Command{
		Use:   "send",
		Short: "Send a value to a channel of an AutoFlow workflow. (EXPERIMENTAL)",
		Long: heredoc.Docf(`
			Send a value to a channel of a workflow on the AutoFlow service of GitLab Relay.

			The channel token addresses the channel: %[1]sglab autoflow workflow start --channel%[1]s prints one for each channel it creates, and modules hand out more while the workflow runs. The workflow token of the same workflow authorizes the send. Pass each token in a file with %[1]s--channel-token-file%[1]s and %[1]s--workflow-token-file%[1]s, or in their environment variables.

			%[1]s--value%[1]s is JSON, converted like the keyword arguments of %[1]sglab autoflow workflow start%[1]s. Sensitive values are not supported yet.

			AutoFlow delivers a value at most once per idempotency key. To retry a send without delivering the value twice, pass the same %[1]s--idempotency-key%[1]s.
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Send a string to a channel of a workflow on Runway staging
			glab autoflow workflow send --hostname staging.gitlab.com \
			  --address staging.autoflow-gke.svc.gitlab.net:443 \
			  --namespace-id 00000000-0000-0000-0000-000000000001 \
			  --workflow-token-file workflow-token.txt --channel-token-file inbox-token.txt \
			  --value '"hello"'
		`),
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		Annotations: map[string]string{
			mcpannotations.Exclude: "true",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := opts.complete(); err != nil {
				return err
			}
			if err := opts.validate(); err != nil {
				return err
			}
			return opts.run(cmd.Context())
		},
	}

	opts.conn.AddFlags(cmd)
	opts.workflowToken = autoflowutils.NewWorkflowTokenFlag(cmd)
	opts.channelToken = autoflowutils.NewChannelTokenFlag(cmd)
	fl := cmd.Flags()
	fl.StringVar(&opts.value, "value", "", "Value to send, as JSON.")
	fl.StringVar(&opts.idempotencyKey, "idempotency-key", "", "Key that deduplicates deliveries to the workflow. Defaults to a random key.")
	cobra.CheckErr(cmd.MarkFlagRequired("value"))

	return cmd
}

func (o *options) complete() error {
	if err := o.conn.Complete(); err != nil {
		return err
	}
	value, err := autoflow.ParseValue([]byte(o.value))
	if err != nil {
		return &cmdutils.FlagError{Err: fmt.Errorf("error parsing --value: %w", err)}
	}
	o.parsedValue = value
	if o.idempotencyKey == "" {
		o.idempotencyKey = "glab-" + rand.Text()
	}
	return nil
}

func (o *options) validate() error {
	if o.workflowToken.FromStdin() && o.channelToken.FromStdin() {
		return &cmdutils.FlagError{Err: errors.New("only one of --workflow-token-file and --channel-token-file can read from standard input")}
	}
	return nil
}

func (o *options) run(ctx context.Context) error {
	workflowToken, err := o.workflowToken.Read(o.io.In)
	if err != nil {
		return err
	}
	channelToken, err := o.channelToken.Read(o.io.In)
	if err != nil {
		return err
	}

	client, conn, err := o.conn.Dial(ctx, o.apiClient)
	if err != nil {
		return err
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(ctx, autoflowutils.CallTimeout)
	defer cancel()
	_, err = client.SendToWorkflowChannel(ctx, &autoflowrpcv1.SendToWorkflowChannelRequest{
		IdempotencyKey: o.idempotencyKey,
		ChannelToken:   channelToken,
		Value:          o.parsedValue,
		WorkflowToken:  &workflowToken,
		NamespaceId:    o.conn.NamespaceID[:],
	})
	if err != nil {
		// AutoFlow may have delivered the value before the call failed.
		return fmt.Errorf("failed to send the value (to retry without delivering it twice, pass --idempotency-key %s): %w", o.idempotencyKey, err)
	}
	o.io.LogInfof("Sent the value with idempotency key %s.\n", o.idempotencyKey)
	return nil
}
