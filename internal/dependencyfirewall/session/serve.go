package session

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/cilog"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/fsx"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/pm"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/proxy"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

const (
	// AddrFile holds the proxy's listen address. It is written last — after the
	// proxy is listening and its CA is on disk — so its presence signals
	// readiness to `job-proxy start`.
	AddrFile = "addr"
	// PIDFile holds the daemon's process id, for `job-proxy stop`.
	PIDFile = "pid"
	// CACertFile holds the proxy's MITM CA in PEM, for the job to trust.
	CACertFile = "proxy-ca.pem"
)

// ServeConfig configures the job-level proxy daemon.
type ServeConfig struct {
	Client    *gitlab.Client
	ProjectID string
	BaseDir   string // working directory; where .gitlab/df/ci-log.json is written
	StateDir  string // where addr/pid/proxy-ca.pem are written
	// CACertPath is the ca_cert setting for the GitLab host, trusted for
	// upstream registry connections alongside the system roots and the CA
	// bundles named in the environment, as a per-command run does.
	CACertPath string
	// ClientCertPath, ClientKeyPath and ClientCertPassword are glab's client_cert,
	// client_key and client_cert_password for the GitLab host, presented to an
	// https:// outbound proxy that requires a client certificate, as a
	// per-command run does. Registries never receive the certificate.
	ClientCertPath     string
	ClientKeyPath      string
	ClientCertPassword func() (string, error)
	IO                 *iostreams.IOStreams
}

// flushInterval bounds how stale the CI log can be if the daemon is killed
// abruptly (the container stops at job end) rather than signalled: at most this
// much of the last activity could be missing.
const flushInterval = time.Second

// Serve runs the job-level inspection proxy until it is signalled or ctx is
// cancelled, streaming the firewall's verdicts to the CI log so
// `glab dependency-firewall ci-summary` can still gate the job even though no
// single package-manager command owns the proxy. It is the body of the detached
// `job-proxy serve` daemon.
func Serve(ctx context.Context, cfg ServeConfig) error {
	base, err := policy.New(cfg.Client)
	if err != nil {
		return err
	}

	// Write each verdict to the CI log the instant it is recorded, before the
	// blocked response reaches the client, so a `ci-summary` that runs right
	// after a blocked install never races the flush below. The ticker only
	// retries a failed hook write and bounds the loss on an abrupt kill.
	opts := []proxy.Option{
		proxy.WithVerdictHook(func(e verdict.Entry) {
			writeVerdicts(cfg.BaseDir, []verdict.Entry{e}, cfg.IO.LogErrorf)
		}),
		proxy.WithUpstreamFailureWarning(cfg.IO.LogErrorf),
	}
	roots, err := pm.UpstreamRootCAs(os.Environ(), cfg.BaseDir, cfg.CACertPath, cfg.IO.LogErrorf)
	if err != nil {
		return fmt.Errorf("failed to load registry CA certificates: %w", err)
	}
	if roots != nil {
		opts = append(opts, proxy.WithUpstreamRootCAs(roots))
	}
	certOpts, err := pm.UpstreamProxyCertificateOptions(cfg.ClientCertPath, cfg.ClientKeyPath, cfg.ClientCertPassword)
	if err != nil {
		return err
	}
	opts = append(opts, certOpts...)
	policySummary := policy.LoadSummary(ctx, base, cfg.ProjectID, cfg.BaseDir, cfg.IO.LogErrorf)
	checker := policy.NewCachingChecker(base, cfg.IO.LogErrorf, policy.WithUnreachableFallback(policySummary))
	p, err := proxy.New(proxy.DefaultMatchers(), checker, cfg.ProjectID, opts...)
	if err != nil {
		return err
	}
	if err := p.Start(); err != nil {
		return err
	}
	defer p.Stop() //nolint:contextcheck // Stop must finish shutdown and tunnel drain on its own timeout even when ctx is already cancelled.

	if err := writeCAPEM(filepath.Join(cfg.StateDir, CACertFile), p.CACertificate()); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(cfg.StateDir, PIDFile), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		return err
	}
	// Written last: `job-proxy start` waits for this file, so the proxy is listening
	// and its CA is on disk by the time the address appears.
	if err := os.WriteFile(filepath.Join(cfg.StateDir, AddrFile), []byte(p.Addr()), 0o644); err != nil {
		return err
	}
	cfg.IO.LogErrorf("dependency firewall: job-level proxy ready at %s\n", p.Addr())

	flush := func() { writeVerdicts(cfg.BaseDir, p.Verdicts(), cfg.IO.LogErrorf) }

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigs)
	tick := time.NewTicker(flushInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			flush()
			return nil
		case <-sigs:
			flush()
			return nil
		case <-tick.C:
			flush()
		}
	}
}

// writeVerdicts merges verdicts into the CI log under the sidecar lock, the same
// way a package-manager run writes it, so a later `ci-summary` reads the full
// set. cilog dedupes by Entry.Key, so re-writing the full snapshot each tick is
// idempotent. A failed write is logged: ci-summary can't fail the job on a
// verdict that never reached the log, so the job log is the only trace of it.
func writeVerdicts(baseDir string, verdicts []verdict.Entry, logf func(string, ...any)) {
	if len(verdicts) == 0 {
		return
	}
	err := fsx.WithLock(cilog.LockPath(baseDir), func() error {
		log, err := cilog.Load(baseDir)
		if err != nil {
			// A present-but-corrupt log: leave it for ci-summary to fail on,
			// rather than overwriting an earlier run's blocked entries.
			return err
		}
		if log.Session.Command == "" {
			log.Session = cilog.New("dependency-firewall job-proxy").Session
		}
		for _, e := range verdicts {
			log.Append(e)
		}
		return cilog.Save(baseDir, log)
	})
	if err != nil {
		logf("dependency firewall: could not write %d verdict(s) to the CI log in %s: %v\n", len(verdicts), baseDir, err)
	}
}
