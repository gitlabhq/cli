package doctor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/claudehooks"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/fallbacksync"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type checkResult struct {
	name    string
	ok      bool
	message string
	fix     string
}

type options struct {
	io        *iostreams.IOStreams
	config    func() config.Config
	apiClient func(repoHost string) (*api.Client, error)
	executor  cmdutils.Executor
	hostname  string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		config:    f.Config,
		apiClient: f.ApiClient,
		executor:  f.Executor(),
		hostname:  f.DefaultHostname(),
	}

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose AI agent governance configuration, authentication, and hook setup. (EXPERIMENTAL)",
		Long: heredoc.Doc(`
			Check that AI agent governance is correctly configured on this machine.

			Verifies:

			- Authentication: glab is authenticated with a valid token
			- glab in PATH: the binary is findable so hooks will work
			- Claude Code hooks: Stop and SessionEnd hooks are installed
			- API connectivity: can reach the GitLab API
			- Fallback periodic sync: whether the scheduled job is installed and loaded, whether the glab binary it runs still exists, and the result of its last run

			Outputs a clear remediation command for any check that fails.
		`) + text.ExperimentalString,
		Example: heredoc.Doc(`
		    # Check AI agent governance configuration on this machine
		    $ glab govern doctor
		`),
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor(cmd.Context(), opts)
		},
	}

	return cmd
}

func runDoctor(ctx context.Context, opts *options) error {
	io := opts.io
	c := io.Color()

	checks := []checkResult{
		checkGlabInPath(),
		checkAuthentication(opts),
		checkClaudeHooks(),
		checkAPIConnectivity(ctx, opts),
		checkFallbackSync(ctx, opts.executor, runtime.GOOS, time.Now()),
	}

	allPassed := true
	for _, check := range checks {
		if check.ok {
			io.LogInfof("%s %s: %s\n", c.GreenCheck(), check.name, check.message)
		} else {
			allPassed = false
			io.LogInfof("%s %s: %s\n", c.FailedIcon(), check.name, check.message)

			if check.fix != "" {
				io.LogInfof("   Fix: %s\n", check.fix)
			}
		}
	}

	io.LogInfo("")
	if allPassed {
		io.LogInfof("%s All checks passed.\n", c.GreenCheck())
	} else {
		io.LogInfof("%s Some checks failed. Address the issues above and re-run 'glab govern doctor'.\n", c.FailedIcon())

		return cmdutils.SilentError
	}

	return nil
}

func checkGlabInPath() checkResult {
	path, err := exec.LookPath("glab")
	if err != nil {
		return checkResult{
			name:    "glab in PATH",
			ok:      false,
			message: "glab binary not found in PATH -- hooks will not fire",
			fix:     "Add glab to your PATH. See https://gitlab.com/gitlab-org/cli#installation",
		}
	}
	return checkResult{
		name:    "glab in PATH",
		ok:      true,
		message: path,
	}
}

func checkAuthentication(opts *options) checkResult {
	cfg := opts.config()
	hostname := opts.hostname
	if hostname == "" {
		hostname = glinstance.DefaultHostname
	}

	token, _, err := cfg.GetWithSource(hostname, "token", true)
	if err != nil || token == "" {
		return checkResult{
			name:    "Authentication",
			ok:      false,
			message: fmt.Sprintf("not authenticated with %s", hostname),
			fix:     "Run: glab auth login",
		}
	}

	return checkResult{
		name:    "Authentication",
		ok:      true,
		message: fmt.Sprintf("authenticated with %s", hostname),
	}
}

func checkClaudeHooks() checkResult {
	home, err := os.UserHomeDir()
	if err != nil {
		return checkResult{
			name:    "Claude Code hooks",
			ok:      false,
			message: fmt.Sprintf("could not determine home directory: %v", err),
		}
	}

	claudeDir := filepath.Join(home, ".claude")
	if _, err := os.Stat(claudeDir); errors.Is(err, fs.ErrNotExist) {
		return checkResult{
			name:    "Claude Code hooks",
			ok:      true,
			message: "Claude Code not detected (~/.claude not found); run 'glab govern setup' after installing Claude Code",
		}
	}

	path, err := claudehooks.SettingsPath()
	if err != nil {
		return checkResult{
			name:    "Claude Code hooks",
			ok:      false,
			message: fmt.Sprintf("could not determine home directory: %v", err),
		}
	}

	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return checkResult{
			name:    "Claude Code hooks",
			ok:      false,
			message: fmt.Sprintf("%s not found", claudehooks.SettingsFile),
			fix:     "Run: glab govern setup",
		}
	}

	raw, err := claudehooks.LoadRawSettings(path)
	if err != nil {
		return checkResult{
			name:    "Claude Code hooks",
			ok:      false,
			message: fmt.Sprintf("could not parse %s", claudehooks.SettingsFile),
			fix:     "Run: glab govern setup",
		}
	}

	hooks, err := claudehooks.HooksFromRaw(raw)
	if err != nil {
		return checkResult{
			name:    "Claude Code hooks",
			ok:      false,
			message: fmt.Sprintf("could not parse hooks in %s", claudehooks.SettingsFile),
			fix:     "Run: glab govern setup",
		}
	}

	missingHooks := []string{}
	if !claudehooks.HookPresent(hooks, "Stop", claudehooks.StopHookCommand) {
		missingHooks = append(missingHooks, "Stop")
	}
	if !claudehooks.HookPresent(hooks, "SessionEnd", claudehooks.SessionEndHookCommand) {
		missingHooks = append(missingHooks, "SessionEnd")
	}

	if len(missingHooks) > 0 {
		return checkResult{
			name:    "Claude Code hooks",
			ok:      false,
			message: fmt.Sprintf("missing hooks: %s", strings.Join(missingHooks, ", ")),
			fix:     "Run: glab govern setup",
		}
	}

	return checkResult{
		name:    "Claude Code hooks",
		ok:      true,
		message: "Stop and SessionEnd hooks installed",
	}
}

func checkAPIConnectivity(ctx context.Context, opts *options) checkResult {
	client, err := opts.apiClient(opts.hostname)
	if err != nil {
		return checkResult{
			name:    "API connectivity",
			ok:      false,
			message: fmt.Sprintf("could not create API client: %v", err),
			fix:     "Run: glab auth login",
		}
	}

	_, _, err = client.Lab().Metadata.GetMetadata(gitlab.WithContext(ctx))
	if err != nil {
		return checkResult{
			name:    "API connectivity",
			ok:      false,
			message: fmt.Sprintf("could not reach %s: %v", opts.hostname, err),
			fix:     "Check your network connection and GitLab instance URL",
		}
	}

	return checkResult{
		name:    "API connectivity",
		ok:      true,
		message: fmt.Sprintf("connected to %s", opts.hostname),
	}
}

func checkFallbackSync(ctx context.Context, executor cmdutils.Executor, goos string, now time.Time) checkResult {
	const name = "Fallback periodic sync"

	home, err := os.UserHomeDir()
	if err != nil {
		return checkResult{name: name, ok: false, message: fmt.Sprintf("could not determine home directory: %v", err)}
	}

	glabPath, err := fallbacksync.InstalledBinary(goos, home)
	switch {
	case errors.Is(err, fallbacksync.ErrUnsupportedOS):
		return checkResult{
			name:    name,
			ok:      true,
			message: fmt.Sprintf("not supported on %s; sessions sync only through the hooks", goos),
		}
	case errors.Is(err, fallbacksync.ErrNotInstalled):
		return checkResult{
			name:    name,
			ok:      true,
			message: "not installed; sessions sync only through the hooks. Run 'glab govern setup' to install it",
		}
	case err != nil:
		return checkResult{
			name:    name,
			ok:      false,
			message: fmt.Sprintf("could not read the installed job: %v", err),
			fix:     "Run: glab govern setup",
		}
	}

	if _, err := os.Stat(glabPath); err != nil {
		return checkResult{
			name:    name,
			ok:      false,
			message: fmt.Sprintf("the job runs %s, which no longer exists", glabPath),
			fix:     "Run: glab govern setup",
		}
	}

	state, err := fallbacksync.State(ctx, executor, goos, os.Getuid())
	switch {
	case err != nil:
		return checkResult{
			name:    name,
			ok:      false,
			message: fmt.Sprintf("could not check whether the job is loaded: %v", err),
			fix:     "Run: glab govern setup",
		}
	case !state.Loaded:
		return checkResult{
			name:    name,
			ok:      false,
			message: "the job is installed but not loaded, so it never runs",
			fix:     "Run: glab govern setup",
		}
	case state.Ran && state.ExitCode != 0:
		return checkResult{
			name:    name,
			ok:      false,
			message: fmt.Sprintf("the job's last run exited with code %d", state.ExitCode),
			fix:     fmt.Sprintf("Run '%s govern audit sync --all' to see the error, then 'glab govern setup' to reinstall the job", glabPath),
		}
	}

	status, err := fallbacksync.ReadStatus()
	switch {
	case errors.Is(err, fallbacksync.ErrNoStatus):
		return checkResult{name: name, ok: true, message: fmt.Sprintf("installed (%s); no run recorded yet", glabPath)}
	case err != nil:
		return checkResult{name: name, ok: true, message: fmt.Sprintf("installed (%s); could not read last run: %v", glabPath, err)}
	}

	return checkResult{
		name:    name,
		ok:      true,
		message: fmt.Sprintf("installed (%s); %s", glabPath, describeLastRun(status, now)),
	}
}

func describeLastRun(s *fallbacksync.Status, now time.Time) string {
	ago := now.Sub(s.FinishedAt).Round(time.Minute)
	desc := fmt.Sprintf("last run %s ago", ago)
	if ago > 2*fallbacksync.Interval {
		desc += fmt.Sprintf(" (expected every %d minutes; the job may not be running)", int(fallbacksync.Interval.Minutes()))
	}
	desc += fmt.Sprintf(": %d sessions synced, %d completed, %d events posted", s.SessionsSynced, s.SessionsCompleted, s.EventsPosted)
	if len(s.Errors) > 0 {
		desc += fmt.Sprintf(", %d errors (first: %s)", len(s.Errors), s.Errors[0])
	}
	if len(s.Paused) > 0 {
		desc += "; paused for " + strings.Join(s.Paused, "; ")
	}
	return desc
}
