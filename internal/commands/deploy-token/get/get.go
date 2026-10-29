package get

import (
	"context"
	"fmt"
	"strconv"

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

	tokenID      int64
	group        string
	outputFormat string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
		baseRepo:  f.BaseRepo,
	}

	cmd := &cobra.Command{
		Use:               "get <token-id> [flags]",
		Short:             "Get a deploy token.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: cobra.NoFileCompletions,
		Long: heredoc.Docf(`
			Get a single deploy token of a project or a group, by its ID.

			The output never contains the token value. A token value is shown only once, when the token is created.

			Token IDs come from %[1]sglab deploy-token list%[1]s.
		`, "`"),
		Example: heredoc.Doc(`
			# Get deploy token 42 of the current project
			glab deploy-token get 42

			# Get a deploy token of a specific project
			glab deploy-token get 42 --repo user/my-repo

			# Get a group's deploy token
			glab deploy-token get 42 --group group/sub-group

			# Get a deploy token as JSON
			glab deploy-token get 42 --output json`),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd, args); err != nil {
				return err
			}

			return opts.run(cmd.Context())
		},
	}

	cmdutils.EnableRepoOverride(cmd, f)

	fl := cmd.Flags()
	fl.StringVarP(&opts.group, "group", "g", "", "Get a deploy token of the group <group> instead of a project.")

	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)
	cmd.MarkFlagsMutuallyExclusive("repo", "group")

	return cmd
}

func (o *options) complete(cmd *cobra.Command, args []string) error {
	tokenID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		// ExitError renders only the wrapped error, so the rejected value has to
		// be named here. It must not lead the message: the error handler
		// capitalizes the first character, which would rewrite the value itself.
		return cmdutils.WrapError(
			fmt.Errorf("invalid deploy token ID: %q", args[0]),
			"invalid deploy token ID",
		)
	}
	o.tokenID = tokenID

	group, err := cmdutils.GroupOverride(cmd)
	if err != nil {
		return err
	}
	o.group = group

	return nil
}

func (o *options) run(ctx context.Context) error {
	// The group scope works outside a repository, so a missing base repo is only
	// fatal for the project scope. Fail before building a client that would be
	// discarded, so the caller sees why the repository was needed.
	repo, repoErr := o.baseRepo()
	if o.group == "" && repoErr != nil {
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

	var token *gitlab.DeployToken
	switch {
	case o.group != "":
		token, _, err = client.DeployTokens.GetGroupDeployToken(o.group, o.tokenID, gitlab.WithContext(ctx))
	default:
		token, _, err = client.DeployTokens.GetProjectDeployToken(repo.FullName(), o.tokenID, gitlab.WithContext(ctx))
	}
	if err != nil {
		return cmdutils.WrapError(err, "failed to get deploy token")
	}

	// JSON output prints the response as-is, so the token value is cleared here
	// instead of relying on the endpoint never returning it.
	token.Token = ""

	switch o.outputFormat {
	case "json":
		return o.io.PrintJSON(token)
	default:
		return o.printDetails(token)
	}
}

func (o *options) printDetails(token *gitlab.DeployToken) error {
	c := o.io.Color()
	table := tableprinter.NewTablePrinter()
	table.AddRow(c.Bold("ID"), token.ID)
	table.AddRow(c.Bold("Name"), token.Name)
	table.AddRow(c.Bold("Username"), token.Username)
	table.AddRow(c.Bold("Scopes"), display.Scopes(token.Scopes))
	table.AddRow(c.Bold("Expires At"), display.ExpiresAt(token.ExpiresAt))
	table.AddRow(c.Bold("Revoked"), token.Revoked)
	table.AddRow(c.Bold("Expired"), token.Expired)
	o.io.LogInfof("%s", table.Render())

	return nil
}
