package view

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

type options struct {
	projectID    string
	client       *gitlab.Client
	web          bool
	outputFormat string
	branch       string
	browser      string
	glamourStyle string

	io              *iostreams.IOStreams
	repo            glrepo.Interface
	config          config.Config
	apiClient       func(repoHost string) (*api.Client, error)
	gitlabClient    func() (*gitlab.Client, error)
	baseRepo        func() (glrepo.Interface, error)
	branchFactory   func() (string, error)
	defaultHostname string
}

func NewCmdView(f cmdutils.Factory) *cobra.Command {
	opts := options{
		io:              f.IO(),
		config:          f.Config(),
		baseRepo:        f.BaseRepo,
		branchFactory:   f.Branch,
		apiClient:       f.ApiClient,
		gitlabClient:    f.GitLabClient,
		defaultHostname: f.DefaultHostname(),
	}

	projectViewCmd := &cobra.Command{
		Use:   "view [repository] [flags]",
		Short: "View a project or repository.",
		Long: heredoc.Doc(`Display the description and README of a project, or open it in the browser.
		`),
		Args: cobra.MaximumNArgs(1),
		Example: heredoc.Doc(`
			# View project information for the current directory.
			# Must be a Git repository.
			$ glab repo view

			# View project information of specified name.
			# glab repo view my-project
			$ glab repo view user/repo
			$ glab repo view group/namespace/repo

			# Specify repository by full [Git] URL.
			$ glab repo view git@gitlab.com:user/repo.git
			$ glab repo view https://gitlab.company.org/user/repo
			$ glab repo view https://gitlab.company.org/user/repo.git
		`),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(args); err != nil {
				return err
			}

			return opts.run()
		},
	}

	projectViewCmd.Flags().BoolVarP(&opts.web, "web", "w", false, "Open a project in the browser.")
	cmdutils.EnableJSONOutput(projectViewCmd, &opts.outputFormat)
	projectViewCmd.Flags().StringVarP(&opts.branch, "branch", "b", "", "View a specific branch of the repository.")

	return projectViewCmd
}

func (o *options) complete(args []string) error {
	o.branch = strings.TrimSpace(o.branch)

	if len(args) == 1 {
		o.projectID = args[0]
	}

	// No project argument - use current repository
	if o.projectID == "" {
		baseRepo, err := o.baseRepo()
		if err != nil {
			return cmdutils.WrapError(err, "`repository` is required when not running in a Git repository.")
		}
		o.repo = baseRepo

		// Configure client to have host of current repository
		client, err := o.apiClient(o.repo.RepoHost())
		if err != nil {
			return err
		}
		o.client = client.Lab()

		if o.branch == "" {
			o.branch, _ = o.branchFactory()
		}
	} else {
		// If the ProjectID is a single token, use current user's namespace
		if !strings.Contains(o.projectID, "/") {
			apiClient, err := o.gitlabClient()
			if err != nil {
				return err
			}
			currentUser, _, err := apiClient.Users.CurrentUser()
			if err != nil {
				return cmdutils.WrapError(err, "Failed to retrieve your current user.")
			}

			o.projectID = currentUser.Username + "/" + o.projectID
		}

		// Get the repo full name from the ProjectID which can be a full URL or a group/repo format
		repo, err := glrepo.FromFullName(o.projectID, o.defaultHostname)
		if err != nil {
			return err
		}
		o.repo = repo
		client, err := o.apiClient(o.repo.RepoHost())
		if err != nil {
			return err
		}
		o.client = client.Lab()
	}

	browser, _ := o.config.Get(o.repo.RepoHost(), "browser")
	o.browser = browser

	o.glamourStyle, _ = o.config.Get(o.repo.RepoHost(), "glamour_style")
	return nil
}

func (o *options) run() error {
	project, err := o.repo.Project(o.client)
	if err != nil {
		return cmdutils.WrapError(err, "Failed to retrieve project information.")
	}

	if o.web {
		projectURL := project.WebURL

		if o.io.IsaTTY {
			fmt.Fprintf(
				o.io.StdOut,
				"Opening %s in your browser.\n",
				generateProjectOpenURL(utils.DisplayURL(projectURL), project.DefaultBranch, o.branch),
			)
		}

		return utils.OpenInBrowser(
			generateProjectOpenURL(projectURL, project.DefaultBranch, o.branch),
			o.browser,
		)
	} else if o.outputFormat == "json" {
		printProjectContentJSON(o, project)
	} else {
		readmeFile, err := getReadmeFile(o, project)
		if err != nil {
			return err
		}

		if o.io.IsaTTY {
			if err := o.io.StartPager(); err != nil {
				return err
			}
			defer o.io.StopPager()

			printProjectContentTTY(o, project, readmeFile)
		} else {
			printProjectContentRaw(o, project, readmeFile)
		}
	}

	return nil
}

func getReadmeFile(opts *options, project *gitlab.Project) (*gitlab.File, error) {
	if project.ReadmeURL == "" {
		return nil, nil
	}

	readmePath := strings.Replace(project.ReadmeURL, project.WebURL+"/-/blob/", "", 1)
	readmePathComponents := strings.Split(readmePath, "/")
	readmeRef := readmePathComponents[0]
	readmeFileName := readmePathComponents[1]

	if opts.branch == "" {
		opts.branch = readmeRef
	}

	readmeFile, _, err := opts.client.RepositoryFiles.GetFile(project.PathWithNamespace, readmeFileName, &gitlab.GetFileOptions{Ref: gitlab.Ptr(opts.branch)})
	if err != nil {
		return nil, cmdutils.WrapError(err, fmt.Sprintf("Failed to retrieve README file on the %s branch.", opts.branch))
	}

	decoded, err := base64.StdEncoding.DecodeString(readmeFile.Content)
	if err != nil {
		return nil, cmdutils.WrapError(err, "Failed to decode README file.")
	}

	readmeFile.Content = string(decoded)

	return readmeFile, nil
}

func generateProjectOpenURL(projectWebURL string, defaultBranch string, branch string) string {
	if branch != "" && defaultBranch != branch {
		return projectWebURL + "/-/tree/" + url.PathEscape(branch)
	}

	return projectWebURL
}

func printProjectContentTTY(opts *options, project *gitlab.Project, readme *gitlab.File) {
	var description string
	var readmeContent string
	var err error

	fullName := project.NameWithNamespace
	if project.Description != "" {
		description, err = utils.RenderMarkdownWithoutIndentations(project.Description, opts.glamourStyle)
		if err != nil {
			description = project.Description
		}
	} else {
		description = "\n(No description provided)\n\n"
	}

	if readme != nil {
		readmeContent, err = utils.RenderMarkdown(readme.Content, opts.glamourStyle)
		if err != nil {
			readmeContent = readme.Content
		}
	}

	c := opts.io.Color()
	// Header
	fmt.Fprint(opts.io.StdOut, c.Bold(fullName))
	fmt.Fprint(opts.io.StdOut, c.Gray(description))

	// Readme
	if readme != nil {
		fmt.Fprint(opts.io.StdOut, readmeContent)
	} else {
		fmt.Fprintln(opts.io.StdOut, c.Gray("(This repository does not have a README file.)"))
	}

	fmt.Fprintln(opts.io.StdOut)
	fmt.Fprintf(opts.io.StdOut, c.Gray("View this project on GitLab: %s\n"), project.WebURL)
}

func printProjectContentRaw(opts *options, project *gitlab.Project, readme *gitlab.File) {
	fullName := project.NameWithNamespace
	description := project.Description

	fmt.Fprintf(opts.io.StdOut, "name:\t%s\n", fullName)
	fmt.Fprintf(opts.io.StdOut, "description:\t%s\n", description)

	if readme != nil {
		fmt.Fprintln(opts.io.StdOut, "---")
		fmt.Fprint(opts.io.StdOut, readme.Content)
		fmt.Fprintln(opts.io.StdOut)
	}
}

func printProjectContentJSON(opts *options, project *gitlab.Project) {
	projectJSON, _ := json.Marshal(project)
	fmt.Fprintln(opts.io.StdOut, string(projectJSON))
}
