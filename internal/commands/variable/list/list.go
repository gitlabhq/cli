package list

import (
	"encoding/json"
	"fmt"

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

type options struct {
	apiClient func(repoHost string) (*api.Client, error)
	io        *iostreams.IOStreams
	baseRepo  func() (glrepo.Interface, error)
	page      int
	perPage   int

	group        string
	outputFormat string
	instance     bool
}

func NewCmdList(f cmdutils.Factory, runE func(opts *options) error) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
		baseRepo:  f.BaseRepo,
	}

	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List variables for a project or group.",
		Aliases: []string{"ls"},
		Args:    cobra.ExactArgs(0),
		Example: heredoc.Doc(`
			$ glab variable list
			$ glab variable list -i
			$ glab variable list --per-page 100 --page 1
			$ glab variable list --group gitlab-org
			$ glab variable list --group gitlab-org --per-page 100
		`,
		),
		Annotations: map[string]string{
			mcpannotations.Exclude: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd); err != nil {
				return err
			}

			if runE != nil {
				return runE(opts)
			}

			return opts.run()
		},
	}

	cmdutils.EnableRepoOverride(cmd, f)
	cmd.PersistentFlags().StringP("group", "g", "", "Select a group or subgroup. Ignored if a repository argument is set.")
	cmdutils.EnableJSONOutput(cmd, &opts.outputFormat)
	cmd.Flags().IntVarP(&opts.perPage, "per-page", "P", 20, "Number of items to list per page.")
	cmd.Flags().IntVarP(&opts.page, "page", "p", 1, "Page number.")
	cmd.Flags().BoolVarP(&opts.instance, "instance", "i", false, "Display instance variables.")

	return cmd
}

func (o *options) complete(cmd *cobra.Command) error {
	group, err := cmdutils.GroupOverride(cmd)
	if err != nil {
		return err
	}
	o.group = group

	return nil
}

func (o *options) run() error {
	color := o.io.Color()

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

	table := tableprinter.NewTablePrinter()

	if o.group != "" {
		if o.outputFormat != "json" {
			o.io.LogInfof("Listing variables for the %s group:\n\n", color.Bold(o.group))
		}
		listOpts := &gitlab.ListGroupVariablesOptions{
			ListOptions: gitlab.ListOptions{
				Page:    int64(o.page),
				PerPage: int64(o.perPage),
			},
		}
		variables, _, err := client.GroupVariables.ListVariables(o.group, listOpts)
		if err != nil {
			return err
		}
		if o.outputFormat == "json" {
			varListJSON, _ := json.Marshal(variables)
			fmt.Fprintln(o.io.StdOut, string(varListJSON))

		} else {
			table.AddRow("KEY", "PROTECTED", "MASKED", "HIDDEN", "EXPANDED", "SCOPE", "DESCRIPTION")
			for _, variable := range variables {
				table.AddRow(variable.Key, variable.Protected, variable.Masked, variable.Hidden, !variable.Raw, variable.EnvironmentScope, variable.Description)
			}
		}
	} else if o.instance {
		if o.outputFormat != "json" {
			o.io.LogInfo("Listing variables for the instance\n\n")
		}
		listOpts := &gitlab.ListInstanceVariablesOptions{
			ListOptions: gitlab.ListOptions{
				Page:    int64(o.page),
				PerPage: int64(o.perPage),
			},
		}
		variables, _, err := client.InstanceVariables.ListVariables(listOpts)
		if err != nil {
			return err
		}
		if o.outputFormat == "json" {
			varListJSON, _ := json.Marshal(variables)
			fmt.Fprintln(o.io.StdOut, string(varListJSON))

		} else {
			table.AddRow("KEY", "PROTECTED", "MASKED", "EXPANDED", "SCOPE", "DESCRIPTION")
			for _, variable := range variables {
				table.AddRow(variable.Key, variable.Protected, variable.Masked, !variable.Raw, "", variable.Description)
			}
		}
	} else {
		repo, err := o.baseRepo()
		if err != nil {
			return err
		}
		if o.outputFormat != "json" {
			o.io.LogInfof("Listing variables from the %s project:\n\n", color.Bold(repo.FullName()))
		}
		listOpts := &gitlab.ListProjectVariablesOptions{
			ListOptions: gitlab.ListOptions{
				Page:    int64(o.page),
				PerPage: int64(o.perPage),
			},
		}
		variables, _, err := client.ProjectVariables.ListVariables(repo.FullName(), listOpts)
		if err != nil {
			return err
		}
		if o.outputFormat == "json" {
			varListJSON, _ := json.Marshal(variables)
			fmt.Fprintln(o.io.StdOut, string(varListJSON))
		} else {
			table.AddRow("KEY", "PROTECTED", "MASKED", "HIDDEN", "EXPANDED", "SCOPE", "DESCRIPTION")
			for _, variable := range variables {
				table.AddRow(variable.Key, variable.Protected, variable.Masked, variable.Hidden, !variable.Raw, variable.EnvironmentScope, variable.Description)
			}
		}
	}

	if o.outputFormat != "json" {
		fmt.Fprint(o.io.StdOut, table.String())
	}
	return nil
}
