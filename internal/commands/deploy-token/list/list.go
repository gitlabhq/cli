package list

import (
	"context"
	"fmt"
	"net/http"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/deploy-token/display"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/tableprinter"
)

type options struct {
	io        *iostreams.IOStreams
	apiClient func(repoHost string) (*api.Client, error)
	baseRepo  func() (glrepo.Interface, error)

	group        string
	instance     bool
	activeOnly   bool
	outputFormat string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
		baseRepo:  f.BaseRepo,
	}

	cmd := &cobra.Command{
		Use:               "list [flags]",
		Short:             "List deploy tokens.",
		Aliases:           []string{"ls"},
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		Long: heredoc.Docf(`
			List the deploy tokens of a project, a group, or the instance.

			Pass %[1]s--instance%[1]s to list every deploy token on the instance. This requires administrator access, and %[1]sGITLAB_GROUP%[1]s does not apply.

			The output never contains the token value. A token value is shown only once, when the token is created.

			Tokens are listed with their expiry, and whether they have been revoked or have expired. Pass %[1]s--active%[1]s to list only the tokens that are still usable.
		`, "`"),
		Example: heredoc.Doc(`
			# List the current project's deploy tokens
			glab deploy-token list

			# List the deploy tokens of a specific project
			glab deploy-token list --repo user/my-repo

			# List only the tokens that are neither revoked nor expired
			glab deploy-token list --active

			# List a group's deploy tokens
			glab deploy-token list --group group/sub-group

			# List every deploy token on the instance (administrator only)
			glab deploy-token list --instance

			# List the current project's deploy tokens as JSON
			glab deploy-token list --output json`),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd); err != nil {
				return err
			}

			return opts.run(cmd.Context())
		},
	}

	cmdutils.EnableRepoOverride(cmd, f)

	fl := cmd.Flags()
	fl.StringVarP(&opts.group, "group", "g", "", "List the deploy tokens of the group <group> instead of a project.")
	fl.BoolVarP(&opts.activeOnly, "active", "a", false, "List only the tokens that are neither revoked nor expired.")
	fl.BoolVarP(&opts.instance, "instance", "i", false, "List every deploy token on the instance instead of a project or group. Requires administrator access.")

	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)
	cmd.MarkFlagsMutuallyExclusive("repo", "group", "instance")

	return cmd
}

func (o *options) complete(cmd *cobra.Command) error {
	if o.instance {
		return nil
	}

	group, err := cmdutils.GroupOverride(cmd)
	if err != nil {
		return err
	}
	o.group = group

	return nil
}

func (o *options) run(ctx context.Context) error {
	// The group and instance scopes work outside a repository, so a missing base
	// repo is only fatal for the project scope. Fail before building a client that would be
	// discarded, so the caller sees why the repository was needed.
	repo, repoErr := o.baseRepo()
	if o.group == "" && !o.instance && repoErr != nil {
		return repoErr
	}

	var repoHost string
	if repoErr == nil {
		repoHost = repo.RepoHost()
	}

	apiClient, err := o.apiClient(repoHost)
	if err != nil {
		return err
	}
	client := apiClient.Lab()

	var tokens []*gitlab.DeployToken
	switch {
	case o.instance:
		tokens, err = gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.DeployToken, *gitlab.Response, error) {
			return client.DeployTokens.ListAllDeployTokens(p, gitlab.WithContext(ctx))
		})
		if gitlab.HasStatusCode(err, http.StatusForbidden) {
			err = fmt.Errorf("%w. Listing the deploy tokens of the instance requires administrator access", err)
		}
	case o.group != "":
		tokens, err = gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.DeployToken, *gitlab.Response, error) {
			return client.DeployTokens.ListGroupDeployTokens(o.group, &gitlab.ListGroupDeployTokensOptions{}, p, gitlab.WithContext(ctx))
		})
	default:
		tokens, err = gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.DeployToken, *gitlab.Response, error) {
			return client.DeployTokens.ListProjectDeployTokens(repo.FullName(), &gitlab.ListProjectDeployTokensOptions{}, p, gitlab.WithContext(ctx))
		})
	}
	if err != nil {
		return cmdutils.WrapError(err, "failed to list deploy tokens")
	}

	if o.activeOnly {
		tokens = activeTokens(tokens)
	}

	// JSON output prints the response as-is, so the token value is cleared here
	// instead of relying on the endpoint never returning it.
	for _, token := range tokens {
		token.Token = ""
	}

	switch o.outputFormat {
	case "json":
		return o.io.PrintJSON(tokens)
	default:
		return o.printTable(tokens)
	}
}

// activeTokens keeps the tokens that are still usable. The deploy token list
// endpoints in client-go accept no active filter, so this is applied here.
func activeTokens(tokens []*gitlab.DeployToken) []*gitlab.DeployToken {
	active := make([]*gitlab.DeployToken, 0, len(tokens))
	for _, token := range tokens {
		if !token.Revoked && !token.Expired {
			active = append(active, token)
		}
	}

	return active
}

func (o *options) printTable(tokens []*gitlab.DeployToken) error {
	if len(tokens) == 0 {
		o.io.LogInfo("No deploy tokens found.")
		return nil
	}

	c := o.io.Color()
	table := tableprinter.NewTablePrinter()
	table.AddRow(c.Bold("ID"), c.Bold("Name"), c.Bold("Username"), c.Bold("Scopes"), c.Bold("Expires At"), c.Bold("Revoked"), c.Bold("Expired"))
	for _, token := range tokens {
		table.AddRow(token.ID, token.Name, token.Username, display.Scopes(token.Scopes), display.ExpiresAt(token.ExpiresAt), token.Revoked, token.Expired)
	}
	o.io.LogInfof("%s", table.Render())

	return nil
}
