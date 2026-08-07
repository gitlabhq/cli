// Package status implements `glab artifact-registry status`, which reports the caller's
// access to the GitLab Artifact Registry by exchanging their GitLab session
// for a short-lived access token and inspecting its claims.
package status

import (
	"context"
	"fmt"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/api/artifactregistry"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/tableprinter"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type options struct {
	io        *iostreams.IOStreams
	apiClient func(repoHost string) (*api.Client, error)

	hostname     string
	outputFormat string
}

// NewCmd returns the `glab artifact-registry status` command.
func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
	}

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Check your access to the GitLab Artifact Registry. (EXPERIMENTAL)",
		Long: heredoc.Doc(`
			Exchange a GitLab credential for a short-lived Artifact Registry access
			token, then print the token's issuer, subject, audience, and expiry so
			you can confirm which identity and instance you are authenticated as. No
			credentials are written to disk.

			Prerequisites:

			- A GitLab Enterprise Edition (EE) instance on GitLab 19.1 or later.
			- Token exchange enabled on the instance (the
			  `+"`gate_token_exchange_endpoint`"+` feature flag).
		`) + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Show Artifact Registry access status
			glab artifact-registry status

			# Show Artifact Registry access status as JSON
			glab artifact-registry status --output json
		`),
		Args: cobra.NoArgs,
		// Not mcpannotations.Safe: reading the claims requires minting a token,
		// so each run creates server-side state rather than only reading it.
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := opts.validate(); err != nil {
				return err
			}
			return opts.run(cmd.Context())
		},
	}

	cmd.Flags().StringVar(&opts.hostname, "hostname", "", "GitLab hostname to check. Defaults to the configured GitLab instance.")
	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)

	return cmd
}

// validate rejects a --hostname that is not a bare hostname, because the value
// is otherwise forwarded to f.ApiClient untouched and decides which host
// receives the caller's credential. Empty means "use the configured instance",
// so it is left alone. Same check as `glab api --hostname`.
func (o *options) validate() error {
	if o.hostname == "" {
		return nil
	}
	if err := glinstance.HostnameValidator(o.hostname); err != nil {
		return &cmdutils.FlagError{Err: fmt.Errorf("error parsing --hostname: %w", err)}
	}
	return nil
}

func (o *options) run(ctx context.Context) error {
	apiClient, err := o.apiClient(o.hostname)
	if err != nil {
		return err
	}

	// The token is read for its claims and thrown away, so let the server pick
	// the lifetime instead of asking for a CLI-chosen one.
	result, err := artifactregistry.NewClient(apiClient.Lab()).ExchangeDefaultToken(ctx)
	if err != nil {
		return fmt.Errorf("failed to check artifact registry status: %w", err)
	}

	switch o.outputFormat {
	case "json":
		return o.printJSON(result)
	default:
		return o.printTable(result)
	}
}

func (o *options) printJSON(result *artifactregistry.ExchangeResult) error {
	return o.io.PrintJSON(struct {
		Issuer    string    `json:"issuer"`
		Subject   string    `json:"subject"`
		Audience  string    `json:"audience"`
		ExpiresAt time.Time `json:"expires_at"`
	}{
		Issuer:    result.Issuer,
		Subject:   result.Subject,
		Audience:  result.Audience,
		ExpiresAt: result.ExpiresAt,
	})
}

func (o *options) printTable(result *artifactregistry.ExchangeResult) error {
	table := tableprinter.NewTablePrinter()
	table.AddRow("Issuer", result.Issuer)
	table.AddRow("Subject", result.Subject)
	table.AddRow("Audience", result.Audience)
	table.AddRow("Expires At", result.ExpiresAt.Format(time.RFC3339))
	o.io.LogInfo(table.String())

	return nil
}
