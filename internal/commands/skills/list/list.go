package list

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/skills/registry"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/tableprinter"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type options struct {
	io *iostreams.IOStreams
}

func NewCmdList(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io: f.IO(),
	}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the available bundled agent skills. (EXPERIMENTAL)",
		Long: heredoc.Doc(`
			List the agent skills bundled with glab. Use the name of a skill with
			'glab skills install <name>' to install just that one.
		`) + text.ExperimentalString,
		Example: heredoc.Doc(`
			# List every bundled skill with its description
			glab skills list
		`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.run()
		},
	}
	return cmd
}

func (o *options) run() error {
	skills, err := registry.All()
	if err != nil {
		return err
	}

	if len(skills) == 0 {
		o.io.LogErrorf("no skills available.\n")
		return nil
	}

	table := tableprinter.NewTablePrinter()
	table.Wrap = true
	table.SetIsTTY(o.io.IsOutputTTY())
	if w := o.io.TerminalWidth(); w > 0 {
		table.SetTerminalWidth(w)
	}
	table.AddRow("Name", "Source", "Description")
	for _, s := range skills {
		table.AddRow(s.Name, string(s.Source), s.Description)
	}
	o.io.LogInfof("%s", table.Render())
	return nil
}
