package stop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/session"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	io       *iostreams.IOStreams
	stateDir string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{io: f.IO()}
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the job-level proxy started by `job-proxy start`.",
		Long: heredoc.Docf(`
			Stop the job-level Dependency Firewall proxy that %[1]sjob-proxy start%[1]s started in this shell. You do not usually need it in a CI/CD job, because the proxy stops when the job's container stops.
		`, "`"),
		Example: heredoc.Doc(`
			# Stop the proxy before the job ends
			glab dependency-firewall job-proxy stop
		`),
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		Annotations: map[string]string{
			mcpannotations.Exclude: "true",
			"help:environment":     session.StateDirEnv + ": directory holding the proxy's runtime state, as exported by `job-proxy start`. stop requires it to find the proxy to stop.\n",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(); err != nil {
				return err
			}
			return opts.run(cmd.Context())
		},
	}
	return cmd
}

func (o *options) complete() error {
	o.stateDir = os.Getenv(session.StateDirEnv)
	if o.stateDir == "" {
		return fmt.Errorf("no running Dependency Firewall proxy found: %s is not set. Run stop in the shell that evaluated `job-proxy start`", session.StateDirEnv)
	}
	return nil
}

func (o *options) run(_ context.Context) error {
	// Remove the per-run proxy CA from the host trust store on the way out, even
	// when the daemon is already gone, so a shell executor's trust store is not
	// left polluted. Best-effort.
	defer session.RemoveSystemTrust(o.stateDir, o.io.LogErrorf)

	pidBytes, err := os.ReadFile(filepath.Join(o.stateDir, session.PIDFile))
	if err != nil {
		return fmt.Errorf("no running Dependency Firewall proxy found in %s: %w", o.stateDir, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		return fmt.Errorf("invalid pid in %s: %w", o.stateDir, err)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("stopping the Dependency Firewall proxy (pid %d): %w", pid, err)
	}
	return nil
}
