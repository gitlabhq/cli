package add

import (
	"errors"
	"fmt"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

type options struct {
	projectID       string
	remoteName      string
	protocol        string
	repoHost        string
	defaultHostname string

	io        *iostreams.IOStreams
	remotes   func() (glrepo.Remotes, error)
	apiClient func(repoHost string) (*api.Client, error)
	config    func() config.Config
}

func NewCmdRemoteAdd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:              f.IO(),
		remotes:         f.Remotes,
		apiClient:       f.ApiClient,
		config:          f.Config,
		defaultHostname: f.DefaultHostname(),
	}

	cmd := &cobra.Command{
		Use:   "add <namespace/project>",
		Short: "Add a Git remote for a GitLab project.",
		Long: heredoc.Doc(`
			Add a Git remote for a GitLab project using a project reference.

			The remote name defaults to the first path component (the namespace),
			so the remote identifies where the repository lives.
		`),
		Example: heredoc.Doc(`
			# Add a remote repository (remote named "alice")
			glab repo remote add alice/my-project

			# Add a remote repository with a custom name
			glab repo remote add alice/my-project --name upstream

			# Add a remote repository in a subgroup (remote named "group")
			glab repo remote add group/subgroup/my-project`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(args); err != nil {
				return err
			}
			if err := opts.validate(); err != nil {
				return err
			}
			return opts.run()
		},
	}

	cmd.Flags().StringVarP(&opts.remoteName, "name", "n", "", "Name for the remote (default: first path component)")
	cmd.Flags().StringVarP(&opts.protocol, "protocol", "p", "", "Git protocol: ssh, https (default: git_protocol config)")

	return cmd
}

func (o *options) complete(args []string) error {
	o.projectID = args[0]

	if o.remoteName == "" {
		o.remoteName = defaultRemoteName(o.projectID)
	}

	repo, err := glrepo.FromFullName(o.projectID, o.defaultHostname, o.config())
	if err != nil {
		return fmt.Errorf("invalid project reference: %w", err)
	}
	o.repoHost = repo.RepoHost()

	if o.protocol == "" {
		o.protocol, _ = o.config().Get(o.repoHost, "git_protocol")
	}

	return nil
}

func (o *options) validate() error {
	if o.protocol != "ssh" && o.protocol != "https" {
		return cmdutils.WrapError(
			errors.New("invalid protocol"),
			fmt.Sprintf("protocol must be 'ssh' or 'https', got %q", o.protocol),
		)
	}

	remotes, err := o.remotes()
	if err != nil {
		return fmt.Errorf("failed to list remotes: %w", err)
	}
	if existing, _ := remotes.FindByName(o.remoteName); existing != nil {
		return fmt.Errorf("remote %q already exists", o.remoteName)
	}

	return nil
}

func (o *options) run() error {
	apiClient, err := o.apiClient(o.repoHost)
	if err != nil {
		return err
	}

	project, err := api.GetProject(apiClient.Lab(), o.projectID)
	if err != nil {
		return fmt.Errorf("failed to find project %q: %w", o.projectID, err)
	}

	remoteURL := glrepo.RemoteURL(project, o.protocol)

	if _, err = git.AddRemote(o.remoteName, remoteURL); err != nil {
		return cmdutils.WrapError(err, "failed to add remote")
	}

	o.io.LogInfof("%s Remote %q added using %s protocol.\n",
		o.io.Color().GreenCheck(), o.remoteName, o.protocol)

	return nil
}

func defaultRemoteName(projectID string) string {
	parts := strings.Split(projectID, "/")
	return parts[0]
}
