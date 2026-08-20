// Package login implements `glab artifact-registry login`, which
// authenticates a package manager against the GitLab Artifact Registry.
package login

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"regexp"
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

// aliasInvalidCharsRe matches runs of characters that cannot appear in a
// registry alias, so they can be collapsed into a single "-".
var aliasInvalidCharsRe = regexp.MustCompile(`[^a-z0-9]+`)

// registryAliasRe restricts an explicit --registry-alias to characters that
// are always safe as Maven <id> XML text content and as a Gradle property
// key prefix. Unlike defaultRegistryAlias's derived value, a user-supplied
// alias is never sanitized before being written, so it must be validated
// instead.
var registryAliasRe = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// registryHostRe restricts a --registry host to what a host name can hold.
// url.Parse is far looser: it accepts '"', '(' and ')' there, and every writer
// interpolates the host into a file its tool parses. credentials.sbt is the
// sharpest case, since sbt compiles it as Scala: one '"' closes the string
// literal and the rest of the host lands in the file as code, so a registry
// like `https://x")+sys.error("boom")+Credentials("z/` becomes something sbt
// runs. The others fail more quietly, on a key their tool never looks up.
//
// An internationalized host has to be given in punycode. That is what the
// tools look for anyway: npm builds its .npmrc key with the WHATWG URL parser,
// which converts the host to punycode, so a UTF-8 host here would write a key
// npm never reads.
var registryHostRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

type options struct {
	io *iostreams.IOStreams
	// apiClient builds the client for an exchange whose token this command
	// writes into a package manager's own configuration, so far only --maven.
	// f.ApiClient, unlike credHelperApiClient: nothing re-resolves the
	// credential afterwards, so every credential glab accepts, GITLAB_TOKEN
	// included, works here.
	apiClient func(repoHost string) (*api.Client, error)
	// credHelperApiClient builds the client for the --docker verification
	// exchange, resolving the same identity the Docker credential helper will.
	// Deliberately not f.ApiClient: see its wiring in NewCmd. A field rather
	// than an inline call so a test can assert which host it is built for.
	credHelperApiClient func(repoHost string) (*api.Client, error)
	cfg                 config.Config
	defaultHostname     string
	// supportedOS reports whether this platform can run the Docker credential
	// helper shim. A field rather than a direct call so a test can exercise the
	// unsupported path on a supported platform.
	supportedOS func() error

	hostname string
	duration time.Duration
	// durationChanged is set from cmd.Flags().Changed("duration") before
	// validate/run are called, since a duration flag's zero value can't
	// distinguish "not passed" from "passed as 0s".
	durationChanged bool

	registry string
	// registryURL is registry parsed, set by validate on the --gradle/--npm/
	// --sbt paths and nil elsewhere. It travels from the check that rejects a
	// bad URL to the writers that key their entries on one, so no writer has to
	// parse the same string again and handle a failure that cannot happen.
	registryURL   *url.URL
	registryAlias string

	docker bool
	maven  bool
	gradle bool
	npm    bool
	sbt    bool
}

// NewCmd returns the `glab artifact-registry login` command.
func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
		// WithoutTokenFromEnvironment, so this client resolves the same identity
		// the Docker credential helper will
		// (internal/commands/auth/docker.Helper.getArtifactRegistryToken builds
		// its client the same way). f.ApiClient reads GITLAB_TOKEN, which the
		// helper ignores, so verifying with it would check an identity Docker
		// never uses: a valid GITLAB_TOKEN over a dead stored token reports
		// success and then 401s on every pull, and the reverse fails a login
		// that would have worked.
		credHelperApiClient: func(repoHost string) (*api.Client, error) {
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

			Use the flag for your package manager:

			- %[1]s--docker%[1]s: registers %[1]sglab%[1]s as a Docker credential
			  helper for the registry.
			- %[1]s--maven%[1]s: writes a %[1]s<server>%[1]s block in
			  %[1]s~/.m2/settings.xml%[1]s, keyed by %[1]s--registry-alias%[1]s.
			  Reference it from a %[1]s<repository>%[1]s carrying the same
			  %[1]s<id>%[1]s.
			- %[1]s--gradle%[1]s: writes %[1]s{alias}Url%[1]s,
			  %[1]s{alias}Username%[1]s, and %[1]s{alias}Password%[1]s in
			  %[1]s~/.gradle/gradle.properties%[1]s, where %[1]s{alias}%[1]s is
			  %[1]s--registry-alias%[1]s.
			- %[1]s--npm%[1]s: writes a %[1]s//{host}{path}/:_authToken%[1]s entry
			  in %[1]s~/.npmrc%[1]s. You still need to point npm at the registry, with a
			  %[1]sregistry=%[1]s or %[1]s@scope:registry=%[1]s line.
			- %[1]s--sbt%[1]s: writes a %[1]scredentials +=%[1]s line in
			  %[1]s~/.sbt/1.0/credentials.sbt%[1]s, which assumes a stock sbt 1.x.
			  An sbt that moved its global base, with
			  %[1]s-Dsbt.global.base%[1]s or a newer default, does not read that
			  file.

			Token lifetime:

			- %[1]s--docker%[1]s exchanges a fresh token on every pull or push,
			  so %[1]s--duration%[1]s does not apply.
			- Every other flag writes one token, and nothing refreshes it. Run
			  the command again before %[1]s--duration%[1]s elapses. The default
			  is 15 minutes and the maximum is 12 hours.

			Credential resolution:

			- Docker runs the credential helper as its own subprocess, which
			  reads your credentials from the configuration file and ignores
			  %[1]sGITLAB_TOKEN%[1]s. This command verifies the login the same
			  way, so run %[1]sglab auth login%[1]s first if no token is stored
			  for the host.
			- Every other flag does read %[1]sGITLAB_TOKEN%[1]s. %[1]sglab%[1]s
			  writes the token into each file itself, so the tool can read it
			  without fetching credentials itself.

			Registry and alias selection:

			- Use %[1]s--registry%[1]s only for a registry the Artifact Registry
			  actually backs. If you name a container registry here, it receives the
			  wrong token and the error only surfaces on the next pull.
			- %[1]s--registry-alias%[1]s applies to %[1]s--maven%[1]s and
			  %[1]s--gradle%[1]s only, because %[1]s--npm%[1]s and
			  %[1]s--sbt%[1]s key their entries on %[1]s--registry%[1]s itself.
			  For %[1]s--gradle%[1]s, use an alias that is a valid identifier
			  in your build script: the default is derived from the registry
			  host and contains hyphens, which Groovy cannot interpolate as
			  %[1]s${...}%[1]s.
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Configure Docker to authenticate against a registry
			glab artifact-registry login --docker --registry registry.example.com

			# Configure Maven to authenticate against a registry for two hours
			glab artifact-registry login --maven --registry https://ar.example.com --duration 2h

			# Configure Gradle to authenticate against a registry for two hours
			glab artifact-registry login --gradle --registry https://ar.example.com --duration 2h

			# Configure npm to authenticate against a registry for two hours
			glab artifact-registry login --npm --registry https://ar.example.com --duration 2h

			# Configure sbt to authenticate against a registry for two hours
			glab artifact-registry login --sbt --registry https://ar.example.com --duration 2h
		`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
			"help:environment": heredoc.Doc(`
				DOCKER_CONFIG: the directory holding Docker's config.json, written by --docker. Defaults to ~/.docker.
				GRADLE_USER_HOME: the directory holding gradle.properties, written by --gradle. Defaults to ~/.gradle.
				npm_config_userconfig: the .npmrc written by --npm. Defaults to ~/.npmrc.
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
	cmd.Flags().BoolVar(&opts.maven, "maven", false, "Configure Maven to authenticate against the registry. Writes to ~/.m2/settings.xml.")
	cmd.Flags().BoolVar(&opts.gradle, "gradle", false, "Configure Gradle to authenticate against the registry. Writes to ~/.gradle/gradle.properties.")
	cmd.Flags().BoolVar(&opts.npm, "npm", false, "Configure npm to authenticate against the registry. Writes to ~/.npmrc.")
	cmd.Flags().BoolVar(&opts.sbt, "sbt", false, "Configure sbt to authenticate against the registry. Writes to ~/.sbt/1.0/credentials.sbt.")
	cmd.MarkFlagsMutuallyExclusive("docker", "maven", "gradle", "npm", "sbt")
	cmd.MarkFlagsOneRequired("docker", "maven", "gradle", "npm", "sbt")

	cmd.Flags().StringVar(&opts.registry, "registry", "", "Registry to authenticate against. For --docker, a bare hostname; for others, typically a URL.")
	cobra.CheckErr(cmd.MarkFlagRequired("registry"))
	cmd.Flags().StringVar(&opts.registryAlias, "registry-alias", "", "Alias/ID to register the registry under (Maven/Gradle only). Defaults to a name derived from --registry.")
	// --duration is accepted and ignored on the --docker path, deliberately:
	// that credential helper exchanges its own token for every request, with its
	// own lifetime (artifactregistry.DockerHelperDuration), so there is no token
	// there whose validity a user could choose. Rejecting the flag instead would
	// break the uniform flag set the other tools use, and silently accepting it
	// would let a user believe they had shortened a token's life. So it is
	// accepted, and run() says it did nothing.
	cmd.Flags().DurationVar(&opts.duration, "duration", artifactregistry.DefaultDuration, "How long the exchanged token should remain valid. Ignored for --docker.")
	cmd.Flags().StringVar(&opts.hostname, "hostname", "", "GitLab hostname to request the token from. Defaults to the configured GitLab instance.")

	return cmd
}

// complete reads the state that lives on the parsed command rather than in a
// flag variable.
func (o *options) complete(cmd *cobra.Command) {
	o.durationChanged = cmd.Flags().Changed("duration")
}

// validate rejects everything that makes the login impossible, before any
// network call or write. The --registry rules split by tool: --docker needs a
// bare hostname because that is what a credential helper is handed, while
// --gradle, --npm and --sbt need a URL with a host because they write it, or a
// key derived from it, into their own files.
//
// Two kinds of error come out of it. A bad flag value is a
// *cmdutils.FlagError, naming the flag whose value the user has to change. The
// --docker environment checks below (platform, glab on PATH) return whatever
// dockercredhelper hands back, deliberately not a FlagError: no flag the user
// could retype fixes either one, so pointing at a flag would misdirect.
//
// No FlagError starts with the flag name, also deliberately. fang's error
// handler title-cases the first word of whatever it renders, which turns a
// leading "--docker=false" into "--Docker=False": a flag spelling that does not
// exist. Leading with "invalid" keeps the flag as the user typed it.
func (o *options) validate() error {
	// MarkFlagsOneRequired only checks that one of the tool flags was passed, so
	// --docker=false alone (all others left at their false default) reaches here
	// with no tool actually selected.
	if !o.docker && !o.maven && !o.gradle && !o.npm && !o.sbt {
		return &cmdutils.FlagError{Err: fmt.Errorf("invalid --docker, --maven, --gradle, --npm, and --sbt: all false leaves no tool selected; pass one of them as true")}
	}

	if o.registryAlias != "" && (o.maven || o.gradle) && !registryAliasRe.MatchString(o.registryAlias) {
		return &cmdutils.FlagError{Err: fmt.Errorf("invalid --registry-alias: must contain only letters, digits, '.', '_', and '-' (got %q)", o.registryAlias)}
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

	if o.docker {
		if strings.Contains(o.registry, "://") {
			return &cmdutils.FlagError{Err: fmt.Errorf("invalid --registry: must be a bare hostname when using --docker (got %q); Docker credential helpers receive bare hostnames, not URLs", o.registry)}
		}
		if strings.Contains(o.registry, "/") {
			return &cmdutils.FlagError{Err: fmt.Errorf("invalid --registry: must not contain a path (got %q); Docker matches credHelpers entries on the host, optionally with a port, so a path would never match", o.registry)}
		}
		if strings.Contains(o.registry, ",") {
			return &cmdutils.FlagError{Err: fmt.Errorf("invalid --registry: must not contain a comma (got %q); artifact_registry_domains stores registries as a comma-separated list", o.registry)}
		}

		// Last, after the flag rules: a user who typed a bad --registry should
		// hear about the flag they can fix, not about their platform.
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
		// The shim is written next to glab and shells out to it, so a glab that
		// PATH cannot resolve is as fatal as an unsupported platform. Install
		// repeats the lookup, which is cheap and keeps it safe for callers that
		// skip this check.
		if _, err := dockercredhelper.Locate(); err != nil {
			return err
		}

		// --duration is not range-checked: nothing on this path sends it, so
		// rejecting a value run() has already said it ignores would contradict
		// the warning.
		return nil
	}

	// --gradle joins --npm and --sbt: all three put the registry URL, or a key
	// derived from it, into the file they write, so a value that is not a URL
	// only fails later at build time. --maven is the exception, and deliberately
	// so: its <server> block carries just the alias and the credentials, and the
	// URL itself lives in the caller's pom.xml keyed by that alias.
	//
	// The scheme is required as well as the host, and not just because the
	// message says so: url.Parse gives "//ar.example.com" a host and no scheme,
	// and --gradle writes that value verbatim into {alias}Url, where Gradle
	// rejects it as a repository URL at build time.
	if o.gradle || o.npm || o.sbt {
		u, err := url.Parse(o.registry)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return &cmdutils.FlagError{Err: fmt.Errorf("invalid --registry: must be a URL with a scheme and host, for example https://registry.example.com (got %q)", o.registry)}
		}
		// Hostname(), so an IPv6 literal arrives without its brackets and the
		// port stays out of the check: url.Parse has already refused a port
		// that is not numeric.
		host := u.Hostname()
		if net.ParseIP(host) == nil && !registryHostRe.MatchString(host) {
			return &cmdutils.FlagError{Err: fmt.Errorf("invalid --registry: its host must be an IP address, or contain only letters, digits, '.', '_' and '-' (got %q); an internationalized host must be given in punycode", host)}
		}

		// Kept, not thrown away: loginNpm and loginSbt both need the parsed
		// URL, and parsing it a second time there would give them an error
		// branch that this check has already ruled out.
		o.registryURL = u
	}

	return artifactregistry.ValidateDuration(o.duration)
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
	if o.registryAlias != "" && !o.maven && !o.gradle {
		o.io.LogErrorf("%s --registry-alias is ignored outside --maven/--gradle logins.\n", o.io.Color().WarnIcon())
	}

	// Resolved once, then used everywhere: the token exchange must run against
	// the same host the domain is recorded under. Load-bearing rather than
	// belt-and-braces on the --docker path, since credHelperApiClient comes
	// from api.NewClientFromConfig, which builds a client for whatever host it
	// is handed and does not substitute the default for an empty one the way
	// f.ApiClient does.
	hostname := o.hostname
	if hostname == "" {
		hostname = o.defaultHostname
	}

	if o.docker {
		// Warned before the exchange, so the user still learns the flag did
		// nothing on a run that then fails.
		if o.durationChanged {
			o.io.LogErrorf("%s --duration is ignored for --docker logins: the Docker credential helper exchanges a fresh token for every request.\n", o.io.Color().WarnIcon())
		}

		apiClient, err := o.credHelperApiClient(hostname)
		if err != nil {
			return err
		}

		// The token is exchanged only to confirm the exchange itself works,
		// then discarded: the Docker credential helper mints a fresh one on
		// every request. Without this, a login with no working credential
		// would still install the shim and write config, and the first sign
		// of trouble would be an unrelated-looking `docker pull` failure.
		//
		// MinDuration, not the server default: nothing ever sends this token, so
		// the shortest lifetime the server will issue verifies just as much while
		// leaving a live credential valid for a second instead of five minutes.
		if _, err := artifactregistry.NewClient(apiClient.Lab()).ExchangeToken(ctx, artifactregistry.MinDuration); err != nil {
			return o.exchangeError(hostname, err)
		}

		return loginDocker(o.io, o.cfg, hostname, o.registry)
	}

	alias := o.registryAlias
	if alias == "" {
		alias = defaultRegistryAlias(o.registry)
	}

	apiClient, err := o.apiClient(hostname)
	if err != nil {
		return err
	}

	result, err := artifactregistry.NewClient(apiClient.Lab()).ExchangeToken(ctx, o.duration)
	if err != nil {
		return fmt.Errorf("failed to get artifact registry token: %w", err)
	}

	switch {
	case o.maven:
		if err := loginMaven(alias, result.Token); err != nil {
			return err
		}
		o.io.LogInfof("%s Configured Maven server %q for %s. Token expires at %s.\n", o.io.Color().GreenCheck(), alias, o.registry, result.ExpiresAt.Format(time.RFC3339))
	case o.gradle:
		if err := loginGradle(o.registry, alias, result.Token); err != nil {
			return err
		}
		// Names the three property keys, not just the file: the alias is what the
		// user has to type into build.gradle, and it is the one writer whose
		// output is unusable without knowing it.
		o.io.LogInfof("%s Configured Gradle properties %sUrl, %sUsername and %sPassword for %s. Token expires at %s.\n",
			o.io.Color().GreenCheck(), alias, alias, alias, o.registry, result.ExpiresAt.Format(time.RFC3339))
	case o.npm:
		if err := loginNpm(o.registryURL, result.Token); err != nil {
			return err
		}
		// Says "auth token", not "configured npm": all this writes is the
		// credential. npm still resolves packages from its default registry
		// until the user points it at this one, so a message claiming npm was
		// configured would be describing a step the user still has to take.
		o.io.LogInfof("%s Wrote an npm auth token for %s. Point npm at that registry with a `registry=` or `@scope:registry=` line if you have not already. Token expires at %s.\n",
			o.io.Color().GreenCheck(), o.registry, result.ExpiresAt.Format(time.RFC3339))
	case o.sbt:
		if err := loginSbt(o.registryURL, result.Token); err != nil {
			return err
		}
		o.io.LogInfof("%s Configured sbt for %s. Token expires at %s.\n", o.io.Color().GreenCheck(), o.registry, result.ExpiresAt.Format(time.RFC3339))
	}

	return nil
}

// defaultRegistryAlias derives a default alias/ID from registry, which may
// be a bare hostname or a full URL. The result is always prefixed with
// "artifact-registry-" and contains only lowercase alphanumerics and single
// hyphens between runs of other characters.
func defaultRegistryAlias(registry string) string {
	host := registry
	if u, err := url.Parse(registry); err == nil && u.Host != "" {
		host = u.Host
	}

	host = strings.ToLower(host)
	host = aliasInvalidCharsRe.ReplaceAllString(host, "-")
	host = strings.Trim(host, "-")

	return "artifact-registry-" + host
}

// exchangeError reports a failed verification exchange, naming an absent stored
// credential as the cause when that is what it is.
//
// Worth two config reads on a path that is already failing: the exchange
// resolves credentials from the configuration file only (see
// credHelperApiClient's wiring in NewCmd), so a bare "401 Unauthorized" is
// baffling to a user holding a GITLAB_TOKEN every other glab command accepts.
// The remedy is also not guessable from the status code, since
// `glab auth login` is what writes the file the Docker credential helper reads.
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
