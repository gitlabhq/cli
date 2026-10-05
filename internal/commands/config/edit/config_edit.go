package edit

import (
	"fmt"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/browser"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	io     *iostreams.IOStreams
	config func() config.Config

	isLocal bool
}

func NewCmdEdit(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		config: f.Config,
		io:     f.IO(),
	}

	cmd := &cobra.Command{
		Use:   "edit",
		Short: "Opens the glab configuration file.",
		Long: heredoc.Docf(`The command uses the following order when choosing the editor to use:

		1. %[1]sglab_editor%[1]s field in the configuration file.
		1. %[1]sVISUAL%[1]s environment variable.
		1. %[1]sEDITOR%[1]s environment variable.
		`, "`"),
		Example: heredoc.Doc(`
			# Open the configuration file with the default editor
			glab config edit

			# Open the configuration file with vim
			EDITOR=vim glab config edit

			# Set vim to be used for all future 'glab config edit' invocations
			glab config set editor vim
			glab config edit

			# Open the local configuration file with the default editor
			glab config edit -l`),
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		Annotations: map[string]string{
			mcpannotations.Exclude: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.run()
		},
	}

	cmd.Flags().BoolVarP(&opts.isLocal, "local", "l", false, "Open '.git/glab-cli/config.yml' file instead of the global '~/.config/glab-cli/config.yml' file.")
	return cmd
}

func (o *options) run() error {
	var configPath string

	if o.isLocal {
		configPath = config.LocalConfigFile()
	} else {
		configPath = fmt.Sprintf("%s/config.yml", config.ConfigDir())
	}

	editor, err := cmdutils.GetEditor(o.config)
	if err != nil {
		return err
	}

	editorCommand, err := browser.Command(configPath, editor)
	if err != nil {
		return err
	}

	editorCommand.Stdin = o.io.In
	editorCommand.Stdout = o.io.StdOut
	editorCommand.Stderr = o.io.StdErr

	err = editorCommand.Run()
	if err != nil {
		return err
	}

	return nil
}
