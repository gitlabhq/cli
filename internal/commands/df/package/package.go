package pkgcmd

import (
	"context"
	"errors"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/purl"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/summary"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type options struct {
	io           *iostreams.IOStreams
	baseRepo     func() (projectID string, err error)
	gitlabClient func() (*gitlab.Client, error)

	purlArg string
}

// NewCmd builds the `glab dependency-firewall package <PURL>` command.
func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io: f.IO(),
		baseRepo: func() (string, error) {
			r, err := f.BaseRepo()
			if err != nil {
				return "", err
			}
			return r.FullName(), nil
		},
		gitlabClient: f.GitLabClient,
	}

	cmd := &cobra.Command{
		Use:   "package <purl>",
		Short: "Check a package against the GitLab Dependency Firewall. (EXPERIMENTAL)",
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		Long: heredoc.Docf(`
			Check a single package coordinate against the GitLab Dependency Firewall policy for the current project and report the outcome (allow, warning, blocked). No package manager binary is required.

			Supported package URL (PURL) types are %[1]snpm%[1]s, %[1]spypi%[1]s, %[1]smaven%[1]s, and %[1]sgem%[1]s. The PURL must include a version, for example %[1]spkg:npm/left-pad@1.3.0%[1]s.

			This command does not write to the CI log at %[1]s.gitlab/df/ci-log.json%[1]s, so %[1]sglab dependency-firewall ci-summary%[1]s does not include its result.

			Exit codes:

			| Exit code | Meaning |
			|-----------|---------|
			| %[1]s0%[1]s | Allow or warning. |
			| %[1]s1%[1]s | Misconfiguration or transport error. |
			| %[1]s3%[1]s | Blocked. |
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Check an npm package version
			glab dependency-firewall package pkg:npm/left-pad@1.3.0

			# Check a PyPI package version
			glab dependency-firewall package pkg:pypi/requests@2.31.0

			# Check a Maven package version
			glab dependency-firewall package pkg:maven/org.slf4j/slf4j-api@2.0.13

			# Check a RubyGems package version
			glab dependency-firewall package pkg:gem/rails@7.1.3
		`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(args); err != nil {
				return err
			}
			return opts.run(cmd.Context())
		},
	}

	return cmd
}

func (o *options) complete(args []string) error {
	o.purlArg = args[0]
	return nil
}

func (o *options) run(ctx context.Context) error {
	p, err := purl.Parse(o.purlArg)
	if err != nil {
		return cmdutils.WrapError(err, "invalid PURL.")
	}

	// The evaluate endpoint requires a version and rejects a blank one with 400,
	// so reject it here with a clear input error instead of resolving the repo
	// and client only to turn the predictable rejection into a generic
	// "failed to check package".
	if p.Version == "" {
		return cmdutils.WrapError(
			errors.New("the PURL must include a version, for example pkg:npm/left-pad@1.3.0"),
			"invalid PURL.")
	}

	projectID, err := o.baseRepo()
	if err != nil {
		return cmdutils.WrapError(err, "failed to resolve GitLab project from the git remote.")
	}

	client, err := o.gitlabClient()
	if err != nil {
		return cmdutils.WrapError(err, "failed to create a GitLab API client.")
	}

	// A single coordinate is checked once, so the CachingChecker's memoization
	// and coalescing add nothing. More importantly, it never surfaces an error:
	// it maps every transport, auth, or server failure to a Blocked result. That
	// is the right fail-closed posture for the proxy guarding an install, but it
	// erases the "we could not ask" case this command must report as exit 1
	// rather than a false "blocked". So call the checker directly and own the
	// fail-open (not evaluating) and fail-report (transport error) decisions
	// here, bounded by the same timeout the proxy uses.
	checker, err := policy.New(client)
	if err != nil {
		return cmdutils.WrapError(err, "failed to initialize the Dependency Firewall policy checker.")
	}

	checkCtx, cancel := context.WithTimeout(ctx, policy.CheckTimeout)
	defer cancel()
	res, err := checker.Check(checkCtx, policy.Request{
		Coordinate: policy.Coordinate{
			Ecosystem: p.Type,
			Name:      p.Name,
			Version:   p.Version,
		},
		ProjectID: projectID,
		Operation: policy.Download,
	})
	// p.Name and p.Version are percent-decoded from user input, so a crafted
	// PURL can carry newlines or terminal control sequences. summary.Render
	// sanitizes its cells; the direct-output paths below must sanitize too.
	coordinate := text.SanitizeInline(p.Name + "@" + p.Version)

	switch {
	case policy.IsNotEvaluating(err):
		// Fail open: the firewall is not evaluating this project (feature flag
		// off, not enforced, or the token cannot see the project), so there is
		// no verdict to apply and the package is allowed.
		o.io.LogInfof("%s: the Dependency Firewall is not evaluating this project; allowing.\n", coordinate)
		return nil
	case err != nil:
		return cmdutils.WrapError(err, "failed to check package.")
	}

	// An allow carries no verdict entry, so summary.Render would print "no
	// activity recorded". Report it with a single explicit line instead; only a
	// warn or block goes through the summary box.
	if res.Verdict == verdict.Allowed {
		o.io.LogInfof("%s: allowed by the Dependency Firewall.\n", coordinate)
		return nil
	}

	summary.Render(o.io, verdictEntries(p.Name, p.Version, res))

	if res.Verdict == verdict.Blocked {
		return cmdutils.WrapErrorWithCode(cmdutils.SilentError, verdict.BlockedExitCode,
			"Dependency Firewall blocked the package.")
	}
	return nil
}

// verdictEntries turns a warn or block Result into the single verdict entry the
// summary renders. It is not called for an allow, which run reports directly.
func verdictEntries(name, version string, res policy.Result) []verdict.Entry {
	return []verdict.Entry{{
		Package: name,
		Version: version,
		Verdict: res.Verdict,
		Reason:  res.Reason,
	}}
}
