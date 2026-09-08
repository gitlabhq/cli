package get

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/tableprinter"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

type options struct {
	gitlabClient func() (*gitlab.Client, error)
	io           *iostreams.IOStreams

	labelID      int
	outputFormat string
}

func NewCmdGet(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
	}
	cmd := &cobra.Command{
		Use:   "get <label-id>",
		Short: "Get information about a single label by ID.",
		Long: heredoc.Docf(`
			Get information about a single label in a project, identified by
			its numeric ID. Use the JSON output to integrate with other tools
			or scripts.

			By default, the label is looked up in the current repository. Use
			%[1]s--repo%[1]s to target another project.
		`, "`"),
		Example: heredoc.Doc(`
			# Get label info using label 1234 as argument
			glab label get 1234

			# Get info about a label in another project
			glab label get 1234 -R owner/repo
		`),
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(args); err != nil {
				return err
			}

			return opts.run(f)
		},
	}

	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)

	return cmd
}

func (o *options) complete(args []string) error {
	if len(args) == 1 {
		o.labelID = utils.StringToInt(args[0])
	}

	return nil
}

func (o *options) run(f cmdutils.Factory) error {
	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	repo, err := f.BaseRepo()
	if err != nil {
		return err
	}
	label, _, err := client.Labels.GetLabel(repo.FullName(), o.labelID)
	if err != nil {
		return cmdutils.WrapError(err, "failed to get label")
	}

	if o.outputFormat == "json" {
		return o.io.PrintJSON(label)
	}

	table := tableprinter.NewTablePrinter()
	table.AddRow("Label ID", label.ID)
	table.AddRow("Name", label.Name)
	table.AddRow("Description", label.Description)
	table.AddRow("Color", label.Color)
	if priority, err := label.Priority.Get(); err == nil {
		table.AddRow("Priority", priority)
	}
	o.io.LogInfo(table.String())

	return nil
}
