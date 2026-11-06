// Package autoflowutils holds what the `glab autoflow workflow` commands
// share.
package autoflowutils

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	autoflowrpcv1 "gitlab.com/gitlab-org/protos/gen/go/gitlab/autoflow/rpc/v1"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/api/autoflow"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

// CallTimeout bounds a single RPC to the AutoFlow service.
const CallTimeout = 30 * time.Second

// Connection is the set of flags that addresses the AutoFlow service and a
// namespace in it.
type Connection struct {
	hostname    string
	address     string
	caCertFile  string
	insecure    bool
	namespaceID string

	// NamespaceID is the parsed --namespace-id, set by Complete.
	NamespaceID uuid.UUID
}

// AddFlags registers the connection flags on cmd.
func (c *Connection) AddFlags(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVar(&c.hostname, "hostname", "", "GitLab hostname to exchange the token with. Defaults to the configured GitLab instance.")
	fl.StringVar(&c.address, "address", "", "Address of the AutoFlow service, as <host>:<port>.")
	fl.StringVar(&c.caCertFile, "ca-cert", "", "Path to a PEM-encoded CA certificate to trust for the AutoFlow service, in addition to the system roots.")
	fl.BoolVar(&c.insecure, "insecure", false, "Connect to the AutoFlow service without TLS, for a local service only. The AutoFlow token and the workflow's credentials travel in cleartext.")
	fl.StringVar(&c.namespaceID, "namespace-id", "", "UUID of your organization, the only AutoFlow namespace your token can address.")

	cobra.CheckErr(cmd.MarkFlagRequired("address"))
	cobra.CheckErr(cmd.MarkFlagRequired("namespace-id"))
	cobra.CheckErr(cmd.MarkFlagFilename("ca-cert", "pem", "crt"))
	cmd.MarkFlagsMutuallyExclusive("insecure", "ca-cert")
}

// Complete parses and validates the connection flags.
func (c *Connection) Complete() error {
	if c.hostname != "" {
		if err := glinstance.HostnameValidator(c.hostname); err != nil {
			return &cmdutils.FlagError{Err: fmt.Errorf("error parsing --hostname: %w", err)}
		}
	}
	id, err := uuid.Parse(c.namespaceID)
	if err != nil {
		return &cmdutils.FlagError{Err: fmt.Errorf("error parsing --namespace-id: %w", err)}
	}
	if id == uuid.Nil {
		return &cmdutils.FlagError{Err: errors.New("error parsing --namespace-id: must not be the nil UUID")}
	}
	c.NamespaceID = id
	return nil
}

// Dial connects to the AutoFlow service with a token exchanged on the GitLab
// instance. The caller must close the returned connection.
func (c *Connection) Dial(ctx context.Context, ios *iostreams.IOStreams, apiClient func(repoHost string) (*api.Client, error)) (autoflowrpcv1.AutoFlowServiceClient, io.Closer, error) {
	var tlsConfig *tls.Config
	if c.insecure {
		ios.LogErrorf("warning: --insecure sends the AutoFlow token and the workflow's credentials to %s in cleartext\n", c.address)
	} else {
		var err error
		tlsConfig, err = c.tlsConfig()
		if err != nil {
			return nil, nil, err
		}
	}
	client, err := apiClient(c.hostname)
	if err != nil {
		return nil, nil, err
	}
	return autoflow.Dial(ctx, client.Lab(), c.address, tlsConfig)
}

func (c *Connection) tlsConfig() (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if c.caCertFile == "" {
		return cfg, nil
	}
	pem, err := os.ReadFile(c.caCertFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read --ca-cert: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("failed to load the system certificate pool: %w", err)
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("--ca-cert %s contains no PEM-encoded certificate", c.caCertFile)
	}
	cfg.RootCAs = pool
	return cfg, nil
}
