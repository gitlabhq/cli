package create

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/run"
)

var currentUser = func(client *gitlab.Client) (*gitlab.User, error) {
	u, _, err := client.Users.CurrentUser()
	if err != nil {
		return nil, err
	}
	return u, nil
}

var createProject = func(client *gitlab.Client, opts *gitlab.CreateProjectOptions) (*gitlab.Project, error) {
	project, _, err := client.Projects.CreateProject(opts)
	if err != nil {
		return nil, err
	}
	return project, nil
}

var addRemote = func(name, url string) (*git.Remote, error) {
	return git.AddRemote(name, url)
}

var gitInitializer = func() error {
	return initGit()
}

var repoInitializer = func(projectPath, remoteURL string) error {
	return initializeRepo(projectPath, remoteURL)
}

var repoCloner = func(cloneURL, target, remoteName string) error {
	_, err := git.RunClone(cloneURL, target, []string{"--origin", remoteName})
	return err
}

func NewCmdCreate(f cmdutils.Factory) *cobra.Command {
	projectCreateCmd := &cobra.Command{
		Use:   "create [path] [flags]",
		Short: `Create a new GitLab project/repository.`,
		Long: heredoc.Docf(`
	Creates the new project with your first configured host in your %[1]sglab%[1]s
	configuration. The host defaults to %[1]sGitLab.com%[1]s if not set. To set a host,
	provide either:

	- A %[1]sGITLAB_HOST%[1]s environment variable.
	- A full URL for the project.
	`, "`"),
		Args: cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCreateProject(cmd, args, f)
		},
		Example: heredoc.Doc(`
			# Create a repository under your account using the current directory name.
			glab repo create

			# Create a repository under a group using the current directory name.
			glab repo create --group glab-cli

			# Create a repository with a specific name.
			glab repo create my-project

			# Create a repository for a group.
			glab repo create glab-cli/my-project

			# Create on a host other than gitlab.com.
			GITLAB_HOST=example.com glab repo create
			glab repo create <host>/path/to/repository`),
	}

	projectCreateCmd.Flags().StringP("name", "n", "", "Name of the new project.")
	projectCreateCmd.Flags().StringP("group", "g", "", "Namespace or group for the new project. Defaults to the current user's namespace.")
	projectCreateCmd.Flags().StringP("description", "d", "", "Description of the new project. Set to \"-\" to open an editor.")
	projectCreateCmd.Flags().String("defaultBranch", "", "Branch name for the new project, overriding both the GitLab instance default and your local git configuration.")
	projectCreateCmd.Flags().String("remoteName", "origin", "Remote name for the Git repository you're in. Defaults to `origin` if not provided.")
	projectCreateCmd.Flags().StringArrayP("tag", "t", []string{}, "The list of tags for the project.")
	projectCreateCmd.Flags().Bool("internal", false, "Make project internal: visible to any authenticated user. Default.")
	projectCreateCmd.Flags().BoolP("private", "p", false, "Make project private: visible only to project members.")
	projectCreateCmd.Flags().BoolP("public", "P", false, "Make project public: visible without any authentication.")
	projectCreateCmd.Flags().Bool("readme", false, "Initialize project with `README.md`. The repository is cloned locally after creation to ensure the local branch matches the remote.")
	projectCreateCmd.Flags().BoolP("skipGitInit", "s", false, "Skip local repository setup (skips both 'git init' and cloning).")

	return projectCreateCmd
}

func runCreateProject(cmd *cobra.Command, args []string, f cmdutils.Factory) error {
	var (
		projectPath string
		visibility  gitlab.VisibilityValue
		err         error
		isPath      bool
		namespaceID int
		namespace   string
	)
	c := f.IO().Color()

	defaultBranch, err := cmd.Flags().GetString("defaultBranch")
	if err != nil {
		return err
	}
	remoteName, err := cmd.Flags().GetString("remoteName")
	if err != nil {
		return err
	}
	skipGitInit, _ := cmd.Flags().GetBool("skipGitInit")
	readme, _ := cmd.Flags().GetBool("readme")

	// Check if directory is already git initialized
	gitDir := path.Join(config.GitDir(false)...)
	stat, statErr := os.Stat(gitDir)
	isGitInitialized := statErr == nil && stat.Mode().IsDir()

	// Determine if we need to set up a local repository in the current directory
	var needsGitInit bool
	if !skipGitInit && !isGitInitialized {
		// Default behavior depends on whether we can prompt
		if f.IO().IsInteractive() {
			needsGitInit = true
			promptMsg := "Directory not Git initialized. Run `git init`?"
			if readme {
				promptMsg = "Directory not Git initialized. Create project on GitLab and clone to the current directory?"
			}
			err := f.IO().Confirm(cmd.Context(), &needsGitInit, promptMsg)
			if err != nil {
				return err
			}
		} else {
			// In non-interactive mode, default to false for safety
			needsGitInit = false
		}
	}

	var gitlabClient *gitlab.Client
	if len(args) == 1 {
		var host string
		host, namespace, projectPath = projectPathFromArgs(args, f.DefaultHostname(), f.Config())
		client, err := f.ApiClient(host)
		if err != nil {
			return err
		}
		gitlabClient = client.Lab()

		user, err := currentUser(gitlabClient)
		if err != nil {
			return err
		}
		if user.Username == namespace {
			namespace = ""
		}
		// When a project name is provided as argument, we won't init git in current directory
		// Instead, we'll create a subdirectory and init there (or just add remote if already in git repo)
		needsGitInit = false
	} else {
		// If we're in a git repository, use the repo name
		// Otherwise, use the current directory name
		if isGitInitialized {
			projectPath, err = git.ToplevelDir()
			if err != nil {
				return err
			}
			projectPath = path.Base(projectPath)
		} else {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("cannot get current directory: %w", err)
			}
			projectPath = path.Base(cwd)
		}
		isPath = true

		c, err := f.ApiClient(f.DefaultHostname())
		if err != nil {
			return err
		}
		gitlabClient = c.Lab()
	}

	group, err := cmd.Flags().GetString("group")
	if err != nil {
		return fmt.Errorf("could not parse group flag: %w", err)
	}
	if group != "" {
		namespace = group
	}

	if namespace != "" {
		group, _, err := gitlabClient.Groups.GetGroup(namespace, &gitlab.GetGroupOptions{})
		if err != nil {
			return fmt.Errorf("could not find group or namespace %s: %w", namespace, err)
		}
		namespaceID = int(group.ID)
	}

	name, _ := cmd.Flags().GetString("name")

	if projectPath == "" && name == "" {
		fmt.Println("ERROR: path or name required to create a project.")
		return cmd.Usage()
	} else if name == "" {
		name = projectPath
	}

	description, _ := cmd.Flags().GetString("description")

	// Handle -d- flag: open editor without template selection
	err = cmdutils.HandleDescriptionEditor(cmd.Context(), &description, f.IO(), f.Config, nil)
	if err != nil {
		return err
	}

	if internal, _ := cmd.Flags().GetBool("internal"); internal {
		visibility = gitlab.InternalVisibility
	} else if private, _ := cmd.Flags().GetBool("private"); private {
		visibility = gitlab.PrivateVisibility
	} else if public, _ := cmd.Flags().GetBool("public"); public {
		visibility = gitlab.PublicVisibility
	}

	tags, _ := cmd.Flags().GetStringArray("tag")

	opts := &gitlab.CreateProjectOptions{
		Name:                 new(name),
		Path:                 new(projectPath),
		Description:          new(description),
		DefaultBranch:        new(defaultBranch),
		TagList:              &tags,
		InitializeWithReadme: new(readme),
	}

	if visibility != "" {
		opts.Visibility = new(visibility)
	}

	if namespaceID != 0 {
		opts.NamespaceID = new(int64(namespaceID))
	}

	project, err := createProject(gitlabClient, opts)
	if err != nil {
		return fmt.Errorf("error creating project: %w", err)
	}

	greenCheck := c.Green("✓")
	f.IO().LogInfof("%s Created project on GitLab: %s - %s\n", greenCheck, project.NameWithNamespace, project.WebURL)

	cfg := f.Config()
	webURL, err := url.Parse(project.WebURL)
	if err != nil {
		return fmt.Errorf("project created but GitLab returned an invalid web URL %q: %w", project.WebURL, err)
	}
	protocol, _ := cfg.Get(webURL.Host, "git_protocol")
	remote := glrepo.RemoteURL(project, protocol)

	if isPath {
		if needsGitInit {
			if readme {
				// GitLab initialized the repo server-side; clone so the local branch matches the remote.
				if err := repoCloner(remote, ".", remoteName); err != nil {
					f.IO().LogErrorf("Warning: Project created on GitLab but clone failed: %v\n", err)
					f.IO().LogErrorf("You can manually clone with: git clone %s .\n", remote)
				} else {
					f.IO().LogInfof("%s Cloned repository\n", greenCheck)
				}
			} else {
				if err := gitInitializer(); err != nil {
					f.IO().LogErrorf("Warning: Project created on GitLab but git init failed: %v\n", err)
					f.IO().LogErrorf("You can manually initialize the repository with: git init\n")
				} else {
					f.IO().LogInfof("%s Initialized git repository\n", greenCheck)
					if _, err := addRemote(remoteName, remote); err != nil {
						f.IO().LogErrorf("Warning: Could not add remote: %v\n", err)
					} else {
						f.IO().LogInfof("%s Added remote %s\n", greenCheck, remote)
					}
					if defaultBranch != "" {
						gitBranch := git.GitCommand("checkout", "-b", defaultBranch)
						gitBranch.Stdout = os.Stdout
						gitBranch.Stdin = os.Stdin
						if err := run.PrepareCmd(gitBranch).Run(); err != nil {
							f.IO().LogErrorf("Warning: Failed to create branch %s: %v\n", defaultBranch, err)
						}
					}
				}
			}
		} else if isGitInitialized {
			// Only when the directory really is a repository. Reached both with
			// --skipGitInit and when the user declined `git init`, and in neither
			// case is there anywhere for a remote to go: `git remote add` would
			// print `fatal: not a git repository` while the command still exits 0.
			if _, err := addRemote(remoteName, remote); err != nil {
				f.IO().LogErrorf("Warning: Could not add remote: %v\n", err)
			} else {
				f.IO().LogInfof("%s Added remote %s\n", greenCheck, remote)
			}
		}

		return nil
	}

	// When a project name is provided (not working in current directory)
	// we need to set up a local subdirectory for it.
	// In non-interactive mode, defaults to false (only creates remote project)
	// to prevent unexpected directory creation for AI agents.
	var doSetup bool
	if !skipGitInit && f.IO().IsInteractive() {
		doSetup = true
		if err := f.IO().Confirm(cmd.Context(), &doSetup, fmt.Sprintf("Create a local project directory for %s?", project.NameWithNamespace)); err != nil {
			return err
		}
	}

	if doSetup {
		projectPath := project.Path
		if readme {
			if err := repoCloner(remote, projectPath, remoteName); err != nil {
				f.IO().LogErrorf("Warning: Project created on GitLab but clone failed: %v\n", err)
				f.IO().LogErrorf("You can manually clone with: git clone %s %s\n", remote, projectPath)
			} else {
				f.IO().LogInfof("%s Initialized repository in './%s/'\n", greenCheck, projectPath)
			}
		} else {
			if err := repoInitializer(projectPath, remote); err != nil {
				return err
			}
			f.IO().LogInfof("%s Initialized repository in './%s/'\n", greenCheck, projectPath)
		}
	}

	return nil
}

func initGit() error {
	gitDir := path.Join(config.GitDir(false)...)
	if stat, err := os.Stat(gitDir); err == nil && stat.Mode().IsDir() {
		return nil
	}

	gitInit := git.GitCommand("init")
	gitInit.Stdout = os.Stdout
	gitInit.Stderr = os.Stderr
	return run.PrepareCmd(gitInit).Run()
}

func initializeRepo(projectPath, remoteURL string) error {
	gitInit := git.GitCommand("init", projectPath)
	gitInit.Stdout = os.Stdout
	gitInit.Stderr = os.Stderr
	err := run.PrepareCmd(gitInit).Run()
	if err != nil {
		return err
	}
	gitRemoteAdd := git.GitCommand("-C", projectPath, "remote", "add", "origin", remoteURL)
	gitRemoteAdd.Stdout = os.Stdout
	gitRemoteAdd.Stderr = os.Stderr
	err = run.PrepareCmd(gitRemoteAdd).Run()
	if err != nil {
		return err
	}
	return nil
}

func projectPathFromArgs(args []string, defaultHostname string, cfg config.Config) (string, string, string) {
	// sanitize input by removing trailing "/"
	project := strings.TrimSuffix(args[0], "/")

	var host, namespace string
	if strings.Contains(project, "/") {
		pp, _ := glrepo.FromFullName(project, defaultHostname, cfg)
		host = pp.RepoHost()
		project = pp.RepoName()
		namespace = pp.RepoOwner()
	}
	return host, namespace, project
}
