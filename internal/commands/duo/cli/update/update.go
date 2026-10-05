package update

import (
	"context"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/binarymgr"
	"gitlab.com/gitlab-org/cli/internal/binarymgr/binaries"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	io  *iostreams.IOStreams
	cfg config.Config
	yes bool
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{io: f.IO(), cfg: f.Config()}
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update the GitLab Duo CLI binary to the latest compatible version.",
		Long: heredoc.Docf(`
			Checks for a newer GitLab Duo CLI version and installs it. If the binary is not installed yet, %[1]sglab%[1]s downloads the latest compatible version.

			Updates stay within the major version this version of %[1]sglab%[1]s supports. When a newer major version exists, update %[1]sglab%[1]s first.

			Updates do not apply when you use a custom binary set with %[1]sGLAB_DUO_CLI_BINARY_PATH%[1]s or the %[1]sduo_cli_binary_path%[1]s configuration key.

			%[1]sglab duo cli --update%[1]s does the same thing.
		`, "`"),
		Example: heredoc.Doc(`
			# Update the GitLab Duo CLI, or download it if not installed
			glab duo cli update

			# Skip the download prompt
			glab duo cli update --yes`),
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		Annotations: map[string]string{
			mcpannotations.Exclude: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.run(cmd.Context())
		},
	}

	cmd.Flags().BoolVarP(&opts.yes, "yes", "y", false, "Skip the download prompt when the binary is not installed.")

	return cmd
}

func (o *options) run(ctx context.Context) error {
	spec := binaries.DuoCLI()
	runner := &binarymgr.Runner{
		IO:      o.io,
		Cfg:     o.cfg,
		Spec:    spec,
		Manager: binarymgr.NewManager(o.io, spec),
		Update:  true,
		Yes:     o.yes,
	}
	return runner.Run(ctx)
}
