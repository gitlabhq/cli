package set

import (
	"fmt"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/auth/authutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/config/confighelp"
	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/oauth2"
)

type options struct {
	io     *iostreams.IOStreams
	config func() config.Config

	hostname string
	isGlobal bool
	key      string
	value    string
}

func NewCmdSet(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:     f.IO(),
		config: f.Config,
	}

	cmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Updates configuration with the value of a given key.",
		Long: heredoc.Docf(`Use %[1]sglab config set --global%[1]s to write to the global configuration.
		Specifying the %[1]s--host%[1]s flag also saves to the global configuration file.

		Keys that glab manages for itself are always written to the global
		configuration, even without %[1]s--global%[1]s.
		`, "`"),
		Example: heredoc.Doc(`
glab config set editor vim
glab config set token xxxxx --host gitlab.com
glab config set check_update false --global`),
		Args: cobra.ExactArgs(2),
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
			switch len(args) {
			case 0:
				return confighelp.KeyCompletions(true), cobra.ShellCompDirectiveNoFileComp
			case 1:
				return confighelp.ValueCompletions(args[0])
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
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
	if config.ConfigKeyEquivalence(o.key) == "client_id" {
		o.value = strings.TrimSpace(o.value)
	}
}

func (o *options) validate() error {
	if !config.IsKnownKey(o.key) {
		return fmt.Errorf("%q is not a recognized glab config key, run `glab config` to see the supported keys", o.key)
	}
	if config.ConfigKeyEquivalence(o.key) == "client_id" && o.value != "" {
		if err := oauth2.ValidateClientID(o.value); err != nil {
			return fmt.Errorf("invalid value for %q: %w", o.key, err)
		}
	}
	return nil
}

func (o *options) run() error {
	cfg := o.config()

	localCfg, _ := cfg.Local()

	if err := o.clearStaleOAuth2Config(cfg); err != nil {
		return err
	}

	// A key glab maintains for itself is read from the global file only, so a
	// local write would land where nothing reads it and the old local value
	// would keep winning. Route those to the global file instead.
	routed := !o.isGlobal && o.hostname == "" && config.IsGlobalOnlyKey(o.key)
	writeGlobal := o.isGlobal || o.hostname != "" || routed
	if routed {
		o.io.LogErrorf("%s %s is managed by glab and is always stored in the global configuration: %s\n",
			o.io.Color().DotWarnIcon(), o.key, config.ConfigFile())
	}

	var err error
	if writeGlobal {
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

	if writeGlobal {
		err = cfg.Write()
	} else {
		err = localCfg.Write()
	}

	if err != nil {
		return fmt.Errorf("failed to write configuration to disk: %w", err)
	}
	return nil
}

// clearStaleOAuth2Config drops the OAuth session fields when a token is written
// to an OAuth-authenticated host. Left in place, is_oauth2 sends the new token
// as a Bearer credential: API calls still succeed, since GitLab accepts a
// personal access token that way, but the credential helpers fail, so the
// breakage surfaces far from this command. Persisted by the caller's Write.
func (o *options) clearStaleOAuth2Config(cfg config.Config) error {
	if o.hostname == "" || o.value == "" || config.ConfigKeyEquivalence(o.key) != "token" {
		return nil
	}

	// searchEnvVars: false, so a stray GLAB_IS_OAUTH2 cannot trigger a rewrite of
	// the config file. A read failure only costs the cleanup, which `glab auth
	// status` also reports, so it must not fail the write the user asked for.
	isOAuth2, _, err := cfg.GetWithSource(o.hostname, "is_oauth2", false)
	if err != nil {
		dbg.Debugf("could not read is_oauth2 for %s, leaving the OAuth configuration alone: %v", o.hostname, err)
		return nil
	}
	if isOAuth2 != "true" {
		return nil
	}

	if err := authutils.ClearOAuth2Fields(cfg, o.hostname); err != nil {
		return fmt.Errorf("failed to clear the OAuth configuration for %q: %w", o.hostname, err)
	}

	o.io.LogErrorf("%s Cleared the OAuth configuration for %s, which authenticates with the token you set.\n", o.io.Color().WarnIcon(), o.hostname)
	return nil
}
