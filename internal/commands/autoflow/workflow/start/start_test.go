//go:build !integration

package start

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
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
	"gitlab.com/gitlab-org/cli/internal/testing/tokenexchangetest"
)

type startCall = autoflowtest.Call[*autoflowrpcv1.StartWorkflowRequest]

func expectStart(relay *autoflowtest.Relay, resp *autoflowrpcv1.StartWorkflowResponse, err error) <-chan startCall {
	calls := make(chan startCall, 1)
	relay.Mock.EXPECT().StartWorkflow(gomock.Any(), gomock.Any()).DoAndReturn(autoflowtest.Answer(calls, resp, err))
	return calls
}

var startedResponse = &autoflowrpcv1.StartWorkflowResponse{WorkflowKey: "wf-key", WorkflowToken: "wf-token"}

func newTestExec(t *testing.T, srv *httptest.Server, opts ...cmdtest.FactoryOption) cmdtest.CmdExecFunc {
	t.Helper()
	return tokenexchangetest.NewTestExec(t, srv, NewCmd, opts...)
}

func TestStart_Text(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	calls := expectStart(relay, startedResponse, nil)
	var gotBody tokenexchangetest.WireRequest
	srv, count, token := autoflowtest.NewTokenServer(t, &gotBody)
	exec := newTestExec(t, srv)

	out, err := exec(relay.Args() + " --script-file " + autoflowtest.WriteFile(t, "def main(): return 1\n"))
	require.NoError(t, err)

	assert.EqualValues(t, 1, count.Load())
	assert.Equal(t, tokenexchangetest.WireRequest{Audience: "gitlab-autoflow", ExpiresIn: 60}, gotBody)

	call := autoflowtest.Received(t, calls)
	gotReq := call.Req
	assert.Equal(t, []string{"Bearer " + token}, call.Auth)
	assert.Equal(t, []byte("def main(): return 1\n"), gotReq.GetWorkflowDefinition())
	wantNamespace := uuid.MustParse(autoflowtest.NamespaceID)
	assert.Equal(t, wantNamespace[:], gotReq.GetNamespaceId())
	assert.Len(t, gotReq.GetTokenBinding(), 64)
	assert.True(t, strings.HasPrefix(gotReq.GetIdempotencyKey(), "glab-"), gotReq.GetIdempotencyKey())

	assert.Equal(t, "workflow_key: wf-key\n"+
		"workflow_token: wf-token\n"+
		"namespace_id: "+autoflowtest.NamespaceID+"\n"+
		"idempotency_key: "+gotReq.GetIdempotencyKey()+"\n"+
		"token_binding: "+base64.StdEncoding.EncodeToString(gotReq.GetTokenBinding())+"\n",
		out.OutBuf.String())
	assert.Empty(t, out.ErrBuf.String())
}

func TestStart_JSONWithIdempotencyKey(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	calls := expectStart(relay, startedResponse, nil)
	var gotBody tokenexchangetest.WireRequest
	srv, _, _ := autoflowtest.NewTokenServer(t, &gotBody)
	exec := newTestExec(t, srv)

	out, err := exec(relay.Args() + ` --script 'def main(): return 1' --idempotency-key my-key --output json`)
	require.NoError(t, err)

	gotReq := autoflowtest.Received(t, calls).Req
	assert.Equal(t, "my-key", gotReq.GetIdempotencyKey())
	assert.Equal(t, []byte("def main(): return 1"), gotReq.GetWorkflowDefinition())

	var got struct {
		WorkflowKey    string `json:"workflow_key"`
		WorkflowToken  string `json:"workflow_token"`
		NamespaceID    string `json:"namespace_id"`
		IdempotencyKey string `json:"idempotency_key"`
		TokenBinding   string `json:"token_binding"`
	}
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &got))
	assert.Equal(t, "wf-key", got.WorkflowKey)
	assert.Equal(t, "wf-token", got.WorkflowToken)
	assert.Equal(t, autoflowtest.NamespaceID, got.NamespaceID)
	assert.Equal(t, "my-key", got.IdempotencyKey)
	assert.Equal(t, base64.StdEncoding.EncodeToString(gotReq.GetTokenBinding()), got.TokenBinding)
}

func TestStart_ScriptFromStdin(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	calls := expectStart(relay, startedResponse, nil)
	var gotBody tokenexchangetest.WireRequest
	srv, _, _ := autoflowtest.NewTokenServer(t, &gotBody)
	exec := newTestExec(t, srv, cmdtest.WithStdin("def main(): return 2\n"))

	_, err := exec(relay.Args() + " --script-file -")
	require.NoError(t, err)

	gotReq := autoflowtest.Received(t, calls).Req
	assert.Equal(t, []byte("def main(): return 2\n"), gotReq.GetWorkflowDefinition())
}

func TestStart_FlagErrors(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	tests := []struct {
		name    string
		args    string
		wantErr string
	}{
		{
			name:    "no script",
			args:    relay.Args(),
			wantErr: "at least one of the flags in the group [script script-file] is required",
		},
		{
			name:    "script and script file",
			args:    relay.Args() + " --script x --script-file y",
			wantErr: "if any flags in the group [script script-file] are set none of the others can be",
		},
		{
			name:    "no address",
			args:    "--namespace-id " + autoflowtest.NamespaceID + " --script x",
			wantErr: `required flag(s) "address" not set`,
		},
		{
			name:    "invalid namespace ID",
			args:    "--address " + relay.Address + " --namespace-id 42 --script x",
			wantErr: "error parsing --namespace-id",
		},
		{
			name:    "nil namespace ID",
			args:    "--address " + relay.Address + " --namespace-id 00000000-0000-0000-0000-000000000000 --script x",
			wantErr: "must not be the nil UUID",
		},
		{
			name:    "token binding not base64",
			args:    relay.Args() + " --script x --idempotency-key k --token-binding-file " + autoflowtest.WriteFile(t, "!!"),
			wantErr: "error parsing the token binding",
		},
		{
			name:    "token binding too short",
			args:    relay.Args() + " --script x --idempotency-key k --token-binding-file " + autoflowtest.WriteFile(t, base64.StdEncoding.EncodeToString(make([]byte, 63))),
			wantErr: "must decode to 64 bytes, got 63",
		},
		{
			name:    "token binding without idempotency key",
			args:    relay.Args() + " --script x --token-binding-file " + autoflowtest.WriteFile(t, base64.StdEncoding.EncodeToString(make([]byte, 64))),
			wantErr: "a token binding re-submits an earlier workflow, so it needs the --idempotency-key of that workflow",
		},
		{
			name:    "script and token binding from stdin",
			args:    relay.Args() + " --script-file - --token-binding-file -",
			wantErr: "only one of --script-file and --token-binding-file can read from standard input",
		},
		{
			name:    "empty script",
			args:    relay.Args() + " --script ''",
			wantErr: "the workflow definition is empty",
		},
		{
			name:    "missing script file",
			args:    relay.Args() + " --script-file /nonexistent/main.star",
			wantErr: "failed to read the workflow definition",
		},
		{
			name:    "invalid hostname",
			args:    relay.Args() + " --script x --hostname https://gitlab.example.com",
			wantErr: "error parsing --hostname",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var gotBody tokenexchangetest.WireRequest
			srv, count, _ := autoflowtest.NewTokenServer(t, &gotBody)
			exec := newTestExec(t, srv)

			_, err := exec(tc.args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
			assert.EqualValues(t, 0, count.Load())
		})
	}
}

func TestStart_AudienceNotEnabled(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	srv, count := tokenexchangetest.NewTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"audience does not have a valid value"}`))
	})
	exec := newTestExec(t, srv)

	_, err := exec(relay.Args() + " --script x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "audience does not have a valid value")
	assert.Contains(t, err.Error(), "token_exchange_autoflow_audience feature flag")
	assert.EqualValues(t, 1, count.Load())
}

// The test sets TMPDIR, where the command saves a generated binding, so it
// cannot run in parallel.
func TestStart_RelayErrorSavesGeneratedBinding(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	relay := autoflowtest.NewRelay(t)
	calls := expectStart(relay, nil, status.Error(codes.DeadlineExceeded, "context deadline exceeded"))
	var gotBody tokenexchangetest.WireRequest
	srv, _, _ := autoflowtest.NewTokenServer(t, &gotBody)
	exec := newTestExec(t, srv)

	out, err := exec(relay.Args() + " --script x")
	require.Error(t, err)
	assert.Equal(t, codes.DeadlineExceeded, status.Code(err))
	assert.Empty(t, out.OutBuf.String())
	gotReq := autoflowtest.Received(t, calls).Req
	m := regexp.MustCompile(`^failed to start workflow \(to retry without starting a second one, pass --idempotency-key (\S+) and --token-binding-file "([^"]+)"\): `).FindStringSubmatch(err.Error())
	require.NotNil(t, m, err.Error())
	assert.Equal(t, gotReq.GetIdempotencyKey(), m[1])
	info, err := os.Stat(m[2])
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	// The hint is the retry.
	retries := expectStart(relay, startedResponse, nil)
	_, err = exec(relay.Args() + " --script x --idempotency-key " + m[1] + " --token-binding-file " + m[2])
	require.NoError(t, err)
	retry := autoflowtest.Received(t, retries).Req
	assert.Equal(t, gotReq.GetIdempotencyKey(), retry.GetIdempotencyKey())
	assert.Equal(t, gotReq.GetTokenBinding(), retry.GetTokenBinding())
}

// The test sets TMPDIR, where the command would save a generated binding, so
// it cannot run in parallel.
func TestStart_RelayRejectionHasNoRetryHint(t *testing.T) {
	for _, code := range []codes.Code{codes.Unauthenticated, codes.PermissionDenied, codes.InvalidArgument, codes.FailedPrecondition, codes.ResourceExhausted} {
		t.Run(code.String(), func(t *testing.T) {
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)
			relay := autoflowtest.NewRelay(t)
			expectStart(relay, nil, status.Error(code, "rejected"))
			var gotBody tokenexchangetest.WireRequest
			srv, _, _ := autoflowtest.NewTokenServer(t, &gotBody)
			exec := newTestExec(t, srv)

			_, err := exec(relay.Args() + " --script x")
			require.Error(t, err)
			assert.Equal(t, code, status.Code(err))
			assert.True(t, strings.HasPrefix(err.Error(), "failed to start workflow: "), err.Error())
			saved, err := os.ReadDir(tmp)
			require.NoError(t, err)
			assert.Empty(t, saved, "a rejected workflow leaves no binding behind")
		})
	}
}

func TestStart_RelayErrorNamesBindingSource(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString(make([]byte, 64))
	bindingFile := autoflowtest.WriteFile(t, encoded)

	t.Run("file", func(t *testing.T) {
		t.Setenv("TMPDIR", t.TempDir())
		relay := autoflowtest.NewRelay(t)
		expectStart(relay, nil, status.Error(codes.Unavailable, "connection reset"))
		var gotBody tokenexchangetest.WireRequest
		srv, _, _ := autoflowtest.NewTokenServer(t, &gotBody)
		exec := newTestExec(t, srv)

		_, err := exec(relay.Args() + " --script x --idempotency-key my-key --token-binding-file " + bindingFile)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "pass --idempotency-key my-key and --token-binding-file "+strconv.Quote(bindingFile)+"): ")
		saved, err := os.ReadDir(os.Getenv("TMPDIR"))
		require.NoError(t, err)
		assert.Empty(t, saved, "a binding from a file is not copied")
	})

	t.Run("environment", func(t *testing.T) {
		t.Setenv("GLAB_AUTOFLOW_TOKEN_BINDING", encoded)
		relay := autoflowtest.NewRelay(t)
		expectStart(relay, nil, status.Error(codes.Unavailable, "connection reset"))
		var gotBody tokenexchangetest.WireRequest
		srv, _, _ := autoflowtest.NewTokenServer(t, &gotBody)
		exec := newTestExec(t, srv)

		_, err := exec(relay.Args() + " --script x --idempotency-key my-key")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "pass --idempotency-key my-key and keep GLAB_AUTOFLOW_TOKEN_BINDING set): ")
	})
}

func TestStart_UntrustedCertificate(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	var gotBody tokenexchangetest.WireRequest
	srv, _, _ := autoflowtest.NewTokenServer(t, &gotBody)
	exec := newTestExec(t, srv)

	_, err := exec("--address " + relay.Address + " --namespace-id " + autoflowtest.NamespaceID + " --script x")
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))
	assert.Contains(t, err.Error(), "certificate")
}

func TestStart_InvalidCACert(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	var gotBody tokenexchangetest.WireRequest
	srv, count, _ := autoflowtest.NewTokenServer(t, &gotBody)
	exec := newTestExec(t, srv)

	_, err := exec("--address " + relay.Address + " --namespace-id " + autoflowtest.NamespaceID + " --script x --ca-cert " + autoflowtest.WriteFile(t, "not a certificate"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "contains no PEM-encoded certificate")
	assert.EqualValues(t, 0, count.Load())
}

func TestStart_NamespaceOverride(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	calls := expectStart(relay, startedResponse, nil)
	var gotBody tokenexchangetest.WireRequest
	// The token carries no organization: an explicit namespace does not need one.
	srv, _ := tokenexchangetest.NewTokenExchangeServer(t, autoflowtest.MakeToken(t, nil), &gotBody)
	exec := newTestExec(t, srv)
	other := uuid.New()

	out, err := exec(relay.Args() + " --script x --namespace-id " + other.String())
	require.NoError(t, err)
	assert.Equal(t, other[:], autoflowtest.Received(t, calls).Req.GetNamespaceId())
	assert.Contains(t, out.OutBuf.String(), "namespace_id: "+other.String()+"\n")
}

func TestStart_TokenWithoutOrganization(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		claims  map[string]any
		wantErr string
	}{
		{name: "no gitlab claims", claims: nil, wantErr: `the AutoFlow token is not issued for an organization (its gitlab.origin claim is "")`},
		{name: "origin ID not a UUID", claims: map[string]any{"origin": "organization", "origin_id": "42"}, wantErr: `the gitlab.origin_id claim of the AutoFlow token is "42", not an organization UUID`},
		{name: "nil origin ID", claims: map[string]any{"origin": "organization", "origin_id": "00000000-0000-0000-0000-000000000000"}, wantErr: `the gitlab.origin_id claim of the AutoFlow token is "00000000-0000-0000-0000-000000000000", not an organization UUID`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			relay := autoflowtest.NewRelay(t)
			var gotBody tokenexchangetest.WireRequest
			srv, _ := tokenexchangetest.NewTokenExchangeServer(t, autoflowtest.MakeToken(t, tc.claims), &gotBody)
			exec := newTestExec(t, srv)

			_, err := exec(relay.Args() + " --script x")
			require.EqualError(t, err, tc.wantErr)
		})
	}
}

func TestStart_TokenBindingFromEnvironment(t *testing.T) {
	relay := autoflowtest.NewRelay(t)
	calls := expectStart(relay, startedResponse, nil)
	var gotBody tokenexchangetest.WireRequest
	srv, _, _ := autoflowtest.NewTokenServer(t, &gotBody)
	exec := newTestExec(t, srv)
	binding := make([]byte, 64)
	binding[0] = 7
	t.Setenv("GLAB_AUTOFLOW_TOKEN_BINDING", base64.StdEncoding.EncodeToString(binding))

	_, err := exec(relay.Args() + " --script x --idempotency-key my-key")
	require.NoError(t, err)
	assert.Equal(t, binding, autoflowtest.Received(t, calls).Req.GetTokenBinding())
}

func TestStart_TokenBindingFileOverridesEnvironment(t *testing.T) {
	fromFile := make([]byte, 64)
	fromFile[0] = 1
	t.Setenv("GLAB_AUTOFLOW_TOKEN_BINDING", base64.StdEncoding.EncodeToString(make([]byte, 64)))
	relay := autoflowtest.NewRelay(t)
	calls := expectStart(relay, startedResponse, nil)
	var gotBody tokenexchangetest.WireRequest
	srv, _, _ := autoflowtest.NewTokenServer(t, &gotBody)
	exec := newTestExec(t, srv)

	_, err := exec(relay.Args() + " --script x --idempotency-key my-key --token-binding-file " + autoflowtest.WriteFile(t, base64.StdEncoding.EncodeToString(fromFile)))
	require.NoError(t, err)
	assert.Equal(t, fromFile, autoflowtest.Received(t, calls).Req.GetTokenBinding())
}

func TestStart_Resubmit(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	calls := expectStart(relay, startedResponse, nil)
	var gotBody tokenexchangetest.WireRequest
	srv, _, _ := autoflowtest.NewTokenServer(t, &gotBody)
	exec := newTestExec(t, srv)
	binding := make([]byte, 64)
	for i := range binding {
		binding[i] = byte(i)
	}
	encoded := base64.StdEncoding.EncodeToString(binding)

	out, err := exec(relay.Args() + " --script x --idempotency-key my-key --token-binding-file " + autoflowtest.WriteFile(t, encoded+"\n"))
	require.NoError(t, err)

	gotReq := autoflowtest.Received(t, calls).Req
	assert.Equal(t, "my-key", gotReq.GetIdempotencyKey())
	assert.Equal(t, binding, gotReq.GetTokenBinding())
	assert.Contains(t, out.OutBuf.String(), "token_binding: "+encoded+"\n")
}

func TestStart_KwargsAndChannels(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	calls := expectStart(relay, &autoflowrpcv1.StartWorkflowResponse{
		WorkflowKey:   "wf-key",
		WorkflowToken: "wf-token",
		ChannelTokens: []*autoflowv1.ChannelToken{{ChannelName: "inbox", Token: "ch-token"}},
	}, nil)
	var gotBody tokenexchangetest.WireRequest
	srv, _, _ := autoflowtest.NewTokenServer(t, &gotBody)
	exec := newTestExec(t, srv)

	out, err := exec(relay.Args() + ` --script x --kwarg count=2 --kwarg 'opts={"a": [true, null]}' --kwarg 'msg="a=b"' --channel inbox`)
	require.NoError(t, err)

	want := []*autoflowv1.Kwarg{
		{Name: "count", Value: &autoflowv1.Value{Kind: &autoflowv1.Value_IntegerValue{IntegerValue: 2}}},
		{Name: "opts", Value: &autoflowv1.Value{Kind: &autoflowv1.Value_DictValue{DictValue: &autoflowv1.DictValue{KeyValues: []*autoflowv1.KeyValue{{
			Key: &autoflowv1.Value{Kind: &autoflowv1.Value_StringValue{StringValue: "a"}},
			Val: &autoflowv1.Value{Kind: &autoflowv1.Value_ListValue{ListValue: &autoflowv1.ListValue{Values: []*autoflowv1.Value{
				{Kind: &autoflowv1.Value_BoolValue{BoolValue: true}},
				{Kind: &autoflowv1.Value_NoneValue{}},
			}}}},
		}}}}}},
		{Name: "msg", Value: &autoflowv1.Value{Kind: &autoflowv1.Value_StringValue{StringValue: "a=b"}}},
		{Name: "inbox", Value: &autoflowv1.Value{Kind: &autoflowv1.Value_ChannelValue{ChannelValue: &autoflowv1.ChannelValue{Name: "inbox"}}}},
	}
	got := autoflowtest.Received(t, calls).Req.GetKwargs()
	require.Len(t, got, len(want))
	for i := range want {
		assert.True(t, proto.Equal(want[i], got[i]), "kwarg %d: got %v", i, got[i])
	}
	assert.Contains(t, out.OutBuf.String(), "token_binding: ")
	assert.True(t, strings.HasSuffix(out.OutBuf.String(), "\nchannel_token.inbox: ch-token\n"), out.OutBuf.String())
}

func TestStart_KwargErrors(t *testing.T) {
	t.Parallel()

	relay := autoflowtest.NewRelay(t)
	tests := []struct {
		name    string
		args    string
		wantErr string
	}{
		{name: "no equals sign", args: "--kwarg count", wantErr: "error parsing --kwarg count: want <name>=<JSON value>"},
		{name: "invalid JSON", args: "--kwarg count=two", wantErr: "error parsing --kwarg count: invalid character"},
		{name: "no name", args: "--kwarg =1", wantErr: "a keyword argument needs a name"},
		{name: "duplicate kwarg", args: "--kwarg a=1 --kwarg a=2", wantErr: `keyword argument "a" is passed more than once`},
		{name: "kwarg and channel", args: "--kwarg inbox=1 --channel inbox", wantErr: `keyword argument "inbox" is passed more than once`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var gotBody tokenexchangetest.WireRequest
			srv, count, _ := autoflowtest.NewTokenServer(t, &gotBody)
			exec := newTestExec(t, srv)

			_, err := exec(relay.Args() + " --script x " + tc.args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
			assert.EqualValues(t, 0, count.Load())
		})
	}
}
