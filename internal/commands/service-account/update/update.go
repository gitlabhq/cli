package update

import (
	"context"
	"fmt"
	"strings"

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

	account      string
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
		Use:               "update <id | username> [flags]",
		Short:             "Update a service account.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: cobra.NoFileCompletions,
		Long: heredoc.Docf(`
			Update the name, username, or email address of a service account of a project or a group, by its ID or username.

			- Only the given fields change. At least one of %[1]s--name%[1]s, %[1]s--username%[1]s, or %[1]s--email%[1]s is required.
			- A field cannot be cleared, so an empty value is rejected.
			- A numeric argument is always treated as an ID. IDs and usernames come from %[1]sglab service-account list%[1]s.
			- A new custom email address must be confirmed before the account uses it, unless the top-level group has a matching verified domain or email confirmation is turned off on the instance. Until then, the address is shown as the unconfirmed email.
			- The username of a service account associated with a composite identity cannot be changed.

			Updating a group service account requires GitLab 17.10 or later. Changing the email address requires GitLab 18.2 or later.
		`, "`"),
		Example: heredoc.Doc(`
			# Rename service account 42 of the current project
			glab service-account update 42 --name "Release bot"

			# Change the username of a service account, found by its current username
			glab service-account update release-bot --username release-bot-2

			# Change the email address of a service account of a specific project
			glab service-account update 42 --email release-bot@example.com --repo user/my-repo

			# Rename a group's service account
			glab service-account update 42 --name "Release bot" --group my-group`),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd, args); err != nil {
				return err
			}
			if err := opts.validate(); err != nil {
				return err
			}

			return opts.run(cmd.Context())
		},
	}

	cmdutils.EnableRepoOverride(cmd, f)

	fl := cmd.Flags()
	fl.StringVarP(&opts.group, "group", "g", "", "Update a service account of a `group` instead of a project. Accepts a group ID or full path.")
	fl.StringVar(&opts.name, "name", "", "New name of the service account.")
	fl.StringVar(&opts.username, "username", "", "New `username` of the service account.")
	fl.StringVar(&opts.email, "email", "", "New email address of the service account.")

	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)
	cmd.MarkFlagsOneRequired("name", "username", "email")
	cmd.MarkFlagsMutuallyExclusive("repo", "group")

	return cmd
}

func (o *options) complete(cmd *cobra.Command, args []string) error {
	o.account = args[0]

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

// validate rejects blank values, which GitLab drops without an error, so the
// update would otherwise report success and change nothing.
func (o *options) validate() error {
	for _, f := range []struct {
		name  string
		set   bool
		value string
	}{
		{"name", o.nameSet, o.name},
		{"username", o.usernameSet, o.username},
		{"email", o.emailSet, o.email},
	} {
		if f.set && strings.TrimSpace(f.value) == "" {
			return &cmdutils.FlagError{Err: fmt.Errorf("--%s can't be empty", f.name)}
		}
	}

	return nil
}

func (o *options) run(ctx context.Context) error {
	client, scope, err := serviceaccountutils.NewClient(o.baseRepo, o.apiClient, o.group)
	if err != nil {
		return err
	}

	id, err := serviceaccountutils.ResolveID(ctx, client, scope, o.account)
	if err != nil {
		return cmdutils.WrapError(err, "failed to update service account")
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
		a, _, err := client.Groups.UpdateServiceAccount(scope.Group, id, &gitlab.UpdateServiceAccountOptions{
			Name:     name,
			Username: username,
			Email:    email,
		}, gitlab.WithContext(ctx))
		if err != nil {
			return cmdutils.WrapError(err, "failed to update service account")
		}
		raw, account = a, serviceaccountutils.FromGroup(a)
	} else {
		a, _, err := client.Projects.UpdateProjectServiceAccount(scope.Project, id, &gitlab.UpdateProjectServiceAccountOptions{
			Name:     name,
			Username: username,
			Email:    email,
		}, gitlab.WithContext(ctx))
		if err != nil {
			return cmdutils.WrapError(err, "failed to update service account")
		}
		raw, account = a, serviceaccountutils.FromProject(a)
	}

	if o.outputFormat == "json" {
		return o.io.PrintJSON(raw)
	}

	serviceaccountutils.PrintDetails(o.io, account)
	o.io.LogErrorf("%s Updated service account %d in %s.\n", o.io.Color().GreenCheck(), account.ID, scope)

	return nil
}
