package remove

import (
	"fmt"
	"strconv"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	userID   int64
	username string

	baseRepo     func() (glrepo.Interface, error)
	gitlabClient func() (*gitlab.Client, error)
	io           *iostreams.IOStreams
}

func newOptions(f cmdutils.Factory) *options {
	return &options{
		io:           f.IO(),
		baseRepo:     f.BaseRepo,
		gitlabClient: f.GitLabClient,
	}
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := newOptions(f)

	cmd := &cobra.Command{
		Use:   "remove [flags]",
		Short: `Remove a member from the project.`,
		Long: heredoc.Doc(`
			Remove a member from the project.
		`),
		Example: heredoc.Doc(`
			# Remove a user by username
			glab repo members remove --username=john.doe

			# Remove a user by ID
			glab repo members remove --user-id=123`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Safe: "false",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(); err != nil {
				return err
			}
			return opts.run()
		},
	}

	cmdutils.EnableRepoOverride(cmd, f)

	fl := cmd.Flags()
	fl.Int64VarP(&opts.userID, "user-id", "u", 0, "User ID instead of username.")
	fl.StringVarP(&opts.username, "username", "", "", "Username instead of user-id.")
	cmd.MarkFlagsMutuallyExclusive("username", "user-id")

	return cmd
}

func (o *options) validate() error {
	if o.username == "" && o.userID == 0 {
		return fmt.Errorf("either username or user-id must be specified")
	}

	if o.username != "" && o.userID != 0 {
		return fmt.Errorf("cannot specify both username and user-id")
	}

	return nil
}

func (o *options) run() error {
	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	repo, err := o.baseRepo()
	if err != nil {
		return err
	}

	var userIDToRemove int64
	var userIdentifier string

	switch {
	case o.userID != 0:
		userIDToRemove = o.userID
		userIdentifier = strconv.FormatInt(o.userID, 10)
	case o.username != "":
		// Get user ID from username
		users, _, err := client.Users.ListUsers(&gitlab.ListUsersOptions{
			Username: new(o.username),
		})
		if err != nil {
			return fmt.Errorf("failed to find user %s: %w", o.username, err)
		}
		if len(users) == 0 {
			return fmt.Errorf("user %s not found", o.username)
		}
		if len(users) > 1 {
			return fmt.Errorf("multiple users found with username %s", o.username)
		}

		userIDToRemove = users[0].ID
		userIdentifier = o.username
	}

	_, err = client.ProjectMembers.DeleteProjectMember(repo.FullName(), userIDToRemove)
	if err != nil {
		return fmt.Errorf("failed to remove member %s: %w", userIdentifier, err)
	}

	c := o.io.Color()
	fmt.Fprintf(o.io.StdOut, "%s Successfully removed %s from %s\n",
		c.GreenCheck(),
		c.Bold(userIdentifier),
		c.Bold(repo.FullName()))

	return nil
}
