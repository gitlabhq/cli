//go:build !integration

package send

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	autoflowrpcv1 "gitlab.com/gitlab-org/protos/gen/go/gitlab/autoflow/rpc/v1"
	autoflowv1 "gitlab.com/gitlab-org/protos/gen/go/gitlab/autoflow/v1"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"gitlab.com/gitlab-org/cli/internal/testing/autoflowtest"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

type sendCall = autoflowtest.Call[*autoflowrpcv1.SendToWorkflowChannelRequest]

func expectSend(relay *autoflowtest.Relay, err error) <-chan sendCall {
	calls := make(chan sendCall, 1)
	var resp *autoflowrpcv1.SendToWorkflowChannelResponse
	if err == nil {
		resp = &autoflowrpcv1.SendToWorkflowChannelResponse{}
	}
	relay.Mock.EXPECT().SendToWorkflowChannel(gomock.Any(), gomock.Any()).DoAndReturn(autoflowtest.Answer(calls, resp, err))
	return calls
}

func tokenArgs(t *testing.T) string {
	t.Helper()
	return " --workflow-token-file " + autoflowtest.WriteFile(t, "wf-token\n") + " --channel-token-file " + autoflowtest.WriteFile(t, "ch-token\n")
}

func TestSend(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	calls := expectSend(relay, nil)
	exec, token := autoflowtest.NewTestExec(t, NewCmd)

	out, err := exec(relay.Args() + tokenArgs(t) + ` --value '["a", 1]'`)
	require.NoError(t, err)

	call := autoflowtest.Received(t, calls)
	assert.Equal(t, []string{"Bearer " + token}, call.Auth)
	assert.Equal(t, "wf-token", call.Req.GetWorkflowToken())
	assert.Equal(t, "ch-token", call.Req.GetChannelToken())
	wantNamespace := uuid.MustParse(autoflowtest.NamespaceID)
	assert.Equal(t, wantNamespace[:], call.Req.GetNamespaceId())
	want := &autoflowv1.Value{Kind: &autoflowv1.Value_ListValue{ListValue: &autoflowv1.ListValue{Values: []*autoflowv1.Value{
		{Kind: &autoflowv1.Value_StringValue{StringValue: "a"}},
		{Kind: &autoflowv1.Value_IntegerValue{IntegerValue: 1}},
	}}}}
	assert.True(t, proto.Equal(want, call.Req.GetValue()), "got %v", call.Req.GetValue())
	key := call.Req.GetIdempotencyKey()
	assert.True(t, strings.HasPrefix(key, "glab-"), key)
	assert.Equal(t, "Sent the value with idempotency key "+key+".\n", out.OutBuf.String())
}

func TestSend_ChannelTokenFromStdin(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	calls := expectSend(relay, nil)
	exec, _ := autoflowtest.NewTestExec(t, NewCmd, cmdtest.WithStdin("stdin-token\n"))

	_, err := exec(relay.Args() + " --workflow-token-file " + autoflowtest.WriteFile(t, "wf-token") + " --channel-token-file - --value 1 --idempotency-key my-key")
	require.NoError(t, err)

	call := autoflowtest.Received(t, calls)
	assert.Equal(t, "stdin-token", call.Req.GetChannelToken())
	assert.Equal(t, "my-key", call.Req.GetIdempotencyKey())
}

func TestSend_RelayError(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	expectSend(relay, status.Error(codes.Unavailable, "connection reset"))
	exec, _ := autoflowtest.NewTestExec(t, NewCmd)

	_, err := exec(relay.Args() + tokenArgs(t) + " --value 1 --idempotency-key my-key")
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))
	assert.Contains(t, err.Error(), "failed to send the value (to retry without delivering it twice, pass --idempotency-key my-key)")
}

func TestSend_FlagErrors(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	workflowTokenFile := autoflowtest.WriteFile(t, "wf-token")
	tests := []struct {
		name    string
		args    string
		wantErr string
	}{
		{name: "no value", args: tokenArgs(t), wantErr: `required flag(s) "value" not set`},
		{name: "invalid value", args: tokenArgs(t) + " --value nope", wantErr: "error parsing --value: invalid character"},
		{name: "no channel token", args: " --workflow-token-file " + workflowTokenFile + " --value 1", wantErr: "pass --channel-token-file or set GLAB_AUTOFLOW_CHANNEL_TOKEN"},
		{name: "both tokens from stdin", args: " --workflow-token-file - --channel-token-file - --value 1", wantErr: "only one of --workflow-token-file and --channel-token-file can read from standard input"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			exec, _ := autoflowtest.NewTestExec(t, NewCmd)
			_, err := exec(relay.Args() + tc.args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}
