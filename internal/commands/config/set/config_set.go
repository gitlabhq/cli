package set

import (
	"fmt"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	config func() config.Config

	hostname string
	isGlobal bool
	key      string
	value    string
}

func NewCmdSet(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		config: f.Config,
	}

	cmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Updates configuration with the value of a given key.",
		Long: heredoc.Docf(`Use %[1]sglab config set --global%[1]s to write to the global configuration.
		Specifying the %[1]s--host%[1]s flag also saves to the global configuration file.
		`, "`"),
		Example: heredoc.Doc(`
glab config set editor vim
glab config set token xxxxx --host gitlab.com
glab config set check_update false --global`),
		Args: cobra.ExactArgs(2),
		Annotations: map[string]string{
			mcpannotations.Exclude: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(args)
			if err := opts.validate(); err != nil {
				return err
			}
			return opts.run()
		},
	}

	fl := cmd.Flags()
	fl.StringVarP(&opts.hostname, "host", "", "", "Set per-host setting.")
	fl.BoolVarP(&opts.isGlobal, "global", "g", false, "Write to global '~/.config/glab-cli/config.yml' file rather than the repository's '.git/glab-cli/config.yml' file.")
	return cmd
}

func (o *options) complete(args []string) {
	o.key = args[0]
	o.value = args[1]
}

func (o *options) validate() error {
	if !config.IsKnownKey(o.key) {
		return fmt.Errorf("%q is not a recognized glab config key, run `glab config` to see the supported keys", o.key)
	}
	return nil
}

func (o *options) run() error {
	cfg := o.config()

	localCfg, _ := cfg.Local()

	var err error
	if o.isGlobal || o.hostname != "" {
		err = cfg.Set(o.hostname, o.key, o.value)
	} else {
		err = localCfg.Set(o.key, o.value)
	}

	if err != nil {
		// The value is deliberately left out of the message: keys such as
		// `token` and `job_token` hold credentials, and this error reaches
		// stderr, CI logs, and terminal scrollback. The user supplied the
		// value, so the key alone is enough to locate the failure.
		return fmt.Errorf("failed to set %q: %w", o.key, err)
	}

	if o.isGlobal || o.hostname != "" {
		err = cfg.Write()
	} else {
		err = localCfg.Write()
	}

	if err != nil {
		return fmt.Errorf("failed to write configuration to disk: %w", err)
	}
	return nil
}
