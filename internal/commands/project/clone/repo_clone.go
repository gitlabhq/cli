package clone

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	groupName         string
	includeSubgroups  bool
	preserveNamespace bool
	withMREnabled     bool
	withIssuesEnabled bool
	withShared        bool
	archived          bool
	archivedSet       bool
	active            bool
	activeSet         bool
	visibility        string
	owned             bool
	gitFlags          []string
	dir               string
	host              string
	protocol          string

	page     int
	perPage  int
	paginate bool

	io        *iostreams.IOStreams
	apiClient *api.Client

	currentUser *gitlab.User
}

type ContextOpts struct {
	Project *gitlab.Project
	Repo    string
}

func NewCmdClone(f cmdutils.Factory, runE func(*options, *ContextOpts) error) *cobra.Command {
	opts := &options{
		gitFlags: []string{},
		io:       f.IO(),
	}

	ctxOpts := &ContextOpts{}

	repoCloneCmd := &cobra.Command{
		Use:   `clone [<repo> | -g <group>] [<dir>] [flags] [-- <gitflags>...]`,
		Short: `Clone a GitLab repository or project.`,
		Example: heredoc.Doc(`
			# Clones repository into current directory
			glab repo clone gitlab-org/cli
			glab repo clone https://gitlab.com/gitlab-org/cli

			# Clones repository into 'mydirectory'
			glab repo clone gitlab-org/cli mydirectory

			# Clones repository 'glab' for current user
			glab repo clone glab

			# Finds the project by the ID provided and clones it
			glab repo clone 4356677

			# Clones a specific branch
			glab repo clone gitlab-org/cli -- --branch development

			# Clones with a shallow clone (depth 1)
			glab repo clone gitlab-org/cli -- --depth 1

			# Clones with multiple Git flags
			glab repo clone gitlab-org/cli -- --branch main --single-branch --depth 1

			# Clones all repos in a group
			glab repo clone -g everyonecancontribute --paginate

			# Clones all non-archived repos in a group
			glab repo clone -g everyonecancontribute --archived=false --paginate

			# Clones only active projects in a group
			glab repo clone -g everyonecancontribute --active=true --paginate

			# Clones from a GitLab Self-Managed or GitLab Dedicated instance
			GITLAB_HOST=salsa.debian.org glab repo clone myrepo`),
		Long: heredoc.Docf(`
		Clone a GitLab repository to your local machine. Specify the
		repository by name, namespace/repo path, full URL, or project ID.

		The command uses your configured protocol (SSH or HTTPS).

		To pass Git clone flags, add them after %[1]s--%[1]s. For example:
		%[1]sglab repo clone <repo> -- --branch <branch-name>%[1]s

		When you clone a fork you own, the command adds an %[1]supstream%[1]s
		remote that points to the parent project.
		`, "`"),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// Move arguments after "--" to gitFlags
			if dashPos := cmd.ArgsLenAtDash(); dashPos != -1 {
				opts.gitFlags = args[dashPos:]
				args = args[:dashPos]
			}
			dbg.Debug("Args:", strings.Join(args, " "))
			dbg.Debug("GitFlags:", strings.Join(opts.gitFlags, " "))
			if nArgs := len(args); nArgs > 0 {
				if opts.groupName != "" {
					opts.dir = args[0]
				} else {
					ctxOpts.Repo = args[0]
					if nArgs > 1 {
						opts.dir = args[1]
					}
				}
			}
			dbg.Debug("Dir:", opts.dir)

			if ctxOpts.Repo == "" && opts.groupName == "" {
				return &cmdutils.FlagError{Err: fmt.Errorf("Specify repository argument, or use the --group flag to specify a group to clone all repos from the group.")}
			}

			opts.archivedSet = cmd.Flags().Changed("archived")
			opts.activeSet = cmd.Flags().Changed("active")

			if runE != nil {
				return runE(opts, ctxOpts)
			}

			opts.host = f.DefaultHostname()

			apiClient, err := f.ApiClient(opts.host)
			if err != nil {
				return err
			}
			opts.apiClient = apiClient

			opts.currentUser, _, err = opts.apiClient.Lab().Users.CurrentUser()
			if err != nil {
				return err
			}

			cfg := f.Config()
			opts.protocol, _ = cfg.Get(opts.host, "git_protocol")

			if opts.groupName != "" {
				return groupClone(opts, ctxOpts)
			}

			return cloneRun(opts, ctxOpts)
		},
	}

	repoCloneCmd.Flags().StringVarP(&opts.groupName, "group", "g", "", "Specify the group to clone repositories from.")
	repoCloneCmd.Flags().BoolVarP(&opts.preserveNamespace, "preserve-namespace", "p", false, "Clone the repository in a subdirectory based on namespace. (default false)")
	repoCloneCmd.Flags().BoolVarP(&opts.active, "active", "", false, "Limit by project status. When true, returns active projects. When false, returns projects that are archived or marked for deletion. Used with the --group flag.")
	repoCloneCmd.Flags().BoolVarP(&opts.archived, "archived", "a", false, "Limit by archived status. Use with '-a=false' to exclude archived repositories. Used with the --group flag. (default false)")
	repoCloneCmd.Flags().BoolVarP(&opts.includeSubgroups, "include-subgroups", "G", true, "Include projects in subgroups of this group. Default is true. Used with the --group flag.")
	repoCloneCmd.Flags().BoolVarP(&opts.owned, "mine", "m", false, "Limit by projects in the group owned by the current authenticated user. Used with the --group flag. (default false)")
	repoCloneCmd.Flags().StringVarP(&opts.visibility, "visibility", "v", "", "Limit by visibility: public, internal, private. Used with the --group flag.")
	repoCloneCmd.Flags().BoolVarP(&opts.withIssuesEnabled, "with-issues-enabled", "I", false, "Limit by projects with the issues feature enabled. Default is false. Used with the --group flag.")
	repoCloneCmd.Flags().BoolVarP(&opts.withMREnabled, "with-mr-enabled", "M", false, "Limit by projects with the merge request feature enabled. Default is false. Used with the --group flag.")
	repoCloneCmd.Flags().BoolVarP(&opts.withShared, "with-shared", "S", true, "Include projects shared to this group. Default is true. Used with the --group flag.")
	repoCloneCmd.Flags().BoolVarP(&opts.paginate, "paginate", "", false, "Make additional HTTP requests to fetch all pages of projects before cloning. Respects --per-page. (default false)")
	repoCloneCmd.Flags().IntVarP(&opts.page, "page", "", 1, "Page number.")
	repoCloneCmd.Flags().IntVarP(&opts.perPage, "per-page", "", 30, "Number of items to list per page.")

	repoCloneCmd.Flags().SortFlags = false
	repoCloneCmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		if errors.Is(err, pflag.ErrHelp) {
			return err
		}
		return &cmdutils.FlagError{Err: fmt.Errorf("%w\nSeparate Git clone flags with '--'.", err)}
	})

	return repoCloneCmd
}

func groupClone(opts *options, ctxOpts *ContextOpts) error {
	c := opts.io.Color()
	listOpts := &gitlab.ListGroupProjectsOptions{}
	if !opts.withShared {
		listOpts.WithShared = new(false)
	}
	if opts.withMREnabled {
		listOpts.WithMergeRequestsEnabled = new(true)
	}
	if opts.withIssuesEnabled {
		listOpts.WithIssuesEnabled = new(true)
	}
	if opts.owned {
		listOpts.Owned = new(true)
	}
	if opts.activeSet {
		listOpts.Active = new(opts.active)
	}
	if opts.archivedSet {
		listOpts.Archived = new(opts.archived)
	}
	if opts.includeSubgroups {
		includeSubGroups := true
		listOpts.IncludeSubGroups = &includeSubGroups
	}
	if opts.visibility != "" {
		listOpts.Visibility = new(gitlab.VisibilityValue(opts.visibility))
	}

	listOpts.PerPage = 100
	if opts.paginate {
		listOpts.PerPage = 30
	}
	if opts.perPage != 0 {
		listOpts.PerPage = int64(opts.perPage)
	}
	if opts.page != 0 {
		listOpts.Page = int64(opts.page)
	}

	var projects []*gitlab.Project
	var err error
	if opts.paginate {
		projects, err = gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.Project, *gitlab.Response, error) {
			return opts.apiClient.Lab().Groups.ListGroupProjects(opts.groupName, listOpts, p)
		})
	} else {
		projects, _, err = opts.apiClient.Lab().Groups.ListGroupProjects(opts.groupName, listOpts)
	}
	if err != nil {
		return err
	}
	if len(projects) == 0 {
		fmt.Fprintf(opts.io.StdErr, "Group %q does not have any projects.\n", opts.groupName)
		return cmdutils.SilentError
	}

	var finalOutput []string
	for _, project := range projects {
		ctxOpt := *ctxOpts
		ctxOpt.Project = project
		ctxOpt.Repo = project.PathWithNamespace
		err = cloneRun(opts, &ctxOpt)
		if err != nil {
			finalOutput = append(finalOutput, fmt.Sprintf("%s %s - Error: %q", c.RedCheck(), project.PathWithNamespace, err.Error()))
		} else {
			finalOutput = append(finalOutput, fmt.Sprintf("%s %s", c.GreenCheck(), project.PathWithNamespace))
		}
	}

	// Print error/success msgs in human-readable formats
	for _, out := range finalOutput {
		fmt.Fprintln(opts.io.StdOut, out)
	}
	if err != nil { // if any error came up
		return cmdutils.SilentError
	}
	return nil
}

func cloneRun(opts *options, ctxOpts *ContextOpts) error {
	localDir := opts.dir
	if !git.IsValidURL(ctxOpts.Repo) {
		// Assuming that repo is a project ID if it is an integer
		if _, err := strconv.ParseInt(ctxOpts.Repo, 10, 64); err != nil {
			// Assuming that "/" in the project name means its owned by an organisation
			if !strings.Contains(ctxOpts.Repo, "/") {
				ctxOpts.Repo = fmt.Sprintf("%s/%s", opts.currentUser.Username, ctxOpts.Repo)
			}
		}
		if ctxOpts.Project == nil {
			p, err := api.GetProject(opts.apiClient.Lab(), ctxOpts.Repo)
			if err != nil {
				return err
			}
			ctxOpts.Project = p
		}
		ctxOpts.Repo = glrepo.RemoteURL(ctxOpts.Project, opts.protocol)
		if opts.preserveNamespace {
			localDir = filepath.Join(opts.dir, filepath.FromSlash(ctxOpts.Project.PathWithNamespace))
		} else if opts.dir != "" && opts.groupName != "" {
			localDir = filepath.Join(opts.dir, ctxOpts.Project.Path)
		}
	} else if !strings.HasSuffix(ctxOpts.Repo, ".git") {
		ctxOpts.Repo += ".git"
	}
	_, err := git.RunClone(ctxOpts.Repo, localDir, opts.gitFlags)
	if err != nil {
		return err
	}
	// Cloned project was a fork belonging to the user; user is
	// treating fork's ssh/https url as origin. Add upstream as remote pointing
	// to forked repo's ssh/https url depending on the users preferred protocol
	if ctxOpts.Project != nil {
		if ctxOpts.Project.ForkedFromProject != nil && strings.Contains(ctxOpts.Project.PathWithNamespace, opts.currentUser.Username) {
			if localDir == "" {
				localDir = "./" + ctxOpts.Project.Path
			}
			fProject, err := api.GetProject(opts.apiClient.Lab(), ctxOpts.Project.ForkedFromProject.PathWithNamespace)
			if err != nil {
				return err
			}
			repoURL := glrepo.RemoteURL(fProject, opts.protocol)
			err = git.AddUpstreamRemote(repoURL, localDir)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
