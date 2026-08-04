package configure

import (
	"os"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	dfconfig "gitlab.com/gitlab-org/cli/internal/dependencyfirewall/config"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/text"
)

// supportedManagers are the package managers this command can configure. Later
// stack slices add their manager here as each wrapper lands.
var supportedManagers = []string{"npm"}

type options struct {
	io *iostreams.IOStreams

	manager     string
	baseDir     string
	repoResolve string
	repoDeploy  string

	resolveSet bool
	deploySet  bool
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{io: f.IO()}

	cmd := &cobra.Command{
		Use:   "configure <package-manager>",
		Short: "Configure Dependency Firewall registry URLs for a package manager.",
		Long: heredoc.Docf(`
			Write a package manager's resolve and deploy registry URLs to
			%[1]s.gitlab/df/config.json%[1]s.

			Supported package managers: %[2]s.

			The file is written relative to the current working directory, so run this
			command from the directory you run the package manager in.

			Only the flags you pass are updated; existing values and unknown keys are
			preserved.
		`, "`", "`"+strings.Join(supportedManagers, "`, `")+"`") + text.BetaString,
		Example: heredoc.Doc(`
			# Set the resolve (read) and deploy (publish) registry URLs for npm
			glab dependency-firewall configure npm --repo-resolve https://gitlab.com/api/v4/projects/42/packages/npm/ --repo-deploy https://gitlab.com/api/v4/projects/42/packages/npm/

			# Update only the resolve URL; the deploy URL is preserved
			glab dependency-firewall configure npm --repo-resolve https://gitlab.com/api/v4/projects/42/packages/npm/
		`),
		ValidArgs: supportedManagers,
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd, args); err != nil {
				return err
			}
			return opts.run()
		},
	}

	cmd.Flags().StringVar(&opts.repoResolve, "repo-resolve", "", "Full registry URL to resolve (install) packages from.")
	cmd.Flags().StringVar(&opts.repoDeploy, "repo-deploy", "", "Full registry URL to deploy (publish) packages to.")
	cmd.MarkFlagsOneRequired("repo-resolve", "repo-deploy")

	return cmd
}

func (o *options) complete(cmd *cobra.Command, args []string) error {
	o.manager = args[0]
	o.resolveSet = cmd.Flags().Changed("repo-resolve")
	o.deploySet = cmd.Flags().Changed("repo-deploy")
	// baseDir anchors the .gitlab/df/config.json path. The config is per-checkout
	// tooling state that the package manager reads from its own working directory,
	// so it is written relative to the current working directory.
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	o.baseDir = wd
	return nil
}

func (o *options) run() error {
	var resolve, deploy *string
	if o.resolveSet {
		resolve = &o.repoResolve
	}
	if o.deploySet {
		deploy = &o.repoDeploy
	}

	if err := dfconfig.Merge(o.baseDir, o.manager, resolve, deploy); err != nil {
		return cmdutils.WrapError(err, "failed to write Dependency Firewall config.")
	}

	o.io.LogInfof("Wrote Dependency Firewall config to %s\n", dfconfig.Path(o.baseDir))
	return nil
}
