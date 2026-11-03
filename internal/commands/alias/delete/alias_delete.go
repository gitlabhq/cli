package delete

import (
	"fmt"
	"slices"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	config func() config.Config
	name   string
	io     *iostreams.IOStreams
}

func NewCmdDelete(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		config: f.Config,
		io:     f.IO(),
	}

	aliasDeleteCmd := &cobra.Command{
		Use:   "delete <alias name> [flags]",
		Short: `Delete an alias.`,
		Long: heredoc.Docf(`
		Delete an alias by name. The deletion is permanent. To restore the
		alias, run %[1]sglab alias set%[1]s with the original expansion.
		`, "`"),
		Example: heredoc.Doc(`
		# Delete the alias named "mrv"
		glab alias delete mrv
		`),
		Args: cobra.ExactArgs(1),
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			if len(args) != 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			aliasCfg, err := opts.config().Aliases()
			if err != nil {
				dbg.Debugf("error getting aliases: %v", err)
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			aliases := aliasCfg.All()
			completions := make([]string, 0, len(aliases))
			for k, v := range aliases {
				completions = append(completions, cobra.CompletionWithDesc(k, v))
			}
			slices.Sort(completions)
			return completions, cobra.ShellCompDirectiveNoFileComp
		},
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(args)

			return opts.run()
		},
	}
	return aliasDeleteCmd
}

func (o *options) complete(args []string) {
	o.name = args[0]
}

func (o *options) run() error {
	c := o.io.Color()
	cfg := o.config()

	aliasCfg, err := cfg.Aliases()
	if err != nil {
		return fmt.Errorf("couldn't read aliases config: %w", err)
	}

	expansion, ok := aliasCfg.Get(o.name)
	if !ok {
		return fmt.Errorf("no such alias %q", o.name)
	}
	err = aliasCfg.Delete(o.name)
	if err != nil {
		return fmt.Errorf("failed to delete alias '%s': %w", o.name, err)
	}
	redCheck := c.Red("✓")
	o.io.LogErrorf("%s Deleted alias '%s'; was '%s'.\n", redCheck, o.name, expansion)
	return nil
}
