package main

import (
	"context"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	apiCmd "gitlab.com/gitlab-org/cli/internal/commands/api"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

// telemetrySendTimeout bounds how long glab delays its own exit for the event.
const telemetrySendTimeout = 2 * time.Second

// noRetry stops the client's five default retries turning an instant failure,
// such as a refused connection, into the full timeout.
func noRetry(ctx context.Context, _ *http.Response, _ error) (bool, error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	return false, nil
}

func telemetryRequestOptions(ctx context.Context) []gitlab.RequestOptionFunc {
	return []gitlab.RequestOptionFunc{
		gitlab.WithContext(ctx),
		gitlab.WithRequestRetry(noRetry),
	}
}

// telemetrySkippedCommands run from shell startup files and from git, not from
// a person, so they must not block on the network.
var telemetrySkippedCommands = map[string]struct{}{
	"completion":             {},
	"auth git-credential":    {},
	"auth credential-helper": {},
}

func addTelemetryHook(f cmdutils.Factory, cmd *cobra.Command) func() {
	return func() {
		sendTelemetryData(f, cmd)
	}
}

// isTelemetryEnabled checks if usage data is disabled via config or env var
func isTelemetryEnabled(cfg config.Config) bool {
	if enabled, found := utils.IsEnvVarEnabled("GLAB_SEND_TELEMETRY"); found {
		return enabled
	}

	// Fall back to config value if env var not set
	if telemetryEnabled, _ := cfg.Get("", "telemetry"); telemetryEnabled != "" {
		if telemetryEnabledParsed, err := strconv.ParseBool(telemetryEnabled); err == nil {
			return telemetryEnabledParsed
		}
	}

	return true
}

// parseCommand parses a command string and returns components
func parseCommand(parts []string) (string, string, string) {
	if len(parts) < 2 {
		return "", "", ""
	}

	// glab is always the first value, command is the next
	command := parts[1]

	subcommandParts := parts[2:]
	subcommand := strings.Join(subcommandParts, " ")

	fullCommand := command
	if subcommand != "" {
		fullCommand += " " + subcommand
	}

	return command, subcommand, fullCommand
}

func sendTelemetryData(f cmdutils.Factory, cmd *cobra.Command) {
	if cmd == nil {
		return
	}

	command, subcommand, fullCommand := parseCommand(strings.Split(cmd.CommandPath(), " "))
	if _, skip := telemetrySkippedCommands[fullCommand]; skip {
		dbg.Debug("Telemetry skipped for machine-invoked command: ", fullCommand)
		return
	}

	client, event, ok := buildTelemetryEvent(f, command, subcommand, fullCommand)
	if !ok {
		return
	}
	maps.Copy(event.AdditionalProperties, apiEventProperties(cmd))

	// Not cmd.Context(): that is already cancelled if the command was interrupted.
	ctx, cancel := context.WithTimeout(context.Background(), telemetrySendTimeout)
	defer cancel()

	if err := trackEvent(client, event, telemetryRequestOptions(ctx)...); err != nil {
		dbg.Debug("Could not send telemetry data: ", err.Error())
	}
}

// trackEventOptions is gitlab.TrackEventOptions plus project_path, which
// client-go does not model yet. Sending the path lets the instance resolve the
// project and namespace IDs, which saves glab an API call of its own.
type trackEventOptions struct {
	Event                string            `json:"event"`
	SendToSnowplow       *bool             `json:"send_to_snowplow,omitempty"`
	ProjectPath          string            `json:"project_path,omitempty"`
	AdditionalProperties map[string]string `json:"additional_properties,omitempty"`
}

func trackEvent(client *gitlab.Client, event *trackEventOptions, options ...gitlab.RequestOptionFunc) error {
	req, err := client.NewRequest(http.MethodPost, "usage_data/track_event", event, options)
	if err != nil {
		return err
	}

	_, err = client.Do(req, nil)
	return err
}

// Reported instead of the path when no route matches, so an unrecognised path
// is discarded whole rather than partially redacted.
const unmatchedEndpoint = "unmatched"

func apiEventProperties(cmd *cobra.Command) map[string]string {
	if cmd.Name() != "api" {
		return nil
	}

	args := cmd.Flags().Args()
	if len(args) == 0 {
		return nil
	}

	return map[string]string{
		"api_endpoint": templateEndpoint(args[0]),
		"http_method":  apiCmd.EffectiveMethod(cmd),
	}
}

// templateEndpoint reduces a request path to the matching route template.
// Identifiers are not distinguishable from route nouns by shape, so the path is
// matched rather than rewritten: "users/phikai/projects" must not keep the
// username.
func templateEndpoint(path string) string {
	// Query strings and fragments carry identifiers and sometimes tokens.
	path, _, _ = strings.Cut(path, "?")
	path, _, _ = strings.Cut(path, "#")

	// client-go reaches GraphQL through its own client, so it has no route.
	if strings.Trim(path, "/") == "graphql" {
		return "/graphql"
	}

	if route, ok := gitlab.MatchRoute(path); ok {
		return route.String()
	}
	return unmatchedEndpoint
}

// buildTelemetryEvent assembles the event without making a request of its own.
func buildTelemetryEvent(f cmdutils.Factory, command, subcommand, fullCommand string) (*gitlab.Client, *trackEventOptions, bool) {
	var projectPath string
	var client *gitlab.Client

	repo, err := f.BaseRepo()
	if err != nil {
		dbg.Debug("Could not determine base repo in telemetry hook: ", err.Error())

		c, err := f.ApiClient("")
		if err != nil {
			dbg.Debug("Could not get API client in telemetry hook: ", err.Error())
			return nil, nil, false
		}
		client = c.Lab()
	} else {
		c, err := f.GitLabClient()
		if err != nil {
			dbg.Debug("Could not get API client in telemetry hook: ", err.Error())
			return nil, nil, false
		}
		client = c
		projectPath = repo.FullName()
	}

	buildInfo := f.BuildInfo()
	properties := map[string]string{
		"label":                  command,
		"property":               subcommand,
		"command_and_subcommand": fullCommand,
		"cli_version":            buildInfo.Version,
		"platform":               buildInfo.Platform,
		"architecture":           buildInfo.Architecture,
	}
	// Absent rather than empty, so "no agent" and "client too old to report
	// one" stay distinguishable via cli_version.
	if buildInfo.CodingAgent != "" {
		properties["coding_agent"] = buildInfo.CodingAgent
	}

	return client, &trackEventOptions{
		Event:                "gitlab_cli_command_used",
		ProjectPath:          projectPath,
		SendToSnowplow:       new(true),
		AdditionalProperties: properties,
	}, true
}
