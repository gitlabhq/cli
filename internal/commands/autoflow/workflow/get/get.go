// Package get implements `glab autoflow workflow get`.
package get

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	autoflowrpcv1 "gitlab.com/gitlab-org/protos/gen/go/gitlab/autoflow/rpc/v1"
	"google.golang.org/protobuf/types/known/timestamppb"

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

	conn          autoflowutils.Connection
	workflowToken *cmdutils.SecretFile
	wait          autoflowutils.Wait
	valueFormat   string
	outputFormat  string

	workflowKey string
}

// NewCmd returns the `glab autoflow workflow get` command.
func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
	}

	cmd := &cobra.Command{
		Use:   "get <workflow-key>",
		Short: "Get the state and result of an AutoFlow workflow. (EXPERIMENTAL)",
		Long: heredoc.Docf(`
			Get the state of a workflow on the AutoFlow service of GitLab Relay and, once it has finished, its result.

			The command needs the workflow token that %[1]sglab autoflow workflow start%[1]s printed. Pass it in a file with %[1]s--workflow-token-file%[1]s or in its environment variable.

			With %[1]s--wait%[1]s, the command polls until the workflow has finished and exits with an error unless it completed.

			The result value is printed as JSON: %[1]sNone%[1]s becomes %[1]snull%[1]s, an integer or a float a number, a list an array and a dict with string keys an object. A value without a JSON equivalent, such as a tuple, bytes or a channel, is printed in the protobuf JSON form of %[1]sgitlab.autoflow.v1.Value%[1]s. %[1]s--value-format protojson%[1]s prints the whole value in that form.
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Wait until a workflow on Runway staging has finished
			glab autoflow workflow get "$WORKFLOW_KEY" --hostname staging.gitlab.com \
			  --address staging.autoflow-gke.svc.gitlab.net:443 \
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
	cmd.Flags().Var(
		cmdutils.NewEnumValue([]string{"json", "protojson"}, "json", &opts.valueFormat),
		"value-format", "Format of the result value: json, protojson.",
	)
	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)

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

	client, conn, err := o.conn.Dial(ctx, o.io, o.apiClient)
	if err != nil {
		return err
	}
	defer conn.Close()

	req := &autoflowrpcv1.GetWorkflowRequest{
		WorkflowKey:   o.workflowKey,
		WorkflowToken: &token,
		NamespaceId:   o.conn.NamespaceID[:],
	}
	var resp *autoflowrpcv1.GetWorkflowResponse
	if o.wait.Enabled {
		resp, err = o.wait.Until(ctx, client, req)
	} else {
		resp, err = autoflowutils.GetWorkflow(ctx, client, req)
	}
	if err != nil {
		return err
	}

	if err := o.print(resp); err != nil {
		return err
	}
	if o.wait.Enabled && resp.GetState() != autoflowrpcv1.WorkflowState_WORKFLOW_STATE_COMPLETED {
		return fmt.Errorf("workflow finished in state %s", autoflowutils.State(resp.GetState()))
	}
	return nil
}

type workflow struct {
	WorkflowKey    string          `json:"workflow_key"`
	Name           string          `json:"name"`
	IdempotencyKey string          `json:"idempotency_key"`
	State          string          `json:"state"`
	CreatedAt      *time.Time      `json:"created_at,omitempty"`
	UpdatedAt      *time.Time      `json:"updated_at,omitempty"`
	Value          json.RawMessage `json:"value,omitempty"`
	Error          string          `json:"error,omitempty"`
}

func (o *options) print(resp *autoflowrpcv1.GetWorkflowResponse) error {
	w := workflow{
		WorkflowKey:    resp.GetWorkflowKey(),
		Name:           resp.GetName(),
		IdempotencyKey: resp.GetIdempotencyKey(),
		State:          autoflowutils.State(resp.GetState()),
		CreatedAt:      timestamp(resp.GetCreatedAt()),
		UpdatedAt:      timestamp(resp.GetUpdatedAt()),
		Error:          resp.GetResult().GetError().GetMessage(),
	}
	if v := resp.GetResult().GetValue(); v != nil {
		marshal := autoflow.MarshalValue
		if o.valueFormat == "protojson" {
			marshal = autoflow.MarshalProtoValue
		}
		b, err := marshal(v)
		if err != nil {
			return fmt.Errorf("failed to encode the workflow result: %w", err)
		}
		w.Value = b
	}

	switch o.outputFormat {
	case "json":
		return o.io.PrintJSON(w)
	default:
		o.printText(w)
		return nil
	}
}

func (o *options) printText(w workflow) {
	o.io.LogInfof("workflow_key: %s\nname: %s\nidempotency_key: %s\nstate: %s\n", w.WorkflowKey, w.Name, w.IdempotencyKey, w.State)
	if w.CreatedAt != nil {
		o.io.LogInfof("created_at: %s\n", w.CreatedAt.Format(time.RFC3339Nano))
	}
	if w.UpdatedAt != nil {
		o.io.LogInfof("updated_at: %s\n", w.UpdatedAt.Format(time.RFC3339Nano))
	}
	if w.Value != nil {
		o.io.LogInfof("value: %s\n", w.Value)
	}
	if w.Error != "" {
		o.io.LogInfof("error: %s\n", w.Error)
	}
}

func timestamp(ts *timestamppb.Timestamp) *time.Time {
	if ts == nil {
		return nil
	}
	t := ts.AsTime()
	return &t
}
