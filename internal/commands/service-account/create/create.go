package create

import (
	"context"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/service-account/serviceaccountutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	io        *iostreams.IOStreams
	apiClient func(repoHost string) (*api.Client, error)
	baseRepo  func() (glrepo.Interface, error)

	name         string
	nameSet      bool
	username     string
	usernameSet  bool
	email        string
	emailSet     bool
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
		Use:               "create [flags]",
		Short:             "Create a service account.",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		Long: heredoc.Docf(`
			Create a service account for a project or a group.

			- Every flag is optional. Without %[1]s--name%[1]s, the account is named "Service account user". Without %[1]s--username%[1]s or %[1]s--email%[1]s, GitLab generates them.
			- A custom email address must be confirmed before the account uses it, unless the top-level group has a matching verified domain or email confirmation is turned off on the instance. Until then, the address is shown as the unconfirmed email.
			- A service account created in a top-level group can be invited to any group or project on the instance. One created in a subgroup can be invited only to that subgroup and its descendants.

			Creating a service account in a subgroup requires GitLab 18.11 or later. Setting %[1]s--email%[1]s for a group service account requires GitLab 17.11 or later.
		`, "`"),
		Example: heredoc.Doc(`
			# Create a service account for the current project
			glab service-account create --name "Release bot"

			# Create it with a chosen username and email address
			glab service-account create --name "Release bot" --username release-bot --email release-bot@example.com

			# Create a service account for a specific project
			glab service-account create --name "Release bot" --repo user/my-repo

			# Create a group service account
			glab service-account create --name "Release bot" --group my-group

			# Create a service account and print its ID from a script
			glab service-account create --name "Release bot" --output json --jq '.id'`),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
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
	fl.StringVarP(&opts.group, "group", "g", "", "Create a service account for a `group` instead of a project. Accepts a group ID or full path.")
	fl.StringVar(&opts.name, "name", "", "Name of the service account.")
	fl.StringVar(&opts.username, "username", "", "The service account's `username`.")
	fl.StringVar(&opts.email, "email", "", "Email address of the service account.")

	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)
	cmd.MarkFlagsMutuallyExclusive("repo", "group")

	return cmd
}

func (o *options) complete(cmd *cobra.Command) error {
	fl := cmd.Flags()
	o.nameSet = fl.Changed("name")
	o.usernameSet = fl.Changed("username")
	o.emailSet = fl.Changed("email")

	group, err := cmdutils.GroupOverride(cmd)
	if err != nil {
		return err
	}
	o.group = group

	return nil
}

func (o *options) run(ctx context.Context) error {
	client, scope, err := serviceaccountutils.NewClient(o.baseRepo, o.apiClient, o.group)
	if err != nil {
		return err
	}

	var name, username, email *string
	if o.nameSet {
		name = &o.name
	}
	if o.usernameSet {
		username = &o.username
	}
	if o.emailSet {
		email = &o.email
	}

	var raw any
	var account serviceaccountutils.Account
	if scope.Group != "" {
		a, _, err := client.Groups.CreateServiceAccount(scope.Group, &gitlab.CreateServiceAccountOptions{
			Name:     name,
			Username: username,
			Email:    email,
		}, gitlab.WithContext(ctx))
		if err != nil {
			return cmdutils.WrapError(err, "failed to create service account")
		}
		raw, account = a, serviceaccountutils.FromGroup(a)
	} else {
		a, _, err := client.Projects.CreateProjectServiceAccount(scope.Project, &gitlab.CreateProjectServiceAccountOptions{
			Name:     name,
			Username: username,
			Email:    email,
		}, gitlab.WithContext(ctx))
		if err != nil {
			return cmdutils.WrapError(err, "failed to create service account")
		}
		raw, account = a, serviceaccountutils.FromProject(a)
	}

	if o.outputFormat == "json" {
		return o.io.PrintJSON(raw)
	}

	serviceaccountutils.PrintDetails(o.io, account)
	o.io.LogErrorf("%s Created service account %d in %s.\n", o.io.Color().GreenCheck(), account.ID, scope)

	return nil
}
