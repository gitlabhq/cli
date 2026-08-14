// Package login implements `glab artifact-registry login`, which
// authenticates a package manager against the GitLab Artifact Registry.
package login

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/api/artifactregistry"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/dockercredhelper"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type options struct {
	io *iostreams.IOStreams
	// apiClient builds the client that performs the verification exchange.
	// Deliberately not f.ApiClient: see its wiring in NewCmd. A field rather
	// than an inline call so a test can assert which host it is built for.
	apiClient       func(repoHost string) (*api.Client, error)
	cfg             config.Config
	defaultHostname string
	// supportedOS reports whether this platform can run the Docker credential
	// helper shim. A field rather than a direct call so a test can exercise the
	// unsupported path on a supported platform.
	supportedOS func() error

	hostname string
	registry string
	duration time.Duration
	// durationChanged is set from cmd.Flags().Changed("duration"), since the
	// flag's own value cannot distinguish "not passed" from "passed as the
	// zero default". Nothing reads duration itself yet; see the flag's
	// registration.
	durationChanged bool

	docker bool
}

// NewCmd returns the `glab artifact-registry login` command.
func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io: f.IO(),
		// WithoutTokenFromEnvironment, so this client resolves the same identity
		// the Docker credential helper will
		// (internal/commands/auth/docker.Helper.getArtifactRegistryToken builds
		// its client the same way). f.ApiClient reads GITLAB_TOKEN, which the
		// helper ignores, so verifying with it would check an identity Docker
		// never uses: a valid GITLAB_TOKEN over a dead stored token reports
		// success and then 401s on every pull, and the reverse fails a login
		// that would have worked.
		apiClient: func(repoHost string) (*api.Client, error) {
			return api.NewClientFromConfig(repoHost, f.Config(), false, f.BuildInfo().UserAgent(), api.WithoutTokenFromEnvironment())
		},
		cfg:             f.Config(),
		defaultHostname: f.DefaultHostname(),
		supportedOS:     dockercredhelper.Supported,
	}

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate a package manager against the GitLab Artifact Registry. (EXPERIMENTAL)",
		Long: heredoc.Docf(`
			Configure a package manager to authenticate against the GitLab Artifact
			Registry, using a short-lived access token exchanged from your GitLab
			session.

			With %[1]s--docker%[1]s, glab is registered as a Docker credential helper
			for the registry, and Docker exchanges a fresh token on every pull or
			push, so %[1]s--duration%[1]s does not apply.

			Use %[1]s--registry%[1]s only for a registry the Artifact Registry
			actually backs. The credential helper prefers the artifact registry
			token and falls back to %[1]scontainer_registry_domains%[1]s only
			when that exchange fails, so a container registry listed here gets an
			artifact registry token it rejects on every pull.

			Docker runs that credential helper as its own subprocess, which reads
			your credentials from the configuration file and ignores
			%[1]sGITLAB_TOKEN%[1]s. This command verifies the login the same way, so
			run %[1]sglab auth login%[1]s first if no token is stored for the host.
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Configure Docker to authenticate against a registry
			glab artifact-registry login --docker --registry registry.example.com
		`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
			"help:environment": heredoc.Doc(`
				DOCKER_CONFIG: the directory holding Docker's config.json, written by --docker. Defaults to ~/.docker.
			`),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts.complete(cmd)

			if err := opts.validate(); err != nil {
				return err
			}
			return opts.run(cmd.Context())
		},
	}

	cmd.Flags().BoolVar(&opts.docker, "docker", false, "Configure Docker to authenticate against the registry. Writes to $DOCKER_CONFIG, or ~/.docker when it is unset.")
	cobra.CheckErr(cmd.MarkFlagRequired("docker"))

	cmd.Flags().StringVar(&opts.registry, "registry", "", "Bare hostname of the registry to authenticate against.")
	cobra.CheckErr(cmd.MarkFlagRequired("registry"))
	// --duration is accepted and ignored, deliberately, and unconditionally so
	// far: --docker is the only tool this command configures, and its credential
	// helper exchanges its own token for every request, with its own lifetime
	// (artifactregistry.DockerHelperDuration), so there is no token here whose
	// validity a user could choose. Rejecting the flag instead would break the
	// uniform flag set the other tools use, and silently accepting it would let
	// a user believe they had shortened a token's life. So it is accepted, and
	// run() says it did nothing.
	//
	// The zero default is part of that: artifactregistry.DefaultDuration here
	// would put "(default 15m0s)" in the generated docs for a value nothing
	// reads. Step 5 gives the flag a consumer and restores the default, and is
	// also where the MinDuration/MaxDuration check belongs: bounds on a value
	// that is never sent anywhere would only be theatre.
	cmd.Flags().DurationVar(&opts.duration, "duration", 0, "How long the exchanged token should remain valid. Ignored for now: --docker is the only tool this command configures, and its credential helper mints a fresh token for every request.")
	cmd.Flags().StringVar(&opts.hostname, "hostname", "", "GitLab hostname to request the token from. Defaults to the configured GitLab instance.")

	return cmd
}

// complete reads the state that lives on the parsed command rather than in a
// flag variable.
func (o *options) complete(cmd *cobra.Command) {
	o.durationChanged = cmd.Flags().Changed("duration")
}

// validate rejects everything that makes the login impossible, before any
// network call or write. Only --docker exists so far, so every --registry rule
// here is a docker rule; steps 5 and 6 branch this on the tool once other
// formats (a full URL) are valid too.
//
// Two kinds of error come out of it. A bad flag value is a
// *cmdutils.FlagError, naming the flag whose value the user has to change. The
// environment checks below (platform, glab on PATH) return whatever
// dockercredhelper hands back, deliberately not a FlagError: no flag the user
// could retype fixes either one, so pointing at a flag would misdirect.
//
// No FlagError starts with the flag name, also deliberately. fang's error
// handler title-cases the first word of whatever it renders, which turns a
// leading "--docker=false" into "--Docker=False": a flag spelling that does not
// exist. Leading with "invalid" keeps the flag as the user typed it.
func (o *options) validate() error {
	// MarkFlagRequired only checks that --docker was passed, so an explicit
	// --docker=false reaches here and would otherwise configure Docker anyway.
	// Steps 5 and 6 turn this into a "pick at least one tool" check.
	if !o.docker {
		return &cmdutils.FlagError{Err: fmt.Errorf("invalid --docker: false is not supported, and --docker is the only tool this command can configure so far")}
	}

	if o.hostname != "" {
		// Checked before HostnameValidator, which only rejects "/" and ":": a
		// hostname with a space otherwise reaches glinstance.APIEndpoint and dies
		// with a net/url parse error rather than a FlagError naming the flag.
		if hasWhitespaceOrControl(o.hostname) {
			return &cmdutils.FlagError{Err: fmt.Errorf("invalid --hostname: must not contain whitespace or control characters (got %q)", o.hostname)}
		}
		if err := glinstance.HostnameValidator(o.hostname); err != nil {
			return &cmdutils.FlagError{Err: fmt.Errorf("error parsing --hostname: %w", err)}
		}
	}

	if o.registry == "" {
		return &cmdutils.FlagError{Err: fmt.Errorf("invalid --registry: must not be empty")}
	}
	// Rejected rather than trimmed: loginDocker writes the value verbatim into
	// artifact_registry_domains but config.ParseDomains reads it back trimmed,
	// so " registry.example.com" would never match the credHelpers key Docker
	// looks up, and every login would append another entry to the domain list.
	if hasWhitespaceOrControl(o.registry) {
		return &cmdutils.FlagError{Err: fmt.Errorf("invalid --registry: must not contain whitespace or control characters (got %q)", o.registry)}
	}
	if strings.Contains(o.registry, "://") {
		return &cmdutils.FlagError{Err: fmt.Errorf("invalid --registry: must be a bare hostname (got %q); Docker credential helpers receive bare hostnames, not URLs", o.registry)}
	}
	if strings.Contains(o.registry, "/") {
		return &cmdutils.FlagError{Err: fmt.Errorf("invalid --registry: must not contain a path (got %q); Docker matches credHelpers entries on the host, optionally with a port, so a path would never match", o.registry)}
	}
	if strings.Contains(o.registry, ",") {
		return &cmdutils.FlagError{Err: fmt.Errorf("invalid --registry: must not contain a comma (got %q); artifact_registry_domains stores registries as a comma-separated list", o.registry)}
	}

	// Last, after the flag rules: a user who typed a bad --registry should hear
	// about the flag they can fix, not about their platform.
	//
	// Both are checked here rather than left to dockercredhelper.Install,
	// because run exchanges a token before it writes anything: without them, a
	// login that can never work mints a live credential on the server and only
	// then reports that Docker can never use it. Together they are everything
	// Install can reject before its first write, so past this point an install
	// failure is something that was not knowable up front.
	if err := o.supportedOS(); err != nil {
		return err
	}
	// The shim is written next to glab and shells out to it, so a glab that PATH
	// cannot resolve is as fatal as an unsupported platform. Install repeats the
	// lookup, which is cheap and keeps it safe for callers that skip this check.
	if _, err := dockercredhelper.Locate(); err != nil {
		return err
	}

	return nil
}

// hasWhitespaceOrControl reports whether s holds whitespace or a control
// character. Neither belongs in a hostname, and both survive a write only to
// break the read that follows it.
func hasWhitespaceOrControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	})
}

func (o *options) run(ctx context.Context) error {
	// Warned before the exchange, so the user still learns the flag did nothing
	// on a run that then fails.
	if o.durationChanged {
		o.io.LogErrorf("%s --duration is ignored: the Docker credential helper exchanges a fresh token for every request, and --docker is the only tool this command configures.\n", o.io.Color().WarnIcon())
	}

	// Resolved once, then used everywhere: the token exchange must run against
	// the same host the domain is recorded under. Load-bearing rather than
	// belt-and-braces, since the client now comes from api.NewClientFromConfig,
	// which builds a client for whatever host it is handed and does not
	// substitute the default for an empty one the way f.ApiClient did.
	hostname := o.hostname
	if hostname == "" {
		hostname = o.defaultHostname
	}

	apiClient, err := o.apiClient(hostname)
	if err != nil {
		return err
	}

	// The token is exchanged only to confirm the exchange itself works, then
	// discarded: the Docker credential helper mints a fresh one on every
	// request. Without this, a login with no working credential would still
	// install the shim and write config, and the first sign of trouble would
	// be an unrelated-looking `docker pull` failure.
	//
	// MinDuration, not the server default: nothing ever sends this token, so
	// the shortest lifetime the server will issue verifies just as much while
	// leaving a live credential valid for a second instead of five minutes.
	if _, err := artifactregistry.NewClient(apiClient.Lab()).ExchangeToken(ctx, artifactregistry.MinDuration); err != nil {
		return o.exchangeError(hostname, err)
	}

	return loginDocker(o.io, o.cfg, hostname, o.registry)
}

// exchangeError reports a failed verification exchange, naming an absent stored
// credential as the cause when that is what it is.
//
// Worth two config reads on a path that is already failing: the exchange
// resolves credentials from the configuration file only (see NewCmd's client),
// so a bare "401 Unauthorized" is baffling to a user holding a GITLAB_TOKEN
// every other glab command accepts. The remedy is also not guessable from the
// status code, since `glab auth login` is what writes the file the Docker
// credential helper will read.
//
// Read failures add no hint: api.NewClientFromConfig would have failed on the
// same unreadable token before the exchange ran, so it cannot be the cause here.
func (o *options) exchangeError(hostname string, err error) error {
	wrapped := fmt.Errorf("failed to exchange an artifact registry token from %s; docker pull would hit the same failure: %w", hostname, err)

	// The environment is excluded, matching how the exchange's own client
	// resolved its credential: a token only the environment holds is not one
	// `docker pull` can ever use, so it must not silence the hint.
	stored, _, storedErr := o.cfg.GetWithSource(hostname, "token", false)
	if storedErr != nil || stored != "" {
		return wrapped
	}

	// job_token is read with the environment included, on purpose: CI_JOB_TOKEN
	// is how the Docker credential helper authenticates inside a CI job, so a
	// host holding only that one is configured, not unconfigured.
	jobToken, jobTokenErr := o.cfg.Get(hostname, "job_token")
	if jobTokenErr != nil || jobToken != "" {
		return wrapped
	}

	// No trailing period on either message: the CLI's error renderer appends one.
	if environment, source := config.GetFromEnvWithSource("token"); environment != "" {
		return fmt.Errorf("%w. %s is set, but nothing is stored for %s and neither this command nor the Docker credential helper reads the environment; run `glab auth login --hostname %s` to store a token", wrapped, source, hostname, hostname)
	}
	return fmt.Errorf("%w. No token is stored for %s; run `glab auth login --hostname %s` to store one", wrapped, hostname, hostname)
}
