// Package autoflow connects to the AutoFlow service of GitLab Relay with a
// token-exchange token minted by GitLab for the gitlab-autoflow audience.
package autoflow

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	autoflowrpcv1 "gitlab.com/gitlab-org/protos/gen/go/gitlab/autoflow/rpc/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api/tokenexchange"
)

const audience = "gitlab-autoflow"

// tokenLifetime is short because Relay accepts any valid token for its
// organization's namespace, so a leaked token can start workflows there until
// it expires.
const tokenLifetime = time.Minute

// refreshMargin leaves a call enough of the token's lifetime to complete
// before Relay sees it expire.
const refreshMargin = 20 * time.Second

// TokenBindingSize is the exact length StartWorkflow requires of a token
// binding.
const TokenBindingSize = 64

// Dial exchanges the caller's GitLab credential for a token Relay accepts on
// its public listener and connects to the AutoFlow service at address over
// TLS. Each call exchanges a fresh token when the current one has less than
// refreshMargin left, so the connection outlives a single token. The caller
// must close the returned connection.
func Dial(ctx context.Context, gl *gitlab.Client, address string, tlsConfig *tls.Config) (autoflowrpcv1.AutoFlowServiceClient, *grpc.ClientConn, error) {
	src := &tokenSource{gl: gl, now: time.Now}
	// Exchanged before the dial, so a user without the feature flag gets
	// GitLab's answer instead of a failed RPC.
	if err := src.refresh(ctx); err != nil {
		return nil, nil, err
	}
	conn, err := grpc.NewClient(address,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
		grpc.WithPerRPCCredentials(src),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to %s: %w", address, err)
	}
	return autoflowrpcv1.NewAutoFlowServiceClient(conn), conn, nil
}

type tokenSource struct {
	gl  *gitlab.Client
	now func() time.Time

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

func (s *tokenSource) GetRequestMetadata(ctx context.Context, _ ...string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.now().Add(refreshMargin).After(s.expiresAt) {
		if err := s.refresh(ctx); err != nil {
			return nil, err
		}
	}
	return map[string]string{"authorization": "Bearer " + s.token}, nil
}

func (*tokenSource) RequireTransportSecurity() bool {
	return true
}

func (s *tokenSource) refresh(ctx context.Context) error {
	// Measured on the local clock before the request, so a GitLab clock that
	// runs ahead cannot make a token look fresher than it is.
	expiresAt := s.now().Add(tokenLifetime)
	result, err := tokenexchange.Exchange(ctx, s.gl, audience, tokenLifetime)
	if err != nil {
		// GitLab answers 400 for an audience it does not mint for the caller.
		if errResp, ok := errors.AsType[*gitlab.ErrorResponse](err); ok && errResp.StatusCode == http.StatusBadRequest {
			err = fmt.Errorf("%w (GitLab mints gitlab-autoflow tokens only for users with the token_exchange_autoflow_audience feature flag, and not for CI job tokens)", err)
		}
		return fmt.Errorf("failed to get an AutoFlow token: %w", err)
	}
	s.token = result.Token
	s.expiresAt = expiresAt
	return nil
}
