//go:build !integration

package get

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	autoflowrpcv1 "gitlab.com/gitlab-org/protos/gen/go/gitlab/autoflow/rpc/v1"
	autoflowv1 "gitlab.com/gitlab-org/protos/gen/go/gitlab/autoflow/v1"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"gitlab.com/gitlab-org/cli/internal/testing/autoflowtest"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
	"gitlab.com/gitlab-org/cli/internal/testing/tokenexchangetest"
)

type getCall = autoflowtest.Call[*autoflowrpcv1.GetWorkflowRequest]

var (
	createdAt = time.Date(2026, 11, 6, 14, 0, 0, 0, time.UTC)
	updatedAt = createdAt.Add(111 * time.Millisecond)
)

func workflowIn(state autoflowrpcv1.WorkflowState, result *autoflowv1.Result) *autoflowrpcv1.GetWorkflowResponse {
	return &autoflowrpcv1.GetWorkflowResponse{
		WorkflowKey:    "wf-key",
		Name:           "main",
		IdempotencyKey: "my-key",
		State:          state,
		CreatedAt:      timestamppb.New(createdAt),
		UpdatedAt:      timestamppb.New(updatedAt),
		Result:         result,
	}
}

var completed = workflowIn(autoflowrpcv1.WorkflowState_WORKFLOW_STATE_COMPLETED, &autoflowv1.Result{
	Result: &autoflowv1.Result_Value{Value: &autoflowv1.Value{Kind: &autoflowv1.Value_IntegerValue{IntegerValue: 1}}},
})

// expectGet makes the relay answer GetWorkflow with resps, in order.
func expectGet(relay *autoflowtest.Relay, resps ...*autoflowrpcv1.GetWorkflowResponse) <-chan getCall {
	calls := make(chan getCall, len(resps))
	for _, resp := range resps {
		relay.Mock.EXPECT().GetWorkflow(gomock.Any(), gomock.Any()).DoAndReturn(autoflowtest.Answer(calls, resp, nil))
	}
	return calls
}

func TestGet_Text(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	calls := expectGet(relay, completed)
	exec, token := autoflowtest.NewTestExec(t, NewCmd)

	out, err := exec("wf-key " + relay.Args() + " --workflow-token-file " + autoflowtest.WriteFile(t, "wf-token\n"))
	require.NoError(t, err)

	call := autoflowtest.Received(t, calls)
	assert.Equal(t, []string{"Bearer " + token}, call.Auth)
	assert.Equal(t, "wf-key", call.Req.GetWorkflowKey())
	assert.Equal(t, "wf-token", call.Req.GetWorkflowToken())
	wantNamespace := uuid.MustParse(autoflowtest.NamespaceID)
	assert.Equal(t, wantNamespace[:], call.Req.GetNamespaceId())

	assert.Equal(t, "workflow_key: wf-key\n"+
		"name: main\n"+
		"idempotency_key: my-key\n"+
		"state: COMPLETED\n"+
		"created_at: 2026-11-06T14:00:00Z\n"+
		"updated_at: 2026-11-06T14:00:00.111Z\n"+
		"value: 1\n",
		out.OutBuf.String())
}

func TestGet_TokenFromEnvironment(t *testing.T) {
	relay := autoflowtest.NewRelay(t)
	calls := expectGet(relay, completed)
	exec, _ := autoflowtest.NewTestExec(t, NewCmd)
	t.Setenv("GLAB_AUTOFLOW_WORKFLOW_TOKEN", "env-token")

	_, err := exec("wf-key " + relay.Args())
	require.NoError(t, err)
	assert.Equal(t, "env-token", autoflowtest.Received(t, calls).Req.GetWorkflowToken())
}

func TestGet_TokenFromStdin(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	calls := expectGet(relay, completed)
	exec, _ := autoflowtest.NewTestExec(t, NewCmd, cmdtest.WithStdin("stdin-token\n"))

	_, err := exec("wf-key " + relay.Args() + " --workflow-token-file -")
	require.NoError(t, err)
	assert.Equal(t, "stdin-token", autoflowtest.Received(t, calls).Req.GetWorkflowToken())
}

func TestGet_JSONFailed(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	expectGet(relay, workflowIn(autoflowrpcv1.WorkflowState_WORKFLOW_STATE_FAILED, &autoflowv1.Result{
		Result: &autoflowv1.Result_Error{Error: &autoflowv1.Error{Message: "boom"}},
	}))
	exec, _ := autoflowtest.NewTestExec(t, NewCmd)

	out, err := exec("wf-key " + relay.Args() + " --workflow-token-file " + autoflowtest.WriteFile(t, "wf-token") + " --output json")
	require.NoError(t, err, "without --wait a failed workflow is a successful read")

	var got map[string]any
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &got))
	assert.Equal(t, map[string]any{
		"workflow_key":    "wf-key",
		"name":            "main",
		"idempotency_key": "my-key",
		"state":           "FAILED",
		"created_at":      "2026-11-06T14:00:00Z",
		"updated_at":      "2026-11-06T14:00:00.111Z",
		"error":           "boom",
	}, got)
}

func TestGet_WaitUntilCompleted(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	calls := expectGet(relay, workflowIn(autoflowrpcv1.WorkflowState_WORKFLOW_STATE_RUNNING, nil), completed)
	exec, _ := autoflowtest.NewTestExec(t, NewCmd)

	out, err := exec("wf-key " + relay.Args() + " --workflow-token-file " + autoflowtest.WriteFile(t, "wf-token") + " --wait --poll-interval 1ms")
	require.NoError(t, err)
	assert.Len(t, calls, 2)
	assert.Contains(t, out.OutBuf.String(), "state: COMPLETED\n")
}

func TestGet_WaitUntilCanceled(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	expectGet(relay, workflowIn(autoflowrpcv1.WorkflowState_WORKFLOW_STATE_CANCELED, nil))
	exec, _ := autoflowtest.NewTestExec(t, NewCmd)

	out, err := exec("wf-key " + relay.Args() + " --workflow-token-file " + autoflowtest.WriteFile(t, "wf-token") + " --wait")
	require.EqualError(t, err, "workflow finished in state CANCELED")
	assert.Contains(t, out.OutBuf.String(), "state: CANCELED\n")
}

func TestGet_ValueFormatProtojson(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	expectGet(relay, completed)
	exec, _ := autoflowtest.NewTestExec(t, NewCmd)

	out, err := exec("wf-key " + relay.Args() + " --workflow-token-file " + autoflowtest.WriteFile(t, "wf-token") + " --value-format protojson --output json --jq .value")
	require.NoError(t, err)
	assert.JSONEq(t, `{"integer_value":"1"}`, out.OutBuf.String())
}

func TestGet_WaitRetriesTransientFailures(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	gomock.InOrder(
		relay.Mock.EXPECT().GetWorkflow(gomock.Any(), gomock.Any()).Return(nil, status.Error(codes.Unavailable, "relay restarting")),
		relay.Mock.EXPECT().GetWorkflow(gomock.Any(), gomock.Any()).Return(&autoflowrpcv1.GetWorkflowResponse{
			WorkflowKey: "wf-key",
			State:       autoflowrpcv1.WorkflowState_WORKFLOW_STATE_COMPLETED,
		}, nil),
	)
	exec, _ := autoflowtest.NewTestExec(t, NewCmd)

	out, err := exec("wf-key " + relay.Args() + " --workflow-token-file " + autoflowtest.WriteFile(t, "wf-token") + " --wait --poll-interval 1ms")
	require.NoError(t, err)
	assert.Equal(t, "workflow_key: wf-key\nname: \nidempotency_key: \nstate: COMPLETED\n", out.OutBuf.String(), "a missing timestamp is omitted")
}

func TestGet_WaitGivesUpOnOtherFailures(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	relay.Mock.EXPECT().GetWorkflow(gomock.Any(), gomock.Any()).Return(nil, status.Error(codes.PermissionDenied, "workflow token does not match"))
	exec, _ := autoflowtest.NewTestExec(t, NewCmd)

	_, err := exec("wf-key " + relay.Args() + " --workflow-token-file " + autoflowtest.WriteFile(t, "wf-token") + " --wait --poll-interval 1ms")
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestGet_WaitGivesUpAfterRepeatedTransientFailures(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	relay.Mock.EXPECT().GetWorkflow(gomock.Any(), gomock.Any()).Return(nil, status.Error(codes.Unavailable, "relay down")).Times(6)
	exec, _ := autoflowtest.NewTestExec(t, NewCmd)

	_, err := exec("wf-key " + relay.Args() + " --workflow-token-file " + autoflowtest.WriteFile(t, "wf-token") + " --wait --poll-interval 1ms")
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))
}

func TestGet_WaitEndsOnUnknownState(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	expectGet(relay, workflowIn(autoflowrpcv1.WorkflowState(99), nil))
	exec, _ := autoflowtest.NewTestExec(t, NewCmd)

	_, err := exec("wf-key " + relay.Args() + " --workflow-token-file " + autoflowtest.WriteFile(t, "wf-token") + " --wait")
	require.EqualError(t, err, "workflow finished in state 99")
}

func TestGet_RelayError(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	relay.Mock.EXPECT().GetWorkflow(gomock.Any(), gomock.Any()).Return(nil, status.Error(codes.PermissionDenied, "namespace does not match the token's organization"))
	exec, _ := autoflowtest.NewTestExec(t, NewCmd)

	out, err := exec("wf-key " + relay.Args() + " --workflow-token-file " + autoflowtest.WriteFile(t, "wf-token"))
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	assert.Contains(t, err.Error(), "failed to get workflow")
	assert.Empty(t, out.OutBuf.String())
}

func TestGet_FlagErrors(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	tests := []struct {
		name    string
		args    string
		wantErr string
	}{
		{
			name:    "no workflow key",
			args:    relay.Args() + " --workflow-token-file " + autoflowtest.WriteFile(t, "wf-token"),
			wantErr: "accepts 1 arg(s), received 0",
		},
		{
			name:    "no workflow token",
			args:    "wf-key " + relay.Args(),
			wantErr: "pass --workflow-token-file or set GLAB_AUTOFLOW_WORKFLOW_TOKEN",
		},
		{
			name:    "empty workflow token",
			args:    "wf-key " + relay.Args() + " --workflow-token-file " + autoflowtest.WriteFile(t, " \n"),
			wantErr: "the workflow token is empty",
		},
		{
			name:    "missing workflow token file",
			args:    "wf-key " + relay.Args() + " --workflow-token-file /nonexistent/token",
			wantErr: "failed to read --workflow-token-file",
		},
		{
			name:    "no namespace ID",
			args:    "wf-key --address " + relay.Address,
			wantErr: `required flag(s) "namespace-id" not set`,
		},
		{
			name:    "poll interval not positive",
			args:    "wf-key " + relay.Args() + " --workflow-token-file " + autoflowtest.WriteFile(t, "wf-token") + " --wait --poll-interval 0s",
			wantErr: "--poll-interval must be positive, got 0s",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var gotBody tokenexchangetest.WireRequest
			srv, count, _ := autoflowtest.NewTokenServer(t, &gotBody)
			exec := tokenexchangetest.NewTestExec(t, srv, NewCmd)

			_, err := exec(tc.args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
			assert.EqualValues(t, 0, count.Load())
		})
	}
}
