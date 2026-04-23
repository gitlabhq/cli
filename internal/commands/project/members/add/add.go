package add

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	role      string
	roleID    int64
	expiresAt string
	userID    int
	username  string

	baseRepo     func() (glrepo.Interface, error)
	gitlabClient func() (*gitlab.Client, error)
	io           *iostreams.IOStreams
}

var roles = map[string]gitlab.AccessLevelValue{
	"guest":      gitlab.GuestPermissions,
	"reporter":   gitlab.ReporterPermissions,
	"developer":  gitlab.DeveloperPermissions,
	"maintainer": gitlab.MaintainerPermissions,
	"owner":      gitlab.OwnerPermissions,
}

var (
	//go:embed long.md
	longHelp string
	//go:embed example.md
	exampleHelp string
)

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
		Use:     "add [flags]",
		Short:   `Add a member to the project.`,
		Long:    longHelp,
		Example: strings.Trim(exampleHelp, "\n\r"),
		Args:    cobra.NoArgs,
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
	fl.StringVarP(&opts.role, "role", "r", "developer", "Role for the user (guest, reporter, developer, maintainer, owner)")
	fl.Int64Var(&opts.roleID, "role-id", 0, "ID of a custom role defined in the project or group.")
	fl.StringVarP(&opts.expiresAt, "expires-at", "e", "", "Expiration date for the membership (YYYY-MM-DD)")
	fl.IntVarP(&opts.userID, "user-id", "u", 0, "User ID instead of username.")
	fl.StringVarP(&opts.username, "username", "", "", "Username instead of user-id.")
	cmd.MarkFlagsMutuallyExclusive("username", "user-id")
	cmd.MarkFlagsMutuallyExclusive("role", "role-id")

	return cmd
}

func (o *options) validate() error {
	if o.username == "" && o.userID == 0 {
		return fmt.Errorf("either username or user-id must be specified")
	}

	if o.roleID != 0 {
		// Custom role: no further validation here, but API will validate
		if o.role != "developer" { // Reset to default if role-id is set
			o.role = ""
		}
	} else {
		if _, ok := roles[o.role]; !ok {
			return fmt.Errorf("invalid role: %s. Valid roles are: guest, reporter, developer, maintainer, owner", o.role)
		}
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

	var userIDToAdd int
	var userIdentifier string

	switch {
	case o.userID != 0:
		userIDToAdd = o.userID
		userIdentifier = strconv.Itoa(o.userID)
	case o.username != "":
		// Get user ID from username
		users, _, err := client.Users.ListUsers(&gitlab.ListUsersOptions{
			Username: &o.username,
		})
		if err != nil {
			return fmt.Errorf("failed to find user %s: %w", o.username, err)
		}
		switch len(users) {
		case 0:
			return fmt.Errorf("user %s not found", o.username)
		case 1:
		default:
			return fmt.Errorf("multiple users found with username %s", o.username)
		}

		userIDToAdd = int(users[0].ID)
		userIdentifier = o.username
	}

	addOptions := &gitlab.AddProjectMemberOptions{}

	if o.roleID != 0 {
		addOptions.MemberRoleID = new(o.roleID)
	} else {
		role := roles[o.role]
		addOptions.AccessLevel = new(role)
	}

	if o.expiresAt != "" {
		addOptions.ExpiresAt = new(o.expiresAt)
	}

	addOptions.UserID = new(userIDToAdd)
	member, _, err := client.ProjectMembers.AddProjectMember(repo.FullName(), addOptions)
	if err != nil {
		return fmt.Errorf("failed to add member %s: %w", userIdentifier, err)
	}

	c := o.io.Color()
	if o.roleID != 0 {
		fmt.Fprintf(o.io.StdOut, "%s Successfully added %s with custom role ID %d to %s\n",
			c.GreenCheck(),
			c.Bold(member.Username),
			o.roleID,
			c.Bold(repo.FullName()))
	} else {
		fmt.Fprintf(o.io.StdOut, "%s Successfully added %s as %s to %s\n",
			c.GreenCheck(),
			c.Bold(member.Username),
			c.Bold(o.role),
			c.Bold(repo.FullName()))
	}

	if o.expiresAt != "" {
		fmt.Fprintf(o.io.StdOut, "  Membership expires on: %s\n", o.expiresAt)
	}

	return nil
}
