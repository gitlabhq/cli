// Package cancel implements `glab autoflow workflow cancel`.
package cancel

import (
	"context"
	"fmt"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	autoflowrpcv1 "gitlab.com/gitlab-org/protos/gen/go/gitlab/autoflow/rpc/v1"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/autoflow/autoflowutils"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type options struct {
	io        *iostreams.IOStreams
	apiClient func(repoHost string) (*api.Client, error)

	conn          autoflowutils.Connection
	workflowToken *cmdutils.SecretFile
	wait          autoflowutils.Wait

	workflowKey string
}

// NewCmd returns the `glab autoflow workflow cancel` command.
func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
	}

	cmd := &cobra.Command{
		Use:   "cancel <workflow-key>",
		Short: "Cancel an AutoFlow workflow. (EXPERIMENTAL)",
		Long: heredoc.Docf(`
			Request the cancellation of a workflow on the AutoFlow service of GitLab Relay. The cancellation is asynchronous: the command returns once AutoFlow accepted the request.

			The command needs the workflow token that %[1]sglab autoflow workflow start%[1]s printed. Pass it in a file with %[1]s--workflow-token-file%[1]s or in its environment variable.

			With %[1]s--wait%[1]s, the command polls until the workflow has finished and exits with an error unless it ended canceled. A workflow that finished before the cancellation reached it keeps its state.
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Cancel a workflow on Runway staging and wait until it is canceled
			glab autoflow workflow cancel "$WORKFLOW_KEY" --hostname staging.gitlab.com \
			  --address staging.autoflow-gke.svc.gitlab.net:443 \
			  --namespace-id 00000000-0000-0000-0000-000000000001 \
			  --workflow-token-file workflow-token.txt --wait
		`),
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: cobra.NoFileCompletions,
		Annotations: map[string]string{
			mcpannotations.Exclude: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(args); err != nil {
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
	opts.wait.AddFlags(cmd)

	return cmd
}

func (o *options) complete(args []string) error {
	o.workflowKey = args[0]
	return o.conn.Complete()
}

func (o *options) validate() error {
	return o.wait.Validate()
}

func (o *options) run(ctx context.Context) error {
	token, err := o.workflowToken.Read(o.io.In)
	if err != nil {
		return err
	}

	client, conn, err := o.conn.Dial(ctx, o.apiClient)
	if err != nil {
		return err
	}
	defer conn.Close()

	callCtx, cancel := context.WithTimeout(ctx, autoflowutils.CallTimeout)
	defer cancel()
	_, err = client.CancelWorkflow(callCtx, &autoflowrpcv1.CancelWorkflowRequest{
		WorkflowKey:   o.workflowKey,
		WorkflowToken: &token,
		NamespaceId:   o.conn.NamespaceID[:],
	})
	if err != nil {
		return fmt.Errorf("failed to cancel workflow: %w", err)
	}
	o.io.LogInfof("Requested the cancellation of workflow %s.\n", o.workflowKey)

	if !o.wait.Enabled {
		return nil
	}
	resp, err := o.wait.Until(ctx, client, &autoflowrpcv1.GetWorkflowRequest{
		WorkflowKey:   o.workflowKey,
		WorkflowToken: &token,
		NamespaceId:   o.conn.NamespaceID[:],
	})
	if err != nil {
		return err
	}
	// Asserts the state, not who caused it: a workflow someone else canceled
	// counts.
	if resp.GetState() != autoflowrpcv1.WorkflowState_WORKFLOW_STATE_CANCELED {
		return fmt.Errorf("workflow finished in state %s", autoflowutils.State(resp.GetState()))
	}
	o.io.LogInfof("Workflow %s is canceled.\n", o.workflowKey)
	return nil
}
