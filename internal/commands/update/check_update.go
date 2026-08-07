package update

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/hashicorp/go-version"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

const (
	defaultProjectURL = "https://gitlab.com/gitlab-org/cli"
	commandUse        = "check-update"
)

var commandAliases = []string{"update"}

func NewCheckUpdateCmd(f cmdutils.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   commandUse,
		Short: "Check for the latest glab version.",
		Long: heredoc.Docf(`
		Checks for the latest version of glab available on GitLab.com.

		When you run this command explicitly, glab always checks for updates,
		even if the previous check was less than 24 hours ago.

		When glab runs this check automatically after other commands, it
		checks for updates at most once every 24 hours.

		To turn off the automatic update check, run
		%[1]sglab config set check_update false%[1]s. To turn it back on,
		run %[1]sglab config set check_update true%[1]s.
		`, "`"),
		Example: heredoc.Doc(`
		# Check for the latest glab version
		glab check-update

		# Check for the latest glab version using the alias
		glab update
		`),
		Aliases: commandAliases,
		Args:    cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return CheckUpdateExplicit(f)
		},
	}

	return cmd
}

// clientCreator is a variable that can be overridden for testing
var clientCreator = CreateUnauthenticatedClient

// installMethodDetector is overridable for tests so we don't depend on the
// filesystem path of the running test binary.
var installMethodDetector = DetectInstallMethod

func CheckUpdate(f cmdutils.Factory, silentSuccess bool) error {
	return checkUpdate(f, silentSuccess, false)
}

// CheckUpdateExplicit performs an update check when explicitly invoked by the user.
// Unlike automatic checks, this bypasses the 24-hour throttle.
func CheckUpdateExplicit(f cmdutils.Factory) error {
	return checkUpdate(f, false, true)
}

func checkUpdate(f cmdutils.Factory, silentSuccess bool, forceCheck bool) error {
	moreThan24hAgo, err := checkLastUpdate(f, forceCheck)
	if err != nil {
		return err
	}
	// if the last update check was less than 24h ago we skip the version check
	// (unless this is a forced check from explicit command invocation)
	if !moreThan24hAgo {
		return nil
	}

	// Create an unauthenticated API client to check for updates on the public gitlab.com/gitlab-org/cli project.
	// We explicitly avoid using user credentials since:
	// 1. The releases endpoint is public and doesn't require authentication
	// 2. Using user credentials (especially from GITLAB_TOKEN env var) can cause issues
	//    when users have tokens for self-hosted instances that aren't valid for gitlab.com
	apiClient, err := clientCreator(f.BuildInfo().UserAgent())
	if err != nil {
		return err
	}
	gitlabClient := apiClient.Lab()

	releases, _, err := gitlabClient.Releases.ListReleases(
		"gitlab-org/cli", &gitlab.ListReleasesOptions{ListOptions: gitlab.ListOptions{Page: 1, PerPage: 1}})
	if err != nil {
		return fmt.Errorf("failed checking for glab updates: %s", err.Error())
	}
	if len(releases) < 1 {
		return errors.New("no release found for glab")
	}
	latestRelease := releases[0]
	releaseURL := fmt.Sprintf("%s/-/releases/%s", defaultProjectURL, latestRelease.TagName)

	buildInfo := f.BuildInfo()
	version := buildInfo.Version

	if isOlderVersion(latestRelease.Name, version) {
		writeUpdateAvailable(f.IO(), version, latestRelease.TagName, releaseURL, installMethodDetector(), buildInfo.CodingAgent)
	} else if !silentSuccess {
		c := f.IO().Color()
		f.IO().LogErrorf("%v",
			c.Green("You are already using the latest version of glab!\n"))
	}

	// Piggybacks on the 24h throttle so we don't issue one gitlab.com
	// request per installed remote skill on every command.
	writeSkillUpdateBlock(f.IO(), remoteSkillUpdates(f.Config()))
	return nil
}

// writeUpdateAvailable renders the "update available" nudge to stderr. When a
// coding agent is detected it emits a single bracketed line phrased as a
// suggestion the agent can relay; otherwise it emits a multi-line, colored
// notice for humans. When the install method is unknown the upgrade-command
// segments are dropped and only the release-notes URL is shown.
func writeUpdateAvailable(io *iostreams.IOStreams, currentVersion, latestVersion, releaseURL string, method InstallMethod, codingAgent string) {
	if codingAgent != "" {
		writeAgentUpdateLine(io, currentVersion, latestVersion, releaseURL, method)
		return
	}
	writeHumanUpdateBlock(io, currentVersion, latestVersion, releaseURL, method)
}

func writeAgentUpdateLine(io *iostreams.IOStreams, currentVersion, latestVersion, releaseURL string, method InstallMethod) {
	var b strings.Builder
	fmt.Fprintf(&b, "[glab] Update available: %s → %s", currentVersion, latestVersion) //nolint:forbidigo // writing to strings.Builder, not stdout/stderr
	if method.UpgradeCommand != "" {
		fmt.Fprintf(&b, " (installed via %s). Suggested upgrade command: `%s`.", method.Name, method.UpgradeCommand) //nolint:forbidigo // writing to strings.Builder, not stdout/stderr
	} else {
		b.WriteString(".")
	}
	fmt.Fprintf(&b, " Release notes: %s\n", releaseURL) //nolint:forbidigo // writing to strings.Builder, not stdout/stderr
	io.LogErrorf("%s", b.String())
}

func writeHumanUpdateBlock(io *iostreams.IOStreams, currentVersion, latestVersion, releaseURL string, method InstallMethod) {
	c := io.Color()
	// Leading blank line separates the banner from the preceding command
	// output so the nudge doesn't cram against e.g. `glab mr list` results.
	io.LogError("")
	io.LogError(c.Yellow("A new version of glab is available"))
	io.LogErrorf("  %s → %s\n", c.Red(currentVersion), c.Green(latestVersion))
	if method.UpgradeCommand != "" {
		io.LogErrorf("  Run: %s\n", method.UpgradeCommand)
	}
	io.LogErrorf("  Release notes: %s\n", releaseURL)
}

// CreateUnauthenticatedClient creates an API client without authentication for accessing
// public endpoints on gitlab.com. This avoids issues where user credentials (especially
// from environment variables like GITLAB_TOKEN) might be for self-hosted instances
// and invalid for gitlab.com.
func CreateUnauthenticatedClient(userAgent string, options ...api.ClientOption) (*api.Client, error) {
	// Create a client with an empty token for unauthenticated requests
	opts := []api.ClientOption{
		api.WithBaseURL(glinstance.APIEndpoint(glinstance.DefaultHostname, glinstance.DefaultProtocol, "", "")),
		api.WithUserAgent(userAgent),
	}
	opts = append(opts, options...)

	return api.NewClient(
		func(c *http.Client) (gitlab.AuthSource, error) {
			// Use AccessTokenAuthSource with empty token for public API access
			return gitlab.AccessTokenAuthSource{Token: ""}, nil
		},
		opts...,
	)
}

// ShouldSkipUpdate decides whether to suppress the post-command update
// banner, "what's new" nudge, and skill-update check.
//
// previousCommand is the raw first argument from
// `expand.ExpandAlias(cfg, os.Args, nil)`, i.e. `os.Args[1]` after alias
// expansion — not a parsed `cobra.Command.Name()`. That means flag forms
// like `-v` and `--version` arrive verbatim and can be matched here
// (see cmd/glab/main.go where `argCommand := expandedArgs[0]` is the
// value passed in).
//
// Skip reasons:
//   - check-update / update: would re-run the check we just ran, and
//     adds latency to new shells if users set `check_update=false` to
//     avoid it.
//   - completion: same shell-start latency concern.
//   - git-credential / credential-helper: would interfere with Git's
//     credential protocol on stderr.
//   - whatsnew: the user just read the changelog; don't pitch it back.
//   - version / -v / --version: the user just asked "what version do I
//     have?" — trailing the answer with a "what's new" and skill-updates
//     pitch is noise, and also keeps `2>&1`-style automation clean.
func ShouldSkipUpdate(previousCommand string) bool {
	isCheckUpdate := previousCommand == commandUse || utils.PresentInStringSlice(commandAliases, previousCommand)
	isCompletion := previousCommand == "completion"
	isGitCredential := previousCommand == "git-credential"
	isCredentialHelper := previousCommand == "credential-helper"
	isWhatsNew := previousCommand == "whatsnew"
	isVersion := previousCommand == "version" || previousCommand == "-v" || previousCommand == "--version"

	return isCheckUpdate || isCompletion || isGitCredential || isCredentialHelper || isWhatsNew || isVersion
}

func isOlderVersion(latestVersion, appVersion string) bool {
	latestVersion = strings.TrimSpace(latestVersion)
	appVersion = strings.TrimSpace(appVersion)

	vv, ve := version.NewVersion(latestVersion)
	vw, we := version.NewVersion(appVersion)

	return ve == nil && we == nil && vv.GreaterThan(vw)
}

// returns true if we should check for updates
//
// returns false if we should skip the update check
//
// We only want to check for updates once every 24 hours, unless forceCheck is true
func checkLastUpdate(f cmdutils.Factory, forceCheck bool) (bool, error) {
	const updateCheckInterval = 24 * time.Hour
	cfg := f.Config()

	// We don't care when the command was run if the environment variable is forcing an update
	// or if this is an explicit command invocation (forceCheck = true)
	if isEnvForcingUpdate() || forceCheck {
		if err := updateLastCheckTimestamp(cfg); err != nil {
			return false, err
		}
		return true, nil
	}

	last_update, err := cfg.Get("", "last_update_check_timestamp")
	if err != nil {
		return false, err
	}

	// this might be the first time running the command, so last_update might be empty
	// we want to save the current time and check for an update
	if last_update == "" {
		if err := updateLastCheckTimestamp(cfg); err != nil {
			return false, err
		}
		return true, nil
	}

	last_update_time, err := time.Parse(time.RFC3339, last_update)
	if err != nil {
		return false, err
	}

	// if the last check was more than 24h ago we check for an update
	moreThan24hAgo := time.Since(last_update_time) > updateCheckInterval
	if moreThan24hAgo {
		if err := updateLastCheckTimestamp(cfg); err != nil {
			return false, err
		}
	}
	return moreThan24hAgo, nil
}

// isEnvForcingUpdate - returns true if the environment variable `GLAB_CHECK_UPDATE` is set to true
func isEnvForcingUpdate() bool {
	if envVal, ok := os.LookupEnv("GLAB_CHECK_UPDATE"); ok {
		switch strings.ToUpper(envVal) {
		case "TRUE", "YES", "Y", "1":
			return true
		case "FALSE", "NO", "N", "0":
			return false
		}
	}
	// if the value is not set or is not a valid value
	return false
}

// updateLastCheckTimestamp - saves the current time as last_update_check_timestamp to config.yml
func updateLastCheckTimestamp(cfg config.Config) error {
	if err := cfg.Set("", "last_update_check_timestamp", time.Now().Format(time.RFC3339)); err != nil {
		return err
	}

	if err := cfg.Write(); err != nil {
		return err
	}

	return nil
}

// PrintUpdateError prints update check errors with helpful formatting and context.
// This is specifically for errors that occur during background update checks,
// not main command execution errors.
func PrintUpdateError(streams *iostreams.IOStreams, err error, cmd *cobra.Command, debug bool) {
	color := streams.Color()

	var dnsError *net.DNSError
	if errors.As(err, &dnsError) {
		streams.LogErrorf("%s error connecting to %s\n", color.FailedIcon(), dnsError.Name)
		if debug {
			streams.LogError(color.FailedIcon(), dnsError)
		}
		streams.LogInfof("%s Check your internet connection and status.gitlab.com. If on GitLab Self-Managed, run 'sudo gitlab-ctl status' on your server.\n", color.DotWarnIcon())
	} else {
		var exitError *cmdutils.ExitError
		if errors.As(err, &exitError) {
			streams.LogErrorf("%s %s %s=%s\n", color.FailedIcon(), color.Bold(exitError.Details), color.Red("error"), exitError.Err)
		} else {
			streams.LogError("ERROR:", err)

			var flagError *cmdutils.FlagError
			if errors.As(err, &flagError) || strings.HasPrefix(err.Error(), "unknown command ") {
				if cmd != nil {
					streams.LogInfof("Try '%s --help' for more information.", cmd.CommandPath())
				} else {
					streams.LogInfof("Try --help for more information.")
				}
			}
		}
	}

	if cmd != nil {
		streams.LogError()
	}
}
