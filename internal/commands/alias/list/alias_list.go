package list

import (
	"fmt"
	"sort"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/tableprinter"
)

type options struct {
	config func() config.Config
	io     *iostreams.IOStreams
}

func NewCmdList(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		config: f.Config,
		io:     f.IO(),
	}

	aliasListCmd := &cobra.Command{
		Use:   "list [flags]",
		Short: `List aliases.`,
		Long: heredoc.Doc(`
		List all configured aliases and their expansions. Results are sorted
		alphabetically by alias name.
		`),
		Example: heredoc.Doc(`
		# List all configured aliases
		glab alias list
		`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.run()
		},
	}
	return aliasListCmd
}

func (o *options) run() error {
	cfg := o.config()

	aliasCfg, err := cfg.Aliases()
	if err != nil {
		return fmt.Errorf("couldn't read aliases config: %w", err)
	}

	if aliasCfg.Empty() {

		o.io.LogErrorf("no aliases configured.\n")
		return nil
	}

	table := tableprinter.NewTablePrinter()
	table.Wrap = true

	aliasMap := aliasCfg.All()
	var keys []string
	for alias := range aliasMap {
		keys = append(keys, alias)
	}
	sort.Strings(keys)

	table.AddRow("Alias", "Command")
	for _, alias := range keys {
		table.AddRow(alias, aliasMap[alias])
	}
	o.io.LogInfof("%s", table.Render())

	return nil
}
