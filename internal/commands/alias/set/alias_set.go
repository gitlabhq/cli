package set

import (
	"fmt"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/google/shlex"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	io     *iostreams.IOStreams
	config func() config.Config

	name      string
	expansion string
	isShell   bool
	rootCmd   *cobra.Command
}

func NewCmdSet(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		config: f.Config,
		io:     f.IO(),
	}

	aliasSetCmd := &cobra.Command{
		Use:   "set <alias name> '<command>' [flags]",
		Short: `Set an alias for a longer command.`,
		Long: heredoc.Docf(`
		Declare a word as an alias for a longer command.

		Use quotation marks when you define a command, as shown in the examples.

		Your expansion can include arguments and flags. If your expansion
		includes positional placeholders such as %[1]s$1%[1]s or %[1]s$2%[1]s, any extra
		arguments that follow the invocation of the alias are inserted into
		those placeholders.

		To run an alias through %[1]ssh%[1]s, a shell converter, specify the
		%[1]s--shell%[1]s flag. With shell conversion, you can compose commands
		with %[1]s|%[1]s or redirect with %[1]s>%[1]s. Shell aliases have these caveats:

		- Extra arguments that follow the alias are not passed to the expansion.
		  To accept arguments, use %[1]s$1%[1]s, %[1]s$2%[1]s, and so on.
		- To accept all arguments, use %[1]s$@%[1]s.

		On Windows, shell aliases run through %[1]ssh%[1]s as installed by Git for
		Windows. If you installed Git in another way on Windows, shell aliases
		might not work.
		`, "`"),
		Example: heredoc.Doc(`
		# Define an alias for "mr view"
		glab alias set mrv 'mr view'
		# Run the alias; it expands to "glab mr view -w 123"
		glab mrv -w 123

		# Define an alias with a positional placeholder
		glab alias set createissue 'issue create --title "$1"'
		# Run the alias with an argument and an extra flag
		glab createissue "My Issue" --description "Something is broken."

		# Define a shell alias that pipes glab output to grep
		glab alias set --shell igrep 'glab issue list --assignee="$1" | grep $2'
		# Run the shell alias with two arguments
		glab igrep user foo
		`),
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: cobra.NoFileCompletions,
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(cmd, args)

			return opts.run()
		},
	}
	aliasSetCmd.Flags().BoolVarP(&opts.isShell, "shell", "s", false, "Declare an alias to be passed through a shell interpreter.")
	return aliasSetCmd
}

func (o *options) complete(cmd *cobra.Command, args []string) {
	o.rootCmd = cmd.Root()
	o.name = args[0]
	o.expansion = args[1]
}

func (o *options) run() error {
	c := o.io.Color()
	cfg := o.config()

	aliasCfg, err := cfg.Aliases()
	if err != nil {
		return err
	}

	if o.io.IsaTTY && o.io.IsErrTTY {
		o.io.LogErrorf("- Adding alias for %s: %s.\n", c.Bold(o.name), c.Bold(o.expansion))
	}

	expansion := o.expansion
	isShell := o.isShell
	if isShell && !strings.HasPrefix(expansion, "!") {
		expansion = "!" + expansion
	}
	isShell = strings.HasPrefix(expansion, "!")

	if validCommand(o.rootCmd, o.name) {
		return fmt.Errorf("could not create alias: %q is already a glab command", o.name)
	}

	if !isShell && !validCommand(o.rootCmd, expansion) {
		return fmt.Errorf("could not create alias: %s does not correspond to a glab command", expansion)
	}

	successMsg := fmt.Sprintf("%s Added alias.", c.Green("✓"))
	if oldExpansion, ok := aliasCfg.Get(o.name); ok {
		successMsg = fmt.Sprintf("%s Changed alias %s from %s to %s.",
			c.Green("✓"),
			c.Bold(o.name),
			c.Bold(oldExpansion),
			c.Bold(expansion),
		)
	}

	err = aliasCfg.Set(o.name, expansion)
	if err != nil {
		return fmt.Errorf("could not create alias: %w", err)
	}

	o.io.LogError(successMsg)
	return nil
}

func validCommand(rootCmd *cobra.Command, expansion string) bool {
	split, err := shlex.Split(expansion)
	if err != nil {
		return false
	}

	cmd, _, err := rootCmd.Traverse(split)
	return err == nil && cmd != rootCmd
}
