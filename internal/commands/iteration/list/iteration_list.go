package list

import (
	"encoding/json"
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
	"gitlab.com/gitlab-org/cli/internal/utils"
)

type options struct {
	io           *iostreams.IOStreams
	apiClient    func(repoHost string) (*api.Client, error)
	baseRepo     func() (glrepo.Interface, error)
	group        string
	page         int
	perPage      int
	outputFormat string
}

func NewCmdList(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
		baseRepo:  f.BaseRepo,
	}

	iterationListCmd := &cobra.Command{
		Use:     "list [flags]",
		Short:   `List project iterations`,
		Long:    ``,
		Aliases: []string{"ls"},
		Example: heredoc.Doc(`
			- glab iteration list
			- glab iteration ls
			- glab iteration list -R owner/repository
			- glab iteration list -g mygroup
		`),
		Args: cobra.ExactArgs(0),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.run()
		},
	}

	iterationListCmd.Flags().IntVarP(&opts.page, "page", "p", 1, "Page number.")
	iterationListCmd.Flags().IntVarP(&opts.perPage, "per-page", "P", 30, "Number of items to list per page.")
	cmdutils.EnableJSONOutput(iterationListCmd, &opts.outputFormat)
	iterationListCmd.Flags().StringVarP(&opts.group, "group", "g", "", "List iterations for a group.")
	return iterationListCmd
}

type listProjectIterationsOptions struct {
	IncludeAncestors *bool
	PerPage          int64
	Page             int64
}

func (opts *listProjectIterationsOptions) listProjectIterationsOptions() *gitlab.ListProjectIterationsOptions {
	projectOpts := &gitlab.ListProjectIterationsOptions{}
	projectOpts.IncludeAncestors = opts.IncludeAncestors
	projectOpts.PerPage = opts.PerPage
	projectOpts.Page = opts.Page
	return projectOpts
}

func (opts *listProjectIterationsOptions) listGroupIterationsOptions() *gitlab.ListGroupIterationsOptions {
	groupOpts := &gitlab.ListGroupIterationsOptions{}
	groupOpts.IncludeAncestors = opts.IncludeAncestors
	groupOpts.PerPage = opts.PerPage
	groupOpts.Page = opts.Page
	return groupOpts
}

func (o *options) run() error {
	// NOTE: this command can not only be used for projects,
	// so we have to manually check for the base repo, if it doesn't exist,
	// we bootstrap the client with the default hostname.
	var repoHost string
	if baseRepo, err := o.baseRepo(); err == nil {
		repoHost = baseRepo.RepoHost()
	}
	apiClient, err := o.apiClient(repoHost)
	if err != nil {
		return err
	}
	client := apiClient.Lab()

	iterationApiOpts := &listProjectIterationsOptions{}
	iterationApiOpts.IncludeAncestors = gitlab.Ptr(true)

	if o.page != 0 {
		iterationApiOpts.Page = int64(o.page)
	}
	if o.perPage != 0 {
		iterationApiOpts.PerPage = int64(o.perPage)
	} else {
		iterationApiOpts.PerPage = api.DefaultListLimit
	}

	var iterationBuilder strings.Builder

	if o.group != "" {
		iterations, _, err := client.GroupIterations.ListGroupIterations(o.group, iterationApiOpts.listGroupIterationsOptions())
		if err != nil {
			return err
		}
		if o.outputFormat == "json" {
			iterationListJSON, _ := json.Marshal(iterations)
			fmt.Fprintln(o.io.StdOut, string(iterationListJSON))
		} else {
			fmt.Fprintf(o.io.StdOut, "Showing iteration %d of %d for group %s.\n\n", len(iterations), len(iterations), o.group)
			for _, iteration := range iterations {
				iterationBuilder.WriteString(formatIterationInfo(iteration.Description, iteration.Title, iteration.WebURL))
			}
			fmt.Fprintln(o.io.StdOut, utils.Indent(iterationBuilder.String(), " "))
		}
	} else {
		repo, err := o.baseRepo()
		if err != nil {
			return err
		}
		iterations, _, err := client.ProjectIterations.ListProjectIterations(repo.FullName(), iterationApiOpts.listProjectIterationsOptions())
		if err != nil {
			return err
		}
		if o.outputFormat == "json" {
			iterationListJSON, _ := json.Marshal(iterations)
			fmt.Fprintln(o.io.StdOut, string(iterationListJSON))
		} else {
			fmt.Fprintf(o.io.StdOut, "Showing iteration %d of %d on %s.\n\n", len(iterations), len(iterations), repo.FullName())
			for _, iteration := range iterations {
				iterationBuilder.WriteString(formatIterationInfo(iteration.Description, iteration.Title, iteration.WebURL))
			}
			fmt.Fprintln(o.io.StdOut, utils.Indent(iterationBuilder.String(), " "))
		}
	}
	return nil
}

func formatIterationInfo(description string, title string, webURL string) string {
	if description != "" {
		description = fmt.Sprintf(" -> %s", description)
	}
	return fmt.Sprintf("%s%s (%s)\n", title, description, webURL)
}
