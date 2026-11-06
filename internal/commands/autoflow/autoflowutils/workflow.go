package autoflowutils

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	autoflowrpcv1 "gitlab.com/gitlab-org/protos/gen/go/gitlab/autoflow/rpc/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
)

// State returns the name of s without its enum prefix, as the commands print
// it.
func State(s autoflowrpcv1.WorkflowState) string {
	return strings.TrimPrefix(s.String(), "WORKFLOW_STATE_")
}

// isTerminal reports whether a workflow in state s can no longer change.
func isTerminal(s autoflowrpcv1.WorkflowState) bool {
	switch s {
	case autoflowrpcv1.WorkflowState_WORKFLOW_STATE_UNSPECIFIED,
		autoflowrpcv1.WorkflowState_WORKFLOW_STATE_RUNNING:
		return false
	default:
		// A state this version does not know yet ends the wait rather than
		// polling forever.
		return true
	}
}

const maxTransientFailures = 5

func isTransient(err error) bool {
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded:
		return true
	default:
		return false
	}
}

// GetWorkflow reads the workflow req addresses.
func GetWorkflow(ctx context.Context, client autoflowrpcv1.AutoFlowServiceClient, req *autoflowrpcv1.GetWorkflowRequest) (*autoflowrpcv1.GetWorkflowResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()
	resp, err := client.GetWorkflow(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get workflow: %w", err)
	}
	return resp, nil
}

// Wait is the set of flags that makes a command poll a workflow until it has
// finished.
type Wait struct {
	Enabled  bool
	interval time.Duration
}

// AddFlags registers --wait and --poll-interval on cmd.
func (w *Wait) AddFlags(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.BoolVar(&w.Enabled, "wait", false, "Poll until the workflow has finished.")
	fl.DurationVar(&w.interval, "poll-interval", 2*time.Second, "How often to poll the workflow with --wait.")
}

// Validate checks the poll interval.
func (w *Wait) Validate() error {
	if w.interval <= 0 {
		return &cmdutils.FlagError{Err: fmt.Errorf("--poll-interval must be positive, got %s", w.interval)}
	}
	return nil
}

// Until reads the workflow req addresses every poll interval until it has
// finished. It keeps polling through up to maxTransientFailures consecutive
// failures that a retry can fix, such as a restarting Relay.
func (w *Wait) Until(ctx context.Context, client autoflowrpcv1.AutoFlowServiceClient, req *autoflowrpcv1.GetWorkflowRequest) (*autoflowrpcv1.GetWorkflowResponse, error) {
	failures := 0
	for {
		resp, err := GetWorkflow(ctx, client, req)
		switch {
		case err == nil && isTerminal(resp.GetState()):
			return resp, nil
		case err == nil:
			failures = 0
		case ctx.Err() == nil && isTransient(err) && failures < maxTransientFailures:
			failures++
		default:
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(w.interval):
		}
	}
}
