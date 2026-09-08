package semantic

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type options struct {
	io           *iostreams.IOStreams
	gitlabClient func() (*gitlab.Client, error)
	baseRepoFunc func() (glrepo.Interface, error)

	client    *gitlab.Client
	projectID string

	query         string
	directoryPath string
	knn           int
	limit         int
	outputFormat  string
}

type semanticSearchResponse struct {
	Confidence string         `json:"confidence"`
	Results    []searchResult `json:"results"`
}

type searchResult struct {
	Path          string      `json:"path"`
	FileURL       string      `json:"file_url"`
	Score         float64     `json:"score"`
	SnippetRanges []codeChunk `json:"snippet_ranges"`
}

type codeChunk struct {
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Content   string `json:"content"`
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
		baseRepoFunc: f.BaseRepo,
	}

	cmd := &cobra.Command{
		Use:   "semantic [flags]",
		Short: `Search project code using natural language.`,
		Long: heredoc.Doc(`
			Search project code using natural language (semantic similarity).

			Requires the project to have semantic code search enabled via GitLab Duo.
		`) + text.BetaString,
		Example: heredoc.Doc(`
			# Search for authentication-related code in the current project
			glab search semantic -q "authentication middleware"

			# Search within a specific directory
			glab search semantic -q "rate limiting" -d app/services/

			# Search in a specific project with JSON output
			glab search semantic -q "CI pipeline triggers" -R gitlab-org/gitlab --output json

			# Limit results
			glab search semantic -q "database migrations" --limit 5
		`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(); err != nil {
				return err
			}
			if err := opts.complete(); err != nil {
				return err
			}
			return opts.run(cmd.Context())
		},
	}

	fl := cmd.Flags()
	fl.StringVarP(&opts.query, "query", "q", "", "Natural language search query.")
	fl.StringVarP(&opts.directoryPath, "directory-path", "d", "", "Restrict search to files under this path (e.g. app/services/).")
	fl.IntVar(&opts.knn, "knn", 0, "Nearest neighbours to retrieve (1–100). Defaults to 64 server-side.")
	fl.IntVarP(&opts.limit, "limit", "l", 0, "Maximum number of results (1–100). Defaults to 20 server-side.")
	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)

	cobra.CheckErr(cmd.MarkFlagRequired("query"))

	return cmd
}

func (o *options) validate() error {
	if o.knn != 0 && (o.knn < 1 || o.knn > 100) {
		return cmdutils.FlagError{Err: fmt.Errorf("--knn must be between 1 and 100, got %d", o.knn)}
	}
	if o.limit != 0 && (o.limit < 1 || o.limit > 100) {
		return cmdutils.FlagError{Err: fmt.Errorf("--limit must be between 1 and 100, got %d", o.limit)}
	}
	return nil
}

func (o *options) complete() error {
	var err error
	o.client, err = o.gitlabClient()
	if err != nil {
		return err
	}

	baseRepo, err := o.baseRepoFunc()
	if err != nil {
		return err
	}
	o.projectID = baseRepo.FullName()

	return nil
}

func (o *options) run(ctx context.Context) error {
	path := fmt.Sprintf("projects/%s/search/semantic", url.PathEscape(o.projectID))
	req, err := o.client.NewRequest(http.MethodGet, path, nil, []gitlab.RequestOptionFunc{
		gitlab.WithContext(ctx),
	})
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	q := req.URL.Query()
	q.Set("q", o.query)
	if o.directoryPath != "" {
		q.Set("directory_path", o.directoryPath)
	}
	if o.knn != 0 {
		q.Set("knn", strconv.Itoa(o.knn))
	}
	if o.limit != 0 {
		q.Set("limit", strconv.Itoa(o.limit))
	}
	req.URL.RawQuery = q.Encode()

	if o.outputFormat != "json" {
		o.io.LogInfof("Searching for %q in %s...\n", o.query, o.projectID)
	}

	var result semanticSearchResponse
	_, err = o.client.Do(req, &result)
	if err != nil {
		return fmt.Errorf("semantic search request failed: %w", err)
	}

	if o.outputFormat == "json" {
		return o.io.PrintJSON(result)
	}

	return o.printText(&result)
}

func (o *options) printText(result *semanticSearchResponse) error {
	c := o.io.Color()
	o.io.LogInfof("Confidence: %s\n", result.Confidence)

	if len(result.Results) == 0 {
		o.io.LogInfo("\nNo results found.")
		return nil
	}

	o.io.LogInfo()
	for _, r := range result.Results {
		o.io.LogInfof("%s  (score: %.2f)\n",
			c.Bold(r.Path), r.Score)
		for _, chunk := range r.SnippetRanges {
			o.io.LogInfof("  Lines %d–%d:\n", chunk.StartLine, chunk.EndLine)
			for line := range strings.SplitSeq(chunk.Content, "\n") {
				o.io.LogInfof("    %s\n", line)
			}
		}
		o.io.LogInfo()
	}

	return nil
}
