package mirror

import (
	"errors"
	"fmt"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	url                   string
	direction             string
	enabled               bool
	protectedBranchesOnly bool
	allowDivergence       bool
	projectID             int64

	io              *iostreams.IOStreams
	baseRepo        glrepo.Interface
	apiClient       func(repoHost string) (*api.Client, error)
	gitlabClient    func() (*gitlab.Client, error)
	client          *gitlab.Client
	baseRepoFactory func() (glrepo.Interface, error)
	defaultHostname string
}

func NewCmdMirror(f cmdutils.Factory) *cobra.Command {
	opts := options{
		io:              f.IO(),
		apiClient:       f.ApiClient,
		gitlabClient:    f.GitLabClient,
		baseRepoFactory: f.BaseRepo,
		defaultHostname: f.DefaultHostname(),
	}

	projectMirrorCmd := &cobra.Command{
		Use:   "mirror [ID | URL | PATH] [flags]",
		Short: "Configure mirroring on an existing project to sync with a remote repository.",
		Long: heredoc.Docf(`
			Configure repository mirroring for an existing GitLab project.

			The GitLab project must already exist. This command configures mirroring
			on existing projects but does not create new projects.

			Mirror types:

			- Pull mirror: Syncs changes from an external repository to your GitLab project. The external repository is the source of truth. Use pull mirrors to sync from GitHub, Bitbucket, or other GitLab instances.
			- Push mirror: Syncs changes from your GitLab project to an external repository. Your GitLab project is the source of truth. Use push mirrors to sync to GitHub, Bitbucket, or other GitLab instances.

			Authentication:

			- For pull mirrors from private repositories, embed credentials in the URL: %[1]shttps://username:token@gitlab.example.com/org/repo%[1]s
			- For push mirrors to private repositories, configure credentials in the GitLab UI or use SSH URLs with deploy keys.
		`, "`"),
		Example: heredoc.Docf(`
			# Create a project, then configure pull mirroring
			glab repo create mygroup/myproject --public
			glab repo mirror mygroup/myproject --direction=pull --url=%[1]shttps://gitlab.example.com/org/repo%[1]s

			# Configure pull mirroring from a private repository
			glab repo mirror mygroup/myproject --direction=pull --url=%[1]shttps://username:token@gitlab.example.com/org/private-repo%[1]s

			# Configure pull mirroring for protected branches only
			glab repo mirror mygroup/myproject --direction=pull --url=%[1]shttps://gitlab.example.com/org/repo%[1]s --protected-branches-only

			# Configure push mirroring to another GitLab instance
			glab repo mirror mygroup/myproject --direction=push --url=%[1]shttps://gitlab-backup.example.com/backup/myproject%[1]s

			# Configure push mirroring and allow divergent refs
			glab repo mirror mygroup/myproject --direction=push --url=%[1]shttps://gitlab-backup.example.com/backup/repo%[1]s --allow-divergence`, `"`),
		Args: cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
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
	projectMirrorCmd.Flags().StringVar(&opts.url, "url", "", "The target URL to which the repository is mirrored.")
	projectMirrorCmd.Flags().StringVar(&opts.direction, "direction", "pull", "Mirror direction. Options: pull, push.")
	projectMirrorCmd.Flags().BoolVar(&opts.enabled, "enabled", true, "Determines if the mirror is enabled.")
	projectMirrorCmd.Flags().BoolVar(&opts.protectedBranchesOnly, "protected-branches-only", false, "Determines if only protected branches are mirrored. (default false)")
	projectMirrorCmd.Flags().BoolVar(&opts.allowDivergence, "allow-divergence", false, "Determines if divergent refs are skipped. (default false)")

	_ = projectMirrorCmd.MarkFlagRequired("url")
	_ = projectMirrorCmd.MarkFlagRequired("direction")

	return projectMirrorCmd
}

func (o *options) complete(args []string) error {
	if len(args) > 0 {
		baseRepo, err := glrepo.FromFullName(args[0], o.defaultHostname)
		if err != nil {
			return err
		}
		o.baseRepo = baseRepo

		o.gitlabClient = func() (*gitlab.Client, error) {
			if o.client != nil {
				return o.client, nil
			}
			c, err := o.apiClient(o.baseRepo.RepoHost())
			if err != nil {
				return nil, err
			}
			o.client = c.Lab()
			return o.client, nil
		}

	} else {
		baseRepo, err := o.baseRepoFactory()
		if err != nil {
			return err
		}
		o.baseRepo = baseRepo
	}

	o.url = strings.TrimSpace(o.url)

	client, err := o.gitlabClient()
	if err != nil {
		return err
	}
	o.client = client

	project, err := o.baseRepo.Project(o.client)
	if err != nil {
		return cmdutils.WrapError(
			err,
			"Failed to find project. The project must exist before you can configure mirroring. Create it with 'glab repo create'.",
		)
	}
	o.projectID = project.ID

	return nil
}

func (o *options) validate() error {
	if o.direction != "pull" && o.direction != "push" {
		return cmdutils.WrapError(
			errors.New("invalid choice for --direction"),
			"the argument direction value should be 'pull' or 'push'.",
		)
	}

	if o.direction == "pull" && o.allowDivergence {
		fmt.Fprintf(
			o.io.StdOut,
			"[Warning] the 'allow-divergence' flag has no effect for pull mirroring, and is ignored.\n",
		)
	}

	return nil
}

func (o *options) run() error {
	if o.direction == "push" {
		return o.createPushMirror()
	} else {
		return o.createPullMirror()
	}
}

func (o *options) createPushMirror() error {
	pm, _, err := o.client.ProjectMirrors.AddProjectMirror(o.projectID, &gitlab.AddProjectMirrorOptions{
		URL:                   new(o.url),
		Enabled:               new(o.enabled),
		OnlyProtectedBranches: new(o.protectedBranchesOnly),
		KeepDivergentRefs:     new(o.allowDivergence),
	})
	if err != nil {
		return cmdutils.WrapError(err, "Failed to create push mirror. Check if the project exists and ensure you have the necessary permissions.")
	}
	greenCheck := o.io.Color().Green("✓")
	fmt.Fprintf(
		o.io.StdOut,
		"%s Created push mirror for %s (%d) on GitLab at %s (%d).\n",
		greenCheck, pm.URL, pm.ID, o.baseRepo.FullName(), o.projectID,
	)
	return err
}

func (o *options) createPullMirror() error {
	_, _, err := o.client.Projects.EditProject(o.projectID, &gitlab.EditProjectOptions{
		ImportURL:                   new(o.url),
		Mirror:                      new(o.enabled),
		OnlyMirrorProtectedBranches: new(o.protectedBranchesOnly),
	})
	if err != nil {
		return cmdutils.WrapError(err, "Failed to create pull mirror. Check if the project exists and ensure you have the necessary permissions.")
	}
	greenCheck := o.io.Color().Green("✓")
	fmt.Fprintf(
		o.io.StdOut,
		"%s Created pull mirror for %s on GitLab at %s (%d).\n",
		greenCheck, o.url, o.baseRepo.FullName(), o.projectID,
	)
	return err
}
