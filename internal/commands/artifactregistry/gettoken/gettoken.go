// Package gettoken implements `glab artifact-registry get-token`, which exchanges the
// caller's GitLab credential for a short-lived GitLab Artifact Registry
// access token.
package gettoken

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
	"gitlab.com/gitlab-org/cli/internal/text"
)

type options struct {
	io        *iostreams.IOStreams
	apiClient func(repoHost string) (*api.Client, error)

	hostname     string
	duration     time.Duration
	outputFormat string
}

// NewCmd returns the `glab artifact-registry get-token` command.
func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
	}

	cmd := &cobra.Command{
		Use:   "get-token",
		Short: "Get a short-lived access token for the GitLab Artifact Registry. (EXPERIMENTAL)",
		Long: heredoc.Docf(`
			Exchange a GitLab credential for a short-lived access token scoped to the
			GitLab Artifact Registry. The command prints the bare token to stdout,
			so a shell can capture it directly, for example to feed %[1]sdocker login%[1]s.

			Prerequisites:

			- A GitLab Enterprise Edition (EE) instance on GitLab 19.1 or later.
			- Token exchange enabled on the instance (the
			  %[1]sgate_token_exchange_endpoint%[1]s feature flag).
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Get a token using the default duration
			glab artifact-registry get-token

			# Get a token valid for one hour
			glab artifact-registry get-token --duration 1h

			# Get a token as JSON, including its expiry
			glab artifact-registry get-token --output json
		`),
		Args: cobra.NoArgs,
		// mcpannotations.Exclude, not Safe or Destructive: every run mints a
		// live bearer credential and prints it, so an MCP agent invoking this
		// would receive a usable secret. token/create and
		// cluster/agent/get-token, which also print a fresh secret, are
		// excluded for the same reason.
		Annotations: map[string]string{
			mcpannotations.Exclude: "true",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := opts.validate(); err != nil {
				return err
			}
			return opts.run(cmd.Context())
		},
	}

	fl := cmd.Flags()
	fl.StringVar(&opts.hostname, "hostname", "", "GitLab hostname to request the token from. Defaults to the configured GitLab instance.")
	fl.DurationVar(&opts.duration, "duration", artifactregistry.DefaultDuration, fmt.Sprintf("How long the token should remain valid. Must be between %s and %s.", artifactregistry.MinDuration, artifactregistry.MaxDuration))
	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)

	return cmd
}

// validate rejects a --hostname that is not a bare hostname, because the value
// is otherwise forwarded to f.ApiClient untouched and decides which host
// receives the caller's credential. Empty means "use the configured instance",
// so it is left alone. Same check as `glab api --hostname`.
//
// It also rejects a --duration outside artifactregistry's accepted bounds
// here, at the command's own validate step, rather than leaving that guarantee
// to fall out of ExchangeToken's own internal check: run calls ExchangeToken
// only after validate succeeds, so an out-of-range duration must never reach
// it, and this is the layer responsible for enforcing that regardless of what
// ExchangeToken does internally.
func (o *options) validate() error {
	if o.hostname != "" {
		if err := glinstance.HostnameValidator(o.hostname); err != nil {
			return &cmdutils.FlagError{Err: fmt.Errorf("error parsing --hostname: %w", err)}
		}
	}
	if err := artifactregistry.ValidateDuration(o.duration); err != nil {
		return &cmdutils.FlagError{Err: fmt.Errorf("error parsing --duration: %w", err)}
	}
	return nil
}

func (o *options) run(ctx context.Context) error {
	apiClient, err := o.apiClient(o.hostname)
	if err != nil {
		return err
	}

	result, err := artifactregistry.NewClient(apiClient.Lab()).ExchangeToken(ctx, o.duration)
	if err != nil {
		return fmt.Errorf("failed to get artifact registry token: %w", err)
	}

	switch o.outputFormat {
	case "json":
		return o.printJSON(result)
	default:
		return o.printToken(result)
	}
}

func (o *options) printJSON(result *artifactregistry.ExchangeResult) error {
	return o.io.PrintJSON(struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}{
		Token:     result.Token,
		ExpiresAt: result.ExpiresAt,
	})
}

// printToken writes the bare token so a shell can capture it, e.g.
// TOKEN=$(glab artifact-registry get-token). It uses fmt.Fprintln directly
// rather than o.io.LogInfo, which discards its write error: a dropped error
// here would leave the caller with an empty $TOKEN and exit code 0.
func (o *options) printToken(result *artifactregistry.ExchangeResult) error {
	if _, err := fmt.Fprintln(o.io.StdOut, result.Token); err != nil { //nolint:forbidigo // write error must propagate to the caller
		return err
	}
	return nil
}
