package autoflowtest

import (
	"context"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	autoflowrpcv1 "gitlab.com/gitlab-org/protos/gen/go/gitlab/autoflow/rpc/v1"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
	"gitlab.com/gitlab-org/cli/internal/testing/tokenexchangetest"
)

// NamespaceID is the organization the tokens of NewTokenServer are issued
// for, and so the namespace the tests address.
const NamespaceID = "5b4a3928-1706-4f5e-8d4c-3b2a19081726"

// Relay is a mocked AutoFlow service listening on Address with a self-signed
// certificate, which CACertFile holds.
type Relay struct {
	Mock       *MockAutoFlowServiceServer
	Address    string
	CACertFile string
}

// NewRelay starts a Relay. A call the test did not expect fails it.
func NewRelay(t *testing.T) *Relay {
	t.Helper()

	certSrc := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(certSrc.Close)

	caCertFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caCertFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certSrc.Certificate().Raw}), 0o600))

	relay := serve(t, grpc.Creds(credentials.NewServerTLSFromCert(&certSrc.TLS.Certificates[0])))
	relay.CACertFile = caCertFile
	return relay
}

// NewCleartextRelay starts a Relay without TLS. Its CACertFile is empty.
func NewCleartextRelay(t *testing.T) *Relay {
	t.Helper()

	return serve(t)
}

func serve(t *testing.T, opts ...grpc.ServerOption) *Relay {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	mock := NewMockAutoFlowServiceServer(gomock.NewController(t))
	srv := grpc.NewServer(opts...)
	// The mock cannot embed UnimplementedAutoFlowServiceServer, so
	// UnsafeAutoFlowServiceServer supplies the unexported method instead.
	autoflowrpcv1.RegisterAutoFlowServiceServer(srv, struct {
		autoflowrpcv1.UnsafeAutoFlowServiceServer
		*MockAutoFlowServiceServer
	}{MockAutoFlowServiceServer: mock})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	return &Relay{Mock: mock, Address: lis.Addr().String()}
}

// Args returns the flags that address the relay.
func (r *Relay) Args() string {
	return "--address " + r.Address + " --ca-cert " + r.CACertFile
}

// Call is one RPC the relay received.
type Call[Req any] struct {
	Req  Req
	Auth []string
}

// Answer returns a mock action that answers with resp and err and sends the
// call on calls. The call travels on a channel because the race detector
// cannot see the socket ordering the RPC with the test's reads.
func Answer[Req, Resp any](calls chan<- Call[Req], resp Resp, err error) func(context.Context, Req) (Resp, error) {
	return func(ctx context.Context, req Req) (Resp, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		calls <- Call[Req]{Req: req, Auth: md.Get("authorization")}
		return resp, err
	}
}

// Received returns the next call on calls, failing the test if there is none.
func Received[Req any](t *testing.T, calls <-chan Call[Req]) Call[Req] {
	t.Helper()

	select {
	case call := <-calls:
		return call
	default:
		require.FailNow(t, "the RPC was not called")
		return Call[Req]{}
	}
}

// NewTokenServer starts a token exchange endpoint that answers with a
// gitlab-autoflow token, which it returns along with a count of exchanges.
func NewTokenServer(t *testing.T, gotBody *tokenexchangetest.WireRequest) (*httptest.Server, *atomic.Int32, string) {
	t.Helper()

	token := MakeToken(t, map[string]any{"origin": "organization", "origin_id": NamespaceID})
	srv, count := tokenexchangetest.NewTokenExchangeServer(t, token, gotBody)
	return srv, count, token
}

// MakeToken mints a gitlab-autoflow token with the gitlab claim set to
// gitlabClaims.
func MakeToken(t *testing.T, gitlabClaims map[string]any) string {
	t.Helper()

	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss":    "https://gitlab.example.com",
		"sub":    "gid://gitlab/User/1",
		"aud":    "gitlab-autoflow",
		"exp":    time.Now().Add(time.Minute).Unix(),
		"gitlab": gitlabClaims,
	}).SignedString([]byte("test-signing-key"))
	require.NoError(t, err)
	return token
}

// NewTestExec wires cmdFunc to a token exchange endpoint that answers with
// the token it returns.
func NewTestExec(t *testing.T, cmdFunc cmdtest.CmdFunc, opts ...cmdtest.FactoryOption) (cmdtest.CmdExecFunc, string) {
	t.Helper()

	var gotBody tokenexchangetest.WireRequest
	srv, _, token := NewTokenServer(t, &gotBody)
	return tokenexchangetest.NewTestExec(t, srv, cmdFunc, opts...), token
}

// WriteFile writes content to a new file and returns its path.
func WriteFile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}
