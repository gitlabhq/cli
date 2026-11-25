package start

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/cilog"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/pm"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/session"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

// readyTimeout bounds how long start waits for the daemon to listen.
const readyTimeout = 20 * time.Second

type options struct {
	io *iostreams.IOStreams
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{io: f.IO()}
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the job-level proxy and print shell exports to route the job through it.",
		Long: heredoc.Docf(`
			Start the job-level Dependency Firewall proxy as a background process and print %[1]sexport%[1]s lines that route the current shell through it. Evaluate the output so the exports apply to the rest of the job.

			If the proxy cannot start, the command prints %[1]sfalse%[1]s, so the %[1]seval%[1]s fails and a CI/CD job (which runs with %[1]sset -e%[1]s) stops instead of running without the firewall.
		`, "`"),
		Example: heredoc.Doc(`
			# In before_script: route the rest of the CI/CD job through the proxy
			eval "$(glab dependency-firewall job-proxy start)"

			# As the last command in script: fail the job if a package was blocked
			glab dependency-firewall ci-summary
		`),
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		Annotations: map[string]string{
			mcpannotations.Exclude: "true",
			"help:environment":     session.StateDirEnv + ": directory for the proxy's runtime state (address, pid, CA). Set it to pin a known location; otherwise start creates a fresh per-user directory and exports this variable so `job-proxy stop` finds the same proxy.\n",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.run(cmd.Context())
		},
	}
	return cmd
}

// run starts the proxy. Its output is eval'd, so on failure it prints `false`:
// empty output would make the eval succeed and leave the job running green
// with no firewall.
func (o *options) run(ctx context.Context) error {
	if err := o.start(ctx); err != nil {
		o.io.LogInfo("false")
		return err
	}
	return nil
}

func (o *options) start(ctx context.Context) error {
	stateDir, err := session.NewStateDir()
	if err != nil {
		return fmt.Errorf("creating the Dependency Firewall proxy state directory: %w", err)
	}
	// Clear an earlier run's state: start waits for the address file, so a stale
	// one would hand the job a dead proxy and a CA the new daemon doesn't use.
	for _, name := range []string{session.AddrFile, session.PIDFile, session.CACertFile} {
		if err := os.Remove(filepath.Join(stateDir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("clearing stale Dependency Firewall proxy state: %w", err)
		}
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logPath := filepath.Join(stateDir, "daemon.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()

	daemon := exec.Command(exe, "dependency-firewall", "job-proxy", "serve", "--state-dir", stateDir)
	daemon.Stdout = logFile
	daemon.Stderr = logFile
	// The daemon keeps the caller's original HTTP(S)_PROXY/NO_PROXY so its own
	// upstream fetches chain to a corporate egress proxy; the exports printed
	// below only repoint the job, not the daemon.
	daemon.Env = os.Environ()
	if err := detach(daemon); err != nil {
		return err
	}
	if err := daemon.Start(); err != nil {
		return fmt.Errorf("starting the Dependency Firewall proxy daemon: %w", err)
	}
	_ = daemon.Process.Release()

	addr, err := waitForFile(ctx, filepath.Join(stateDir, session.AddrFile), readyTimeout)
	if err != nil {
		return fmt.Errorf("the Dependency Firewall proxy did not become ready: %w\n--- daemon log ---\n%s", err, tail(logPath))
	}
	proxyURL := "http://" + strings.TrimSpace(addr)
	caPath := filepath.Join(stateDir, session.CACertFile)

	// Trust, additive: install into the system store (covers curl, git, apt),
	// and write a merged bundle (system roots + the caller's bundles + the proxy
	// CA) for tools that read a bundle instead of the system store.
	session.InstallSystemTrust(stateDir, caPath, o.io.LogErrorf)
	mergedPath, err := session.WriteMergedBundle(stateDir, caPath, session.CallerBundlePaths(os.Environ()), o.io.LogErrorf)
	if err != nil {
		return fmt.Errorf("writing merged CA bundle: %w", err)
	}

	// Never route traffic to the GitLab instance itself through the firewall
	// proxy: the job's git fetches and API calls must reach GitLab directly, not
	// be MITM'd. CI_SERVER_HOST is exempted on top of the caller's NO_PROXY.
	noProxy := os.Getenv("NO_PROXY") + "," + os.Getenv("no_proxy")
	if host := os.Getenv("CI_SERVER_HOST"); host != "" {
		noProxy = noProxy + "," + host
	}
	env := session.BuildJobEnv(proxyURL, mergedPath, noProxy)
	// Maven/Gradle need a generated settings.xml and JVM trust store rather than
	// the env-var routing/trust the other managers use; best-effort, so a failure
	// here never blocks the rest of the job's coverage.
	jvm, jvmErr := session.JVMTrustEnv(stateDir, proxyURL, caPath, o.io.LogErrorf)
	if jvmErr != nil {
		o.io.LogErrorf("dependency firewall: Java (Maven/Gradle) not fully routed through the proxy: %v\n", jvmErr)
	}
	env = append(env, jvm...)
	env = append(env, session.StateDirEnv+"="+stateDir)
	logDir, err := session.LogBaseDir()
	if err != nil {
		return err
	}
	pm.WarnJobTrustStoreOverrides(os.Environ(), logDir, o.io.LogErrorf)
	env = append(env, cilog.DirEnv+"="+logDir)

	o.io.LogInfof("%s", session.ShellExports(env))
	return nil
}

// waitForFile polls path until it exists and is non-empty, returning its
// contents, or errors on timeout or context cancellation.
func waitForFile(ctx context.Context, path string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		if b, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(b)) != "" {
			return string(b), nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timed out after %s waiting for %s", timeout, path)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// tail returns roughly the last 2 KiB of the file at path, for surfacing a
// failed daemon's log when startup times out.
func tail(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	const maxBytes = 2048
	if len(b) > maxBytes {
		b = b[len(b)-maxBytes:]
	}
	return string(b)
}
