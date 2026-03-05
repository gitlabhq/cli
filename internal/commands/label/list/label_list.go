package list

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/tableprinter"
)

type printLabel struct {
	ID          string
	Name        string
	Description string
	Color       string
}

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

	labelListCmd := &cobra.Command{
		Use:     "list [flags]",
		Short:   `List labels in the repository.`,
		Long:    ``,
		Aliases: []string{"ls"},
		Example: heredoc.Doc(`
			$ glab label list
			$ glab label ls
			$ glab label list -R owner/repository
			$ glab label list -g mygroup
		`),
		Args: cobra.ExactArgs(0),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.run()
		},
	}

	labelListCmd.Flags().IntVarP(&opts.page, "page", "p", 1, "Page number.")
	labelListCmd.Flags().IntVarP(&opts.perPage, "per-page", "P", 30, "Number of items to list per page.")
	cmdutils.EnableJSONOutput(labelListCmd, &opts.outputFormat)
	labelListCmd.Flags().StringVarP(&opts.group, "group", "g", "", "List labels for a group.")

	return labelListCmd
}

type listLabelsOptions struct {
	withCounts *bool
	perPage    int64
	page       int64
}

func (opts *listLabelsOptions) listLabelsOptions() *gitlab.ListLabelsOptions {
	projectOpts := &gitlab.ListLabelsOptions{}
	projectOpts.WithCounts = opts.withCounts
	projectOpts.PerPage = opts.perPage
	projectOpts.Page = opts.page
	return projectOpts
}

func (opts *listLabelsOptions) listGroupLabelsOptions() *gitlab.ListGroupLabelsOptions {
	groupOpts := &gitlab.ListGroupLabelsOptions{}
	groupOpts.WithCounts = opts.withCounts
	groupOpts.PerPage = opts.perPage
	groupOpts.Page = opts.page
	return groupOpts
}

func (o *options) run() error {
	var err error
	var pl []printLabel

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

	labelApiOpts := &listLabelsOptions{}
	labelApiOpts.withCounts = gitlab.Ptr(true)

	if o.page != 0 {
		labelApiOpts.page = int64(o.page)
	}
	if o.perPage != 0 {
		labelApiOpts.perPage = int64(o.perPage)
	} else {
		labelApiOpts.perPage = api.DefaultListLimit
	}

	if o.group != "" {
		labels, _, err := client.GroupLabels.ListGroupLabels(o.group, labelApiOpts.listGroupLabelsOptions())
		if err != nil {
			return err
		}
		if o.outputFormat == "json" {
			labelListJSON, _ := json.Marshal(labels)
			fmt.Fprintln(o.io.StdOut, string(labelListJSON))
		} else {
			fmt.Fprintf(o.io.StdOut, "Showing label %d of %d for group %s.\n\n", len(labels), len(labels), o.group)
			for _, label := range labels {
				pl = append(pl, printLabel{ID: strconv.FormatInt(label.ID, 10), Name: label.Name, Description: label.Description, Color: label.Color})
			}
			printLabels(pl, o.io)
		}
	} else {
		repo, err := o.baseRepo()
		if err != nil {
			return err
		}

		labels, _, err := client.Labels.ListLabels(repo.FullName(), labelApiOpts.listLabelsOptions())
		if err != nil {
			return err
		}
		if o.outputFormat == "json" {
			labelListJSON, _ := json.Marshal(labels)
			fmt.Fprintln(o.io.StdOut, string(labelListJSON))
		} else {
			fmt.Fprintf(o.io.StdOut, "Showing label %d of %d on %s.\n\n", len(labels), len(labels), repo.FullName())
			for _, label := range labels {
				pl = append(pl, printLabel{ID: strconv.FormatInt(label.ID, 10), Name: label.Name, Description: label.Description, Color: label.Color})
			}
			printLabels(pl, o.io)
		}

	}

	return nil
}

func printLabels(label []printLabel, io *iostreams.IOStreams) {
	table := tableprinter.NewTablePrinter()

	if len(label) > 0 {
		table.AddRow("ID", "Name", "Description", "Color")
	}

	for _, l := range label {
		table.AddRow(l.ID, l.Name, l.Description, l.Color)
	}

	io.LogInfo(table.String())
}
