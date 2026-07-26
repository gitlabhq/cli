package fork

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/run"
)

type options struct {
	clone     bool
	addRemote bool
	repo      string
	name      string
	path      string

	cloneSet     bool
	addRemoteSet bool
	isTerminal   bool

	// whether the user specified the repo to clone
	// if false current git repo will be cloned
	currentDirIsParent bool

	repoToFork      glrepo.Interface
	io              *iostreams.IOStreams
	baseRepo        func() (glrepo.Interface, error)
	remotes         func() (glrepo.Remotes, error)
	config          func() config.Config
	apiClient       func(repoHost string) (*api.Client, error)
	defaultHostname string
}

func NewCmdFork(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:                 f.IO(),
		baseRepo:           f.BaseRepo,
		remotes:            f.Remotes,
		config:             f.Config,
		apiClient:          f.ApiClient,
		defaultHostname:    f.DefaultHostname(),
		currentDirIsParent: true,
	}
	forkCmd := &cobra.Command{
		Use:   "fork [<repo>]",
		Short: "Fork a GitLab repository.",
		Long: heredoc.Docf(`
			Defaults to the current repository if no repository is specified.
			Use %[1]s--clone%[1]s and %[1]s--remote%[1]s to skip the interactive prompts.
		`, "`"),
		Example: heredoc.Doc(`
			glab repo fork
			glab repo fork namespace/repo
			glab repo fork namespace/repo --clone`),
		Args: cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(cmd, args)

			return opts.run(cmd.Context())
		},
	}

	forkCmd.Flags().
		StringVarP(&opts.name, "name", "n", "", "The name assigned to the new project after forking.")
	forkCmd.Flags().
		StringVarP(&opts.path, "path", "p", "", "The path assigned to the new project after forking.")
	forkCmd.Flags().
		BoolVarP(&opts.clone, "clone", "c", false, "Clone the fork. Options: true, false.")
	forkCmd.Flags().
		BoolVar(&opts.addRemote, "remote", false, "Add a remote for the fork. Options: true, false.")

	return forkCmd
}

func (o *options) complete(cmd *cobra.Command, args []string) {
	if len(args) > 0 {
		o.repo = args[0]
		o.currentDirIsParent = false
	}

	o.cloneSet = cmd.Flags().Changed("clone")
	o.addRemoteSet = cmd.Flags().Changed("remote")
	o.isTerminal = o.io.IsaTTY && o.io.IsErrTTY && o.io.IsInTTY
}

func (o *options) run(ctx context.Context) error {
	var err error

	c := o.io.Color()
	if o.repo != "" {
		if git.IsValidURL(o.repo) {
			u, err := url.Parse(o.repo)
			if err != nil {
				return fmt.Errorf("invalid argument: %w", err)
			}
			o.repoToFork, err = glrepo.FromURL(u, o.defaultHostname, o.config())
			if err != nil {
				return fmt.Errorf("invalid argument: %w", err)
			}
		} else {
			o.repoToFork, err = glrepo.FromFullName(o.repo, o.defaultHostname, o.config())
			if err != nil {
				return fmt.Errorf("argument error: %w", err)
			}
		}
	} else {
		o.repoToFork, err = o.baseRepo()
		if err != nil {
			return fmt.Errorf("unable to determine source repository: %w", err)
		}
	}

	apiClient, err := o.apiClient(o.repoToFork.RepoHost())
	if err != nil {
		return err
	}
	labClient := apiClient.Lab()

	if o.isTerminal {
		o.io.LogErrorf("- Forking %s\n", c.Bold(o.repoToFork.FullName()))
	}

	forkOpts := &gitlab.ForkProjectOptions{}
	if o.name != "" {
		forkOpts.Name = new(o.name)
	}
	if o.path != "" {
		forkOpts.Path = new(o.path)
	}

	forkedProject, _, err := labClient.Projects.ForkProject(o.repoToFork.FullName(), forkOpts)
	usingExisting := false
	if err != nil {
		if gitlab.HasStatusCode(err, http.StatusConflict) ||
			strings.Contains(err.Error(), "Project namespace name has already been taken") ||
			strings.Contains(err.Error(), "Name has already been taken") {

			o.io.LogError(c.Yellow("! Repository already exists in your namespace"))

			namespace := o.path
			currentUser, err := api.UserByName(labClient, "@me")
			if err != nil {
				return err
			}

			if namespace == "" && currentUser != nil {
				namespace = currentUser.Username
			}

			remoteDesired := o.addRemote

			if o.isTerminal {
				if !o.addRemoteSet {
					remoteDesired = true // set the default value
					err := o.io.Confirm(
						ctx,
						&remoteDesired,
						"Would you like to add this repository as a remote instead?",
					)
					if err != nil {
						return err
					}
				}

				if remoteDesired {
					// Get the existing project details
					// First search for the project name (user-namespace only)
					forkedProject, searchErr := searchProject(o, labClient)

					if searchErr != nil {
						return fmt.Errorf("error searching for existing project: %w", searchErr)
					}

					// Safety check - make sure we have a valid project
					if forkedProject == nil {
						return fmt.Errorf(
							"could not find matching project for %s",
							o.repoToFork.RepoName(),
						)
					}

					o.io.LogErrorf(
						"%s Using existing repository %s.\n",
						c.GreenCheck(),
						c.Bold(forkedProject.PathWithNamespace),
					)
					remoteName := "origin"

					protocol, err := o.config().Get(o.repoToFork.RepoHost(), "git_protocol")
					if err != nil {
						o.io.LogErrorf(
							"%s: %q. Falling back to default protocol.",
							c.Yellow("Warning"),
							err.Error(),
						)
						// Use a reasonable default if we can't get the configured protocol
						protocol = glinstance.DefaultProtocol
					}

					forkedRepoCloneURL := glrepo.RemoteURL(forkedProject, protocol)
					if err := o.addOrReplaceRemote(remoteName, "upstream", forkedRepoCloneURL); err != nil {
						return err
					}
					// Return early since we've successfully handled the existing repository case
					return nil
				} else if !o.currentDirIsParent {
					o.io.LogErrorf("- You can clone the existing repository with:")
					o.io.LogErrorf("  %s\n", c.Gray(fmt.Sprintf("glab repo clone %s/%s", namespace, o.repoToFork.RepoName())))
					return nil
				} else {
					return nil
				}
			}
		} else {
			return err
		}
	}
	// The forking operation for a project is asynchronous and is completed in a background job.
	// The request returns immediately. To determine whether the fork of the project has completed,
	// we query the import_status for the new project.
	importStatus := ""
	var importError error
	maximumRetries := 3
	retries := 0
	skipFirstCheck := true

	if !usingExisting && (forkedProject == nil || forkedProject.ImportStatus != "") {
	loop:
		for {
			// Add a defensive check to prevent nil pointer dereference when accessing forkedProject.ID
			if !skipFirstCheck {
				// Safety check - make sure forkedProject is not nil before accessing its ID field
				if forkedProject == nil {
					o.io.LogErrorf("Error: Lost track of forked project during status check")
					break loop
				}

				// Now it's safe to access forkedProject.ID
				forkedProject, err = api.GetProject(labClient, forkedProject.ID)
				if err != nil {
					o.io.LogErrorf("error checking fork status: %q", err.Error())
					if retries == maximumRetries {
						break loop
					}
					o.io.LogError("- Retrying...")
					retries++
					continue
				}
			}
			skipFirstCheck = false

			// check import status of Fork
			// Import status should be one of {none, failed, scheduled, started, finished}
			// https://docs.gitlab.com/ee/api/project_import_export.html#import-status
			if forkedProject == nil {
				o.io.LogErrorf("Error: Lost track of forked project during status check")
				break loop
			}

			// Now it's safe to access forkedProject.ImportStatus
			switch forkedProject.ImportStatus {
			case "none": // no import initiated
				break loop
			case importStatus:
				continue
			case "scheduled", "started": // import scheduled or started
				if importStatus != forkedProject.ImportStatus { // avoid printing the same message again
					o.io.LogError("- " + forkedProject.ImportStatus)
					importStatus = forkedProject.ImportStatus
				}
			case "finished": // import completed
				o.io.LogError("- " + forkedProject.ImportStatus)
				break loop
			case "failed": // import failed
				importError = errors.New(forkedProject.ImportError) // return the import error
				break loop
			default:
				break loop
			}
		}
	}

	if importError != nil {
		o.io.LogErrorf("%s: %q", c.Red("Fork failed"), importError.Error())
		return nil
	}

	// Only print one message about the fork creation
	if forkedProject != nil {
		o.io.LogErrorf(
			"%s Created fork %s.\n",
			c.GreenCheck(),
			forkedProject.PathWithNamespace,
		)
	} else {
		o.io.LogErrorf("\n%s Created fork but couldn't retrieve details.\n", c.GreenCheck())
		// Early return since we can't proceed with a nil forkedProject
		return nil
	}

	if (!o.isTerminal && o.currentDirIsParent && (!o.addRemote && o.addRemoteSet)) ||
		(!o.currentDirIsParent && (!o.clone && o.addRemoteSet)) {
		return nil
	}

	cfg := o.config()
	protocol, err := cfg.Get(o.repoToFork.RepoHost(), "git_protocol")
	if err != nil {
		return err
	}

	if o.currentDirIsParent {
		// Safety check for Remotes method
		if o.remotes == nil {
			o.io.LogErrorf("%s: Unable to access git remotes", c.Red("Error"))
			return fmt.Errorf("remotes method is nil")
		}

		remotes, err := o.remotes()
		if err != nil {
			return err
		}

		if remote, err := remotes.FindByRepo(o.repoToFork.RepoOwner(), o.repoToFork.RepoName()); err == nil {
			scheme := ""
			if remote.FetchURL != nil {
				scheme = remote.FetchURL.Scheme
			}
			if remote.PushURL != nil {
				scheme = remote.PushURL.Scheme
			}
			if scheme != "" {
				protocol = scheme
			}
		}

		// Defensive check for forkedProject's Namespace
		if forkedProject.Namespace != nil {
			if remote, err := remotes.FindByRepo(forkedProject.Namespace.FullPath, forkedProject.Path); err == nil {
				if o.isTerminal {
					o.io.LogErrorf(
						"%s Using existing remote %s.\n",
						c.GreenCheck(),
						c.Bold(remote.Name),
					)
				}
				return nil
			}
		}

		remoteDesired := o.addRemote
		if !o.addRemoteSet {
			remoteDesired = true // set the default value
			err = o.io.Confirm(
				ctx,
				&remoteDesired,
				"Would you like to add a remote for the fork?",
			)
			if err != nil {
				return fmt.Errorf("failed to prompt: %w", err)
			}
		}
		if remoteDesired {
			remoteName := "origin"
			forkedRepoCloneURL := glrepo.RemoteURL(forkedProject, protocol)
			if err := o.addOrReplaceRemote(remoteName, "upstream", forkedRepoCloneURL); err != nil {
				return err
			}
		}
	} else {
		cloneDesired := o.clone
		if !cloneDesired {
			// If clone is explicitly set to false exit
			if o.cloneSet {
				return nil
			}
			cloneDesired = true // set the default value
			err = o.io.Confirm(ctx, &cloneDesired, "Would you like to clone the fork?")
			if err != nil {
				return fmt.Errorf("failed to prompt: %w", err)
			}
		}
		if cloneDesired {
			repoToFork, err := api.GetProject(labClient, o.repoToFork.FullName())
			if err != nil {
				return err
			}
			forkedRepoURL := glrepo.RemoteURL(forkedProject, protocol)
			cloneDir, err := git.RunClone(forkedRepoURL, "", []string{})
			if err != nil {
				return fmt.Errorf("failed to clone fork: %w", err)
			}
			upstreamURL := glrepo.RemoteURL(repoToFork, protocol)
			err = git.AddUpstreamRemote(upstreamURL, cloneDir)
			if err != nil {
				return err
			}

			if o.isTerminal {
				o.io.LogErrorf("%s Cloned fork.\n", c.GreenCheck())
			}
		}
	}
	return nil
}

func searchProject(o *options, client *gitlab.Client) (*gitlab.Project, error) {
	projects, _, err := client.Projects.ListProjects(&gitlab.ListProjectsOptions{
		Search: new(o.repoToFork.RepoName()),
	})
	if err != nil {
		o.io.LogErrorf("ERROR: Cannot list projects: %v\n", err)
		return nil, err
	}

	currentUser, err := api.UserByName(client, "@me")
	if err != nil {
		o.io.LogErrorf("ERROR: Cannot get current user: %v\n", err)
		return nil, err
	}

	// Attempt to find the project in the current user's namespace (which must match the username)
	for _, project := range projects {
		if project != nil && project.Namespace != nil && currentUser != nil {
			if project.Namespace.Path == currentUser.Username {
				return project, nil
			}
		}
	}

	// No matching project found; output error about unsupported namespaces
	c := o.io.Color()
	o.io.LogError(
		c.Red(
			"Error: Only user namespaces that are equal to your username are currently supported.",
		),
	)
	return nil, fmt.Errorf("no project found for user namespace %s", currentUser.Username)
}

// addOrReplaceRemote handles adding a new remote, renaming existing remotes if needed.
// If a remote with remoteName already exists, it will be renamed to fallbackName.
func (o *options) addOrReplaceRemote(remoteName, fallbackName, remoteURL string) error {
	// Safety check for Remotes method
	if o.remotes == nil {
		return fmt.Errorf("remotes method is nil")
	}

	remotes, err := o.remotes()
	if err != nil {
		return err
	}

	c := o.io.Color()

	// If remote exists, rename it to fallback name
	if _, err := remotes.FindByName(remoteName); err == nil {
		renameCmd := git.GitCommand("remote", "rename", remoteName, fallbackName)
		err = run.PrepareCmd(renameCmd).Run()
		if err != nil {
			return err
		}
		if o.isTerminal {
			o.io.LogErrorf("%s Renamed %s remote to %s\n",
				c.GreenCheck(),
				c.Bold(remoteName),
				c.Bold(fallbackName))
		}
	}

	// Add the new remote
	_, err = git.AddRemote(remoteName, remoteURL)
	if err != nil {
		return fmt.Errorf("failed to add remote: %w", err)
	}

	if o.isTerminal {
		o.io.LogErrorf("%s Added remote %s.\n",
			c.GreenCheck(),
			c.Bold(remoteName))
	}

	return nil
}
