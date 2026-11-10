//go:build !integration

package cancel

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	autoflowrpcv1 "gitlab.com/gitlab-org/protos/gen/go/gitlab/autoflow/rpc/v1"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"gitlab.com/gitlab-org/cli/internal/testing/autoflowtest"
)

type cancelCall = autoflowtest.Call[*autoflowrpcv1.CancelWorkflowRequest]

func expectCancel(relay *autoflowtest.Relay, err error) <-chan cancelCall {
	calls := make(chan cancelCall, 1)
	var resp *autoflowrpcv1.CancelWorkflowResponse
	if err == nil {
		resp = &autoflowrpcv1.CancelWorkflowResponse{}
	}
	relay.Mock.EXPECT().CancelWorkflow(gomock.Any(), gomock.Any()).DoAndReturn(autoflowtest.Answer(calls, resp, err))
	return calls
}

func expectGet(relay *autoflowtest.Relay, states ...autoflowrpcv1.WorkflowState) {
	for _, state := range states {
		relay.Mock.EXPECT().GetWorkflow(gomock.Any(), gomock.Any()).Return(&autoflowrpcv1.GetWorkflowResponse{WorkflowKey: "wf-key", State: state}, nil)
	}
}

func TestCancel(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	calls := expectCancel(relay, nil)
	exec, token := autoflowtest.NewTestExec(t, NewCmd)

	out, err := exec("wf-key " + relay.Args() + " --workflow-token-file " + autoflowtest.WriteFile(t, "wf-token\n"))
	require.NoError(t, err)

	call := autoflowtest.Received(t, calls)
	assert.Equal(t, []string{"Bearer " + token}, call.Auth)
	assert.Equal(t, "wf-key", call.Req.GetWorkflowKey())
	assert.Equal(t, "wf-token", call.Req.GetWorkflowToken())
	wantNamespace := uuid.MustParse(autoflowtest.NamespaceID)
	assert.Equal(t, wantNamespace[:], call.Req.GetNamespaceId())
	assert.Equal(t, "Requested the cancellation of workflow wf-key.\n", out.OutBuf.String())
}

func TestCancel_Wait(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	expectCancel(relay, nil)
	expectGet(relay, autoflowrpcv1.WorkflowState_WORKFLOW_STATE_RUNNING, autoflowrpcv1.WorkflowState_WORKFLOW_STATE_CANCELED)
	exec, _ := autoflowtest.NewTestExec(t, NewCmd)

	out, err := exec("wf-key " + relay.Args() + " --workflow-token-file " + autoflowtest.WriteFile(t, "wf-token") + " --wait --poll-interval 1ms")
	require.NoError(t, err)
	assert.Equal(t, "Requested the cancellation of workflow wf-key.\nWorkflow wf-key is canceled.\n", out.OutBuf.String())
}

func TestCancel_WaitFinishedFirst(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	expectCancel(relay, nil)
	expectGet(relay, autoflowrpcv1.WorkflowState_WORKFLOW_STATE_COMPLETED)
	exec, _ := autoflowtest.NewTestExec(t, NewCmd)

	_, err := exec("wf-key " + relay.Args() + " --workflow-token-file " + autoflowtest.WriteFile(t, "wf-token") + " --wait")
	require.EqualError(t, err, "workflow finished in state COMPLETED")
}

func TestCancel_RelayError(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	expectCancel(relay, status.Error(codes.PermissionDenied, "workflow token does not match"))
	exec, _ := autoflowtest.NewTestExec(t, NewCmd)

	out, err := exec("wf-key " + relay.Args() + " --workflow-token-file " + autoflowtest.WriteFile(t, "wf-token") + " --wait")
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	assert.Contains(t, err.Error(), "failed to cancel workflow")
	assert.Empty(t, out.OutBuf.String())
}

func TestCancel_NoWorkflowToken(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	exec, _ := autoflowtest.NewTestExec(t, NewCmd)

	_, err := exec("wf-key " + relay.Args())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pass --workflow-token-file or set GLAB_AUTOFLOW_WORKFLOW_TOKEN")
}
