package doctor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/claudehooks"
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
	hostname  string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		config:    f.Config,
		apiClient: f.ApiClient,
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

			Outputs a clear remediation command for any check that fails.
		`) + text.ExperimentalString,
		Example: heredoc.Doc(`
		    # Check AI agent governance configuration on this machine
		    $ glab govern doctor
		`),
		Args: cobra.NoArgs,
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
			message: "could not determine home directory",
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
			message: "could not determine home directory",
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
