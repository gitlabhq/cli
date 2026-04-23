package delete

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	forceDelete bool
	repoName    string
	args        []string

	io        *iostreams.IOStreams
	apiClient func(repoHost string) (*api.Client, error)
	baseRepo  func() (glrepo.Interface, error)
}

func NewCmdDelete(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
		baseRepo:  f.BaseRepo,
	}

	projectCreateCmd := &cobra.Command{
		Use:   "delete [<NAMESPACE>/]<NAME>",
		Short: `Delete an existing project on GitLab.`,
		Long: heredoc.Doc(`
			Delete an existing project on GitLab.

			This permanently deletes the entire project, including:
			
			- The Git repository.
			- Issues and merge requests.
			- Wiki pages.
			- CI/CD pipelines and job artifacts.
			- Other project content and settings.

			This action cannot be undone.
		`),
		Args: cobra.MaximumNArgs(1),
		Example: heredoc.Doc(`
			# Delete a personal project.
			glab repo delete dotfiles

			# Delete a project in a GitLab group, or another project
			# you have write access to:
			glab repo delete mygroup/dotfiles
			glab repo delete myorg/mynamespace/dotfiles`),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.args = args
			return opts.run(cmd.Context())
		},
	}

	projectCreateCmd.Flags().BoolVarP(&opts.forceDelete, "yes", "y", false, "Skip the confirmation prompt and immediately delete the project. (default false)")

	return projectCreateCmd
}

func (o *options) run(ctx context.Context) error {
	c, err := o.apiClient("")
	if err != nil {
		return err
	}
	gitlabClient := c.Lab()

	baseRepo, baseRepoErr := o.baseRepo() // don't handle error yet
	if len(o.args) == 1 {
		o.repoName = o.args[0]

		if !strings.ContainsRune(o.repoName, '/') {
			namespace := ""
			if baseRepoErr == nil {
				namespace = baseRepo.RepoOwner()
			} else {
				currentUser, _, err := gitlabClient.Users.CurrentUser()
				if err != nil {
					return err
				}
				namespace = currentUser.Username
			}
			o.repoName = fmt.Sprintf("%s/%s", namespace, o.repoName)
		}
	} else {
		if baseRepoErr != nil {
			return baseRepoErr
		}
		o.repoName = baseRepo.FullName()
	}

	if !o.forceDelete && !o.io.PromptEnabled() {
		return &cmdutils.FlagError{Err: fmt.Errorf("--yes or -y flag is required when not running interactively.")}
	}

	if !o.forceDelete && o.io.PromptEnabled() {
		fmt.Fprintf(o.io.StdErr, "This action will permanently delete the project %s immediately, including its repositories and all content: issues, merge requests, wiki, CI/CD data, and all other project resources.\n\n", o.repoName)
		err = o.io.Confirm(ctx, &o.forceDelete, fmt.Sprintf("Are you ABSOLUTELY SURE you wish to delete %s?", o.repoName))
		if err != nil {
			return err
		}
	}

	if o.forceDelete {
		if o.io.IsErrTTY && o.io.IsaTTY {
			fmt.Fprintf(o.io.StdErr, "- Deleting project %s\n", o.repoName)
		}
		resp, err := gitlabClient.Projects.DeleteProject(o.repoName, nil)
		if err != nil && resp == nil {
			return err
		}
		if resp.StatusCode == http.StatusUnauthorized {
			return fmt.Errorf("you are not authorized to delete %s.\nCheck your token used for glab. Make sure it has the `api` and `write_repository` scopes enabled.", o.repoName)
		}
		return err
	}
	fmt.Fprintln(o.io.StdErr, "aborted by user")
	return nil
}
