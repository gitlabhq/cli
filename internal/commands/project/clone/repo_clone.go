package clone

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	groupName         string
	leafGroup         bool
	leafGroupPath     string
	includeSubgroups  bool
	excludeSubgroups  []string
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
	wiki              bool

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
	repo    glrepo.Interface
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

			# Clones a project's wiki repository
			glab repo clone --wiki gitlab-org/cli

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

			# Clone the repositories in a nested group into a directory named after that group
			glab repo clone -g org/platform/backend --preserve-namespace --leaf-group --paginate

			# Clones all repos in a group, except those in one subgroup
			glab repo clone -g mygroup --exclude-subgroup domain/backend --paginate

			# Clones all non-archived repos in a group
			glab repo clone -g everyonecancontribute --archived=false --paginate

			# Clones only active projects in a group
			glab repo clone -g everyonecancontribute --active=true --paginate

			# Clones from a configured GitLab Self-Managed or GitLab Dedicated instance
			glab repo clone salsa.debian.org/mygroup/myproject

			# Clones from an unconfigured instance using a full URL
			glab repo clone https://salsa.debian.org/mygroup/myproject`),
		Long: heredoc.Docf(`
		Clone a GitLab repository to your local machine. Specify the
		repository by name, namespace/repo path, full URL, or project ID.

		The command uses your configured protocol (SSH or HTTPS).

		Prefix a namespace path with a hostname to use the default host or another host in your %[1]sglab%[1]s configuration. Because dots are valid in GitLab group names, an unconfigured hostname-like prefix is treated as part of the namespace; use a full URL to clone from an unconfigured host.

		If a group name matches a configured hostname, the hostname takes precedence. Specify both the host and the same-named group, or use a full URL to clone from that group.

		To pass Git clone flags, add them after %[1]s--%[1]s. For example:
		%[1]sglab repo clone <repo> -- --branch <branch-name>%[1]s

		When you clone a fork you own, the command adds an %[1]supstream%[1]s
		remote that points to the parent project.

		Use %[1]s--wiki%[1]s to clone the wiki repository associated with a project.
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

			if opts.leafGroup && (opts.groupName == "" || !opts.preserveNamespace) {
				return &cmdutils.FlagError{Err: errors.New("--leaf-group requires --group and --preserve-namespace")}
			}
			if ctxOpts.Repo == "" && opts.groupName == "" {
				return &cmdutils.FlagError{Err: errors.New("specify repository argument, or use the --group flag to specify a group to clone all repos from the group")}
			}

			opts.archivedSet = cmd.Flags().Changed("archived")
			opts.activeSet = cmd.Flags().Changed("active")

			if runE != nil {
				return runE(opts, ctxOpts)
			}

			cfg := f.Config()
			opts.host = f.DefaultHostname()

			// A full URL is handed to git as-is, so a plain clone needs no API
			// client or authenticated user. Only --wiki resolves the repository
			// through the API: it builds a client for the URL's host, which sends
			// a configured or GITLAB_TOKEN credential to that host, the same as
			// --wiki with -R <url>.
			switch {
			case git.IsValidURL(ctxOpts.Repo):
				if opts.wiki {
					repo, err := cloneRepo(ctxOpts.Repo, opts.host, cfg)
					if err != nil {
						return err
					}
					opts.host = repo.RepoHost()
					ctxOpts.repo = repo

					apiClient, err := f.ApiClient(opts.host)
					if err != nil {
						return err
					}
					opts.apiClient = apiClient
				}
			default:
				if ctxOpts.Repo != "" && strings.ContainsRune(ctxOpts.Repo, '/') {
					repo, host, err := glrepo.FromFullNameWithConfiguredHosts(ctxOpts.Repo, opts.host, cfg)
					if err != nil {
						return err
					}
					opts.host = host
					ctxOpts.Repo = repo.FullName()
				}

				apiClient, err := f.ApiClient(opts.host)
				if err != nil {
					return err
				}
				opts.apiClient = apiClient

				opts.currentUser, _, err = apiClient.Lab().Users.CurrentUser()
				if err != nil {
					return err
				}
			}

			opts.protocol, _ = cfg.Get(opts.host, "git_protocol")

			if opts.groupName != "" {
				return groupClone(opts, ctxOpts)
			}

			return cloneRun(opts, ctxOpts)
		},
	}

	repoCloneCmd.Flags().StringVarP(&opts.groupName, "group", "g", "", "Specify the group to clone repositories from.")
	repoCloneCmd.Flags().BoolVarP(&opts.preserveNamespace, "preserve-namespace", "p", false, "Clone the repository in a subdirectory based on namespace.")
	repoCloneCmd.Flags().BoolVar(&opts.leafGroup, "leaf-group", false, "Clone into a directory named after the selected group instead of its full namespace path. Shared projects outside the group keep their full namespace. Requires --group and --preserve-namespace.")
	repoCloneCmd.Flags().BoolVarP(&opts.active, "active", "", false, "Limit by project status. When true, returns active projects. When false, returns projects that are archived or marked for deletion. Used with the --group flag.")
	repoCloneCmd.Flags().BoolVarP(&opts.archived, "archived", "a", false, "Limit by archived status. Use with '-a=false' to exclude archived repositories. Used with the --group flag.")
	repoCloneCmd.Flags().BoolVarP(&opts.includeSubgroups, "include-subgroups", "G", true, "Include projects in subgroups of this group. Default is true. Used with the --group flag.")
	repoCloneCmd.Flags().StringSliceVar(&opts.excludeSubgroups, "exclude-subgroup", nil, "Skip projects in this subgroup, as a path relative to the group, not a numeric ID. Repeat the flag, or separate values with commas, to skip more than one. Projects shared from other namespaces are not affected. Used with the --group flag.")
	repoCloneCmd.Flags().BoolVarP(&opts.owned, "mine", "m", false, "Limit by projects in the group owned by the current authenticated user. Used with the --group flag.")
	repoCloneCmd.Flags().StringVarP(&opts.visibility, "visibility", "v", "", "Limit by visibility: public, internal, private. Used with the --group flag.")
	repoCloneCmd.Flags().BoolVarP(&opts.withIssuesEnabled, "with-issues-enabled", "I", false, "Limit by projects with the issues feature enabled. Default is false. Used with the --group flag.")
	repoCloneCmd.Flags().BoolVarP(&opts.withMREnabled, "with-mr-enabled", "M", false, "Limit by projects with the merge request feature enabled. Default is false. Used with the --group flag.")
	repoCloneCmd.Flags().BoolVarP(&opts.withShared, "with-shared", "S", true, "Include projects shared to this group. Default is true. Used with the --group flag.")
	repoCloneCmd.Flags().BoolVarP(&opts.paginate, "paginate", "", false, "Make additional HTTP requests to fetch all pages of projects before cloning. Respects --per-page.")
	repoCloneCmd.Flags().IntVarP(&opts.page, "page", "", 1, "Page number.")
	repoCloneCmd.Flags().IntVarP(&opts.perPage, "per-page", "", 30, "Number of items to list per page.")
	repoCloneCmd.Flags().BoolVarP(&opts.wiki, "wiki", "", false, "Clone the project's wiki repository.")
	repoCloneCmd.MarkFlagsMutuallyExclusive("group", "wiki")

	repoCloneCmd.Flags().SortFlags = false
	repoCloneCmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		if errors.Is(err, pflag.ErrHelp) {
			return err
		}
		return &cmdutils.FlagError{Err: fmt.Errorf("%w\nseparate Git clone flags with '--'", err)}
	})

	return repoCloneCmd
}

// cloneRepo parses a full URL argument into a repository. The result carries
// both the URL's own host (so cloning from another instance does not target the
// default one) and its project path with any configured subfolder stripped, so
// a --wiki lookup reuses that path instead of parsing the URL a second time.
func cloneRepo(repoArg, defaultHost string, cfg config.Config) (glrepo.Interface, error) {
	u, err := git.ParseURL(repoArg)
	if err != nil {
		return nil, fmt.Errorf("invalid argument: %w", err)
	}
	repo, err := glrepo.FromURL(u, defaultHost, cfg)
	if err != nil {
		return nil, fmt.Errorf("invalid argument: %w", err)
	}
	return repo, nil
}

// wikiProjectPath is the namespace/repo the wiki lookup queries. A full URL is
// parsed once in RunE, so its path already has any configured subfolder
// stripped; FullNameFromURL does not strip it, so it is only a fallback for
// callers that reach cloneRun without a pre-parsed repository.
func wikiProjectPath(ctxOpts *ContextOpts) (string, error) {
	if ctxOpts.repo != nil {
		return ctxOpts.repo.FullName(), nil
	}
	return glrepo.FullNameFromURL(ctxOpts.Repo)
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

	groupPath := opts.groupName
	_, parseErr := strconv.ParseInt(opts.groupName, 10, 64)
	isGroupID := parseErr == nil
	if opts.leafGroup || (isGroupID && len(opts.excludeSubgroups) > 0) {
		group, _, err := opts.apiClient.Lab().Groups.GetGroup(opts.groupName, nil)
		if err != nil {
			return err
		}
		groupPath = group.FullPath
		if opts.leafGroup {
			opts.leafGroupPath = groupPath
		}
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
		opts.io.LogErrorf("Group %q does not have any projects.\n", opts.groupName)
		return cmdutils.SilentError
	}

	if len(opts.excludeSubgroups) > 0 {
		projects = slices.DeleteFunc(projects, func(p *gitlab.Project) bool {
			return isExcludedSubgroup(p.PathWithNamespace, groupPath, opts.excludeSubgroups)
		})
		if len(projects) == 0 {
			opts.io.LogErrorf("No projects left to clone in group %q after applying --exclude-subgroup.\n", opts.groupName)
			return cmdutils.SilentError
		}
	}

	if opts.leafGroup {
		destinations := make(map[string]string, len(projects))
		for _, project := range projects {
			fullPath := project.PathWithNamespace
			dir, err := opts.namespacedCloneDir(fullPath)
			if err != nil {
				return err
			}
			if other, found := destinations[dir]; found {
				return fmt.Errorf("projects %q and %q both clone to %q with --leaf-group. Rerun without --leaf-group or set --with-shared=false", other, fullPath, dir)
			}
			destinations[dir] = fullPath
		}
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
		opts.io.LogInfo(out)
	}
	if err != nil { // if any error came up
		return cmdutils.SilentError
	}
	return nil
}

func validateAPIPath(p string) error {
	if p == "" || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "-") || strings.ContainsAny(p, `\:`) {
		return fmt.Errorf("invalid project path %q", p)
	}
	for seg := range strings.SplitSeq(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("invalid project path %q", p)
		}
	}
	return nil
}

func (opts *options) namespacedCloneDir(projectPath string) (string, error) {
	if err := validateAPIPath(projectPath); err != nil {
		return "", err
	}
	if rel, ok := strings.CutPrefix(projectPath, opts.leafGroupPath+"/"); ok && opts.leafGroupPath != "" {
		projectPath = path.Join(path.Base(opts.leafGroupPath), rel)
	}
	return filepath.Join(opts.dir, filepath.FromSlash(projectPath)), nil
}

func isExcludedSubgroup(pathWithNamespace, groupPath string, excludes []string) bool {
	projectPath := strings.ToLower(pathWithNamespace)
	groupPrefix := strings.ToLower(strings.Trim(groupPath, "/")) + "/"
	for _, exclude := range excludes {
		exclude = strings.ToLower(strings.Trim(exclude, "/"))
		if exclude == "" {
			continue
		}
		fullPath := exclude
		if !strings.HasPrefix(exclude, groupPrefix) {
			fullPath = groupPrefix + exclude
		}
		if projectPath == fullPath || strings.HasPrefix(projectPath, fullPath+"/") {
			return true
		}
	}
	return false
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
		repoURL, err := glrepo.RemoteURL(ctxOpts.Project, opts.protocol)
		if err != nil {
			return err
		}
		ctxOpts.Repo = repoURL
		if opts.preserveNamespace {
			localDir, err = opts.namespacedCloneDir(ctxOpts.Project.PathWithNamespace)
			if err != nil {
				return err
			}
		} else if opts.dir != "" && opts.groupName != "" {
			if err := validateAPIPath(ctxOpts.Project.Path); err != nil {
				return err
			}
			localDir = filepath.Join(opts.dir, ctxOpts.Project.Path)
		}
	} else if !strings.HasSuffix(ctxOpts.Repo, ".git") {
		ctxOpts.Repo += ".git"
	}
	if opts.wiki {
		if ctxOpts.Project == nil {
			projectPath, err := wikiProjectPath(ctxOpts)
			if err != nil {
				return err
			}
			ctxOpts.Project, err = api.GetProject(opts.apiClient.Lab(), projectPath)
			if err != nil {
				return err
			}
		}
		if ctxOpts.Project.WikiAccessLevel == gitlab.DisabledAccessControl {
			return fmt.Errorf("wiki is not enabled for %s", ctxOpts.Project.PathWithNamespace)
		}
		wikiURL, err := glrepo.WikiRemoteURL(ctxOpts.Project, opts.protocol)
		if err != nil {
			return err
		}
		ctxOpts.Repo = wikiURL
	}
	isUserFork := ctxOpts.Project != nil && ctxOpts.Project.ForkedFromProject != nil &&
		opts.currentUser != nil &&
		strings.Contains(ctxOpts.Project.PathWithNamespace, opts.currentUser.Username)
	if isUserFork && localDir == "" {
		if err := validateAPIPath(ctxOpts.Project.Path); err != nil {
			return err
		}
	}
	_, err := git.RunClone(ctxOpts.Repo, localDir, opts.gitFlags)
	if err != nil {
		return err
	}
	// Cloned project was a fork belonging to the user; user is
	// treating fork's ssh/https url as origin. Add upstream as remote pointing
	// to forked repo's ssh/https url depending on the users preferred protocol
	if ctxOpts.Project != nil {
		if isUserFork {
			if localDir == "" {
				localDir = "./" + ctxOpts.Project.Path
			}
			fProject, err := api.GetProject(opts.apiClient.Lab(), ctxOpts.Project.ForkedFromProject.PathWithNamespace)
			if err != nil {
				return err
			}
			repoURL, err := glrepo.RemoteURL(fProject, opts.protocol)
			if err != nil {
				return err
			}
			err = git.AddUpstreamRemote(repoURL, localDir)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
