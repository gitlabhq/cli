package update

import (
	"fmt"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

const (
	archiveFlag       = "archive"
	defaultBranchFlag = "defaultBranch"
	descriptionFlag   = "description"
)

type options struct {
	apiClient       func(repoHost string) (*api.Client, error)
	io              *iostreams.IOStreams
	baseRepo        func() (glrepo.Interface, error)
	gitlabClient    func() (*gitlab.Client, error)
	defaultHostname func() string

	archive       *bool
	defaultBranch *string
	description   *string
	projectID     string
}

func NewCmdUpdate(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		apiClient:       f.ApiClient,
		io:              f.IO(),
		baseRepo:        f.BaseRepo,
		gitlabClient:    f.GitLabClient,
		defaultHostname: f.DefaultHostname,
	}

	cmd := &cobra.Command{
		Use:   "update [path] [flags]",
		Short: `Update an existing GitLab project or repository.`,
		Args:  cobra.MaximumNArgs(1),
		Example: heredoc.Doc(`
			# Update the description for my-project.
			glab repo update my-project --description "This project is cool."

			# Update the default branch for my-project.
			glab repo update my-project --defaultBranch main

			# Archive my-project.
			glab repo update my-project --archive
			glab repo update my-project --archive=true

			# Unarchive my-project.
			glab repo update my-project --archive=false`),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd.Flags(), args); err != nil {
				return err
			}
			return opts.run()
		},
	}

	cmd.Flags().Bool(archiveFlag, false, "Whether the project should be archived. (default false)")
	cmd.Flags().String(defaultBranchFlag, "", "New default branch for the project.")
	cmd.Flags().StringP(descriptionFlag, "d", "", "New description for the project.")
	cmd.MarkFlagsOneRequired(archiveFlag, defaultBranchFlag, descriptionFlag)
	return cmd
}

func (o *options) complete(flags *pflag.FlagSet, args []string) error {
	if flags.Changed(archiveFlag) {
		archive, err := flags.GetBool(archiveFlag)
		if err != nil {
			return err
		}
		o.archive = &archive
	}
	if flags.Changed(defaultBranchFlag) {
		defaultBranch, err := flags.GetString(defaultBranchFlag)
		if err != nil {
			return err
		}
		o.defaultBranch = &defaultBranch
	}
	if flags.Changed(descriptionFlag) {
		description, err := flags.GetString(descriptionFlag)
		if err != nil {
			return err
		}
		o.description = &description
	}

	if len(args) == 1 {
		o.projectID = args[0]
	}
	return nil
}

func (o *options) run() error {
	repo, err := o.getRepoFromProjectID()
	if err != nil {
		return err
	}

	client, err := o.apiClient(repo.RepoHost())
	if err != nil {
		return err
	}
	apiClient := client.Lab()

	var project *gitlab.Project
	if o.settingsChanged() {
		project, _, err = apiClient.Projects.EditProject(repo.FullName(), &gitlab.EditProjectOptions{
			DefaultBranch: o.defaultBranch,
			Description:   o.description,
		})
		if err != nil {
			return fmt.Errorf("updating project: %w", err)
		}
	}

	// Handle archive flag separately - this uses a separate API endpoint
	if o.archive != nil {
		if *o.archive {
			project, _, err = apiClient.Projects.ArchiveProject(repo.FullName())
			if err != nil {
				return fmt.Errorf("archiving project: %w", err)
			}
		} else {
			project, _, err = apiClient.Projects.UnarchiveProject(repo.FullName())
			if err != nil {
				return fmt.Errorf("unarchiving project: %w", err)
			}
		}
	}

	greenCheck := o.io.Color().Green("✓")
	fmt.Fprintf(o.io.StdOut, "%s Updated repository %s on GitLab: %s\n", greenCheck, project.NameWithNamespace, project.WebURL)
	return nil
}

// getRepoFromProjectID wrangles the various ways of specifying a project into
// a repository object. The accepted input formats are:
//   - no project specified - assume the current dir is a Git repository and
//     look up the remote
//   - just project name - need to look up user's username and prefix it with a
//     slash
//   - user/project - can be passed verbatim to the API
//   - fully qualified GitLab URL
func (o *options) getRepoFromProjectID() (glrepo.Interface, error) {
	projectID := o.projectID
	// No project argument - use current repository
	if projectID == "" {
		// Configure client to have host of current repository
		repo, err := o.baseRepo()
		if err != nil {
			return nil, cmdutils.WrapError(err, "`repository` is required when not running in a Git repository.")
		}
		return repo, nil
	} else {
		// If the ProjectID is a single token, use current user's namespace
		if !strings.Contains(projectID, "/") {
			client, err := o.gitlabClient()
			if err != nil {
				return nil, err
			}
			currentUser, _, err := client.Users.CurrentUser()
			if err != nil {
				return nil, cmdutils.WrapError(err, "Failed to retrieve your current user.")
			}

			projectID = currentUser.Username + "/" + projectID
		}

		// Get the repo full name from the ProjectID which can be a full URL or a group/repo format
		return glrepo.FromFullName(projectID, o.defaultHostname())
	}
}

// settingsChanged returns true if a "settings" flag has been changed. These
// are the flags that require a call to the EditProject API to change. They
// include, for example, the description and default branch, but do not include
// the "archive" flag.
func (o *options) settingsChanged() bool {
	return o.defaultBranch != nil || o.description != nil
}
