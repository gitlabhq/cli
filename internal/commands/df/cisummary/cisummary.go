package cisummary

import (
	"os"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/cilog"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/summary"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type options struct {
	io      *iostreams.IOStreams
	baseDir string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{io: f.IO()}

	cmd := &cobra.Command{
		Use:   "ci-summary",
		Short: "Summarize Dependency Firewall activity from the CI log. (BETA)",
		Long: heredoc.Docf(`
			Read %[1]s.gitlab/df/ci-log.json%[1]s and print blocked and flagged packages
			recorded during a %[1]sglab dependency-firewall%[1]s run.

			The log is read from the current working directory. Run this command from
			the same directory as the %[1]sglab dependency-firewall%[1]s run that wrote the
			log, otherwise no activity is reported.

			To fail a CI/CD job when any package was blocked, run this command as the
			last command in %[1]sscript%[1]s. This catches a block even when a package
			manager continues after a refused download. In %[1]safter_script%[1]s, the
			command prints the report but can't fail the job, because GitLab ignores
			%[1]safter_script%[1]s exit codes.

			%[1]s%[1]s%[1]syaml
			install:
			  variables:
			    GLAB_ENABLE_CI_AUTOLOGIN: "true"
			  script:
			    - glab dependency-firewall npm ci
			    - glab dependency-firewall pip install -r requirements.txt
			    - glab dependency-firewall ci-summary
			%[1]s%[1]s%[1]s

			Entries accumulate across runs, and the command never clears them. A block
			from an earlier run keeps the command exiting with %[1]s3%[1]s until you delete
			%[1]s.gitlab/df/ci-log.json%[1]s.

			| Exit code | Meaning |
			|-----------|---------|
			| %[1]s0%[1]s | No blocked entries in the log (allow-only or warnings). |
			| %[1]s1%[1]s | The log could not be read. |
			| %[1]s3%[1]s | At least one entry in the log is blocked. |
		`, "`") + text.BetaString,
		Example: heredoc.Doc(`
			# Show blocked and flagged packages from the last firewall run
			glab dependency-firewall ci-summary
		`),
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(); err != nil {
				return err
			}
			return opts.run()
		},
	}

	return cmd
}

func (o *options) complete() error {
	// baseDir anchors the .gitlab/df/ci-log.json lookup. A firewall run writes its
	// log relative to the directory the package manager ran in, so read it back
	// relative to the current working directory rather than the repository root.
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	o.baseDir = wd
	return nil
}

func (o *options) run() error {
	log, err := cilog.Load(o.baseDir)
	if err != nil {
		return cmdutils.WrapError(err, "failed to read Dependency Firewall CI log.")
	}
	summary.Render(o.io, log.Entries)

	for _, e := range log.Entries {
		if e.Verdict == verdict.Blocked {
			return cmdutils.WrapErrorWithCode(cmdutils.SilentError, verdict.BlockedExitCode,
				"Dependency Firewall blocked one or more packages during this run.")
		}
	}
	return nil
}
