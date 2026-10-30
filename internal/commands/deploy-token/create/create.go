package create

import (
	"context"
	"time"

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

	name         string
	scopes       []string
	username     string
	usernameSet  bool
	expiresAt    time.Time
	expiresAtSet bool
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
		Use:               "create <name> [flags]",
		Short:             "Create a deploy token.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: cobra.NoFileCompletions,
		Long: heredoc.Docf(`
			Create a deploy token for a project or a group.

			The token value is printed only once, when the token is created, and cannot be retrieved later. Use %[1]s--output json%[1]s to capture it from a script.

			A deploy token authenticates as a username and token pair, so both values from the output are needed to use it.

			The token does not expire unless %[1]s--expires-at%[1]s is given.

			Every token must be granted at least one scope with %[1]s--scope%[1]s. For the scopes you can grant, see https://docs.gitlab.com/user/project/deploy_tokens/#scope.
		`, "`"),
		Example: heredoc.Doc(`
			# Create a read-only deploy token for the current project
			glab deploy-token create ci-reader --scope read_repository

			# Create a token that can also pull images from the container registry
			glab deploy-token create ci-reader --scope read_repository --scope read_registry

			# Create a token for a specific project, with a custom username
			glab deploy-token create ci-reader --scope read_repository --username ci-reader --repo user/my-repo

			# Create a token that is valid until the end of 2027
			glab deploy-token create ci-reader --scope read_repository --expires-at 2028-01-01

			# Create a group deploy token
			glab deploy-token create ci-reader --scope read_repository --group group/sub-group

			# Create the token that CI/CD jobs receive as CI_DEPLOY_USER and CI_DEPLOY_PASSWORD
			glab deploy-token create gitlab-deploy-token --scope read_registry

			# Create a token and print its username and value from a script
			glab deploy-token create ci-reader --scope read_repository --output json --jq '"\(.username) \(.token)"'`),
		Annotations: map[string]string{
			// Creating a token prints a secret, so this command is kept out of MCP,
			// as glab token create is.
			mcpannotations.Exclude: "true",
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
	fl.StringVarP(&opts.group, "group", "g", "", "Create a deploy token for a `group` instead of a project. Accepts a group ID or full path.")
	fl.StringSliceVarP(&opts.scopes, "scope", "S", nil, "Scope to grant the token. Multiple scopes can be comma-separated or specified by repeating the flag.")
	fl.StringVar(&opts.username, "username", "", "Username for the token. Defaults to 'gitlab+deploy-token-<id>'.")
	fl.TimeVarP(&opts.expiresAt, "expires-at", "E", time.Time{}, []string{time.DateOnly, time.RFC3339}, "Expiry of the token, as YYYY-MM-DD (00:00 UTC) or an RFC 3339 timestamp. The token does not expire if omitted.")

	cobra.CheckErr(cmd.MarkFlagRequired("scope"))

	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)
	cmd.MarkFlagsMutuallyExclusive("repo", "group")

	return cmd
}

func (o *options) complete(cmd *cobra.Command, args []string) error {
	o.name = args[0]
	o.usernameSet = cmd.Flags().Changed("username")
	o.expiresAtSet = cmd.Flags().Changed("expires-at")

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

	// The API treats an omitted expiry as "never expires" and an omitted username
	// as "assign the default one", so neither is sent unless it was asked for.
	// Both are keyed off the flag rather than its value: the zero time is a
	// parseable date, so a value test would read 0001-01-01 back as "no expiry".
	var expiresAt *time.Time
	if o.expiresAtSet {
		expiresAt = &o.expiresAt
	}

	var username *string
	if o.usernameSet {
		username = &o.username
	}

	var (
		token  *gitlab.DeployToken
		target string
	)
	switch {
	case o.group != "":
		target = "group " + o.group
		token, _, err = client.DeployTokens.CreateGroupDeployToken(o.group, &gitlab.CreateGroupDeployTokenOptions{
			Name:      &o.name,
			Scopes:    &o.scopes,
			Username:  username,
			ExpiresAt: expiresAt,
		}, gitlab.WithContext(ctx))
	default:
		target = "project " + repo.FullName()
		token, _, err = client.DeployTokens.CreateProjectDeployToken(repo.FullName(), &gitlab.CreateProjectDeployTokenOptions{
			Name:      &o.name,
			Scopes:    &o.scopes,
			Username:  username,
			ExpiresAt: expiresAt,
		}, gitlab.WithContext(ctx))
	}
	if err != nil {
		return cmdutils.WrapError(err, "failed to create deploy token")
	}

	switch o.outputFormat {
	case "json":
		return o.io.PrintJSON(token)
	default:
		return o.printDetails(token, target)
	}
}

func (o *options) printDetails(token *gitlab.DeployToken, target string) error {
	c := o.io.Color()
	table := tableprinter.NewTablePrinter()
	table.AddRow(c.Bold("ID"), token.ID)
	table.AddRow(c.Bold("Name"), token.Name)
	table.AddRow(c.Bold("Username"), token.Username)
	table.AddRow(c.Bold("Scopes"), display.Scopes(token.Scopes))
	table.AddRow(c.Bold("Expires At"), display.ExpiresAt(token.ExpiresAt))
	table.AddRow(c.Bold("Token"), token.Token)
	o.io.LogInfof("%s", table.Render())

	o.io.LogErrorf("%s Created a deploy token for %s. Store the token value now. It is not shown again.\n", c.WarnIcon(), target)

	return nil
}
