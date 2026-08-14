package graphstatus

import (
	"context"
	"errors"
	"net/http"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	internalAPI "gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/orbit/internal/orbiterr"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

const (
	formatLLM = "llm"
	formatRaw = "raw"
)

type options struct {
	apiClient func(repoHost string) (*internalAPI.Client, error)
	io        *iostreams.IOStreams

	hostname      string
	namespaceID   int64
	projectID     int64
	fullPath      string
	format        string
	formatChanged bool
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		apiClient: f.ApiClient,
		io:        f.IO(),
	}

	cmd := &cobra.Command{
		Use:   "graph-status",
		Short: `Show indexing progress for a namespace or project. (EXPERIMENTAL)`,
		Long: heredoc.Doc(`
			Calls `+"`GET /api/v4/orbit/graph_status`"+` and prints the
			indexing-progress response as pretty-printed JSON. The response
			carries project counts, per-domain entity counts, and the overall
			indexing run state for the requested scope.

			Exactly one of `+"`--namespace-id`"+`, `+"`--project-id`"+`, or
			`+"`--full-path`"+` is required. `+"`--full-path`"+` accepts the
			full path of a project or group. For example, `+"`gitlab-org/gitlab`"+`.

			Unlike `+"`glab orbit remote query`"+`, this endpoint defaults to
			the `+"`raw`"+` response format. Use `+"`--response-format llm`"+`
			for compact output intended for agents.
		`) + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Look up indexing progress by full path
			$ glab orbit remote graph-status --full-path gitlab-org/gitlab

			# Or by numeric ID
			$ glab orbit remote graph-status --project-id 278964
			$ glab orbit remote graph-status --namespace-id 9970

			# Compact output for agents
			$ glab orbit remote graph-status --full-path gitlab-org/gitlab --response-format llm
		`),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts.formatChanged = cmd.Flags().Changed("response-format") || cmd.Flags().Changed("format")
			return opts.run(cmd.Context())
		},
	}

	fl := cmd.Flags()
	fl.StringVar(&opts.hostname, "hostname", "",
		"GitLab hostname to query. Defaults to the current repository's host or `gitlab.com`.")
	fl.Int64Var(&opts.namespaceID, "namespace-id", 0,
		"Namespace (group) ID to inspect. Cannot be used with --project-id or --full-path.")
	fl.Int64Var(&opts.projectID, "project-id", 0,
		"Project ID to inspect. Cannot be used with --namespace-id or --full-path.")
	fl.StringVar(&opts.fullPath, "full-path", "",
		"Full path of a project or group, such as `gitlab-org/gitlab`. Cannot be used with the ID flags.")
	fl.VarP(cmdutils.NewEnumValue([]string{formatRaw, formatLLM}, formatRaw, &opts.format),
		"response-format", "",
		"Server response format: `raw` (structured JSON) or `llm` (compact GOON/TOON for agents).")

	// Deprecated: use --response-format instead.
	fl.VarP(cmdutils.NewEnumValue([]string{formatRaw, formatLLM}, formatRaw, &opts.format),
		"format", "f", "Response format: `raw` (structured JSON) or `llm` (compact, intended for agents).")
	_ = fl.MarkDeprecated("format", "use --response-format instead.")

	cmd.MarkFlagsMutuallyExclusive("namespace-id", "project-id", "full-path")
	cmd.MarkFlagsOneRequired("namespace-id", "project-id", "full-path")

	cmdutils.AddJQFlag(cmd, f.IO())
	return cmd
}

func (o *options) run(ctx context.Context) error {
	apiOpts := &gitlab.GetGraphStatusOptions{}

	if o.namespaceID != 0 {
		apiOpts.NamespaceID = &o.namespaceID
	}
	if o.projectID != 0 {
		apiOpts.ProjectID = &o.projectID
	}
	if o.fullPath != "" {
		apiOpts.FullPath = &o.fullPath
	}
	if o.formatChanged {
		apiOpts.ResponseFormat = new(gitlab.OrbitResponseFormatValue(o.format))
	}

	client, err := o.apiClient(o.hostname)
	if err != nil {
		return err
	}

	status, resp, err := client.Lab().Orbit.GetGraphStatus(apiOpts, gitlab.WithContext(ctx))
	if err != nil {
		// `graph_status` can return 503 when the underlying Orbit service
		// is unavailable. The shared translator does not have a mapping
		// for 503, so surface it as a clear, descriptive generic-exit
		// error before falling through to the standard taxonomy.
		if resp != nil && resp.StatusCode == http.StatusServiceUnavailable {
			return cmdutils.WrapError(
				errors.New("knowledge graph service unavailable"),
				"The Orbit API returned HTTP 503. The underlying Orbit service is\n"+
					"currently unreachable; retry shortly or check the GitLab status\n"+
					"page.",
			)
		}
		return orbiterr.Translate(err)
	}

	return o.io.PrintJSON(status)
}
