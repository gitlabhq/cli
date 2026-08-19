package path

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	io *iostreams.IOStreams

	dir bool
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io: f.IO(),
	}

	cmd := &cobra.Command{
		Use:   "path",
		Short: "Print the location of the global configuration file.",
		Long: heredoc.Docf(`Print where %[1]sglab%[1]s reads and writes its global configuration. The location depends on the platform and whether a legacy configuration directory exists, so use this command instead of hard-coding a path.

		The command prints the path even if the file does not exist yet, so it is safe to run before the first %[1]sglab auth login%[1]s.

		Use %[1]s--dir%[1]s to print the parent directory. Grant write access to that directory rather than to %[1]sconfig.yml%[1]s alone, because %[1]sglab%[1]s writes a temporary file in that directory first and then replaces %[1]sconfig.yml%[1]s with it.

		Repository-local settings live in the repository's %[1]s.git/glab-cli/config.yml%[1]s and this command does not report them.

		If no user configuration file exists, %[1]sglab%[1]s falls back to a read-only system-wide one. This command always reports the user location.
		`, "`"),
		Example: heredoc.Doc(`
			# Print the path to the global configuration file
			glab config path

			# Print the directory that holds the configuration file
			glab config path --dir

			# Open the configuration file in an editor
			$EDITOR "$(glab config path)"`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Exclude: "true",
			"help:environment": heredoc.Doc(`
				GLAB_CONFIG_DIR: Set to a directory path to override the global configuration location.
			`),
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.run()
		},
	}

	cmd.Flags().BoolVar(&opts.dir, "dir", false, "Print the configuration directory instead of the configuration file.")

	return cmd
}

func (o *options) run() error {
	if o.dir {
		o.io.LogInfo(config.ConfigDir())
		return nil
	}

	o.io.LogInfo(config.ConfigFile())
	return nil
}
