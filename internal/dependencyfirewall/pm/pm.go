// Package pm runs a supported package manager (npm, pip, gem, ...) behind the
// Dependency Firewall's local MITM proxy. The shared Run flow starts the
// proxy, routes the manager's HTTPS traffic through it, makes the manager
// trust the proxy CA, forwards the command's args verbatim, records the
// firewall's per-coordinate verdicts to the CI log, renders a summary, and
// propagates the child's exit code. Each supported manager is a small
// implementation of PackageManager describing only what varies by ecosystem.
package pm

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/cilog"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/fsx"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/proxy"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/summary"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

// PackageManager captures what varies between supported package managers so
// the shared run flow (proxy + exec + log + summary) is written once. In the
// upstream-MITM model managers no longer rewrite their own config: they route
// traffic through the proxy (Environment), trust the proxy CA (CATrustEnviron),
// and supply the coordinate matcher for their ecosystem (Matcher).
type PackageManager interface {
	// Name is the CLI noun and log key: "npm", "pip", "maven".
	Name() string
	// Binary is the executable looked up on PATH.
	Binary() string
	// Environment returns child-process env vars that route the manager's
	// HTTPS traffic through proxyURL. The parent env (with proxy vars
	// sanitized) is supplied by Run via proxyEnviron.
	Environment(proxyURL string) []string
	// CATrustEnviron returns the env vars that make the manager trust the
	// proxy's MITM CA at caPath (NODE_EXTRA_CA_CERTS, SSL_CERT_FILE,
	// PIP_CERT/REQUESTS_CA_BUNDLE, MAVEN_OPTS truststore, etc.).
	CATrustEnviron(caPath string) []string
	// ExistingBundleVar names the environment variable that holds the user's
	// pre-existing CA bundle for this ecosystem (e.g. NODE_EXTRA_CA_CERTS,
	// REQUESTS_CA_BUNDLE, SSL_CERT_FILE), so the engine can prepend those
	// trust anchors to the proxy CA rather than replacing them. An empty
	// string means the manager has no such variable to preserve.
	ExistingBundleVar() string
	// CleanupCAFiles removes any files this manager derived from the CA
	// bundle at caPath (for example a JVM truststore or a generated
	// settings.xml). The engine always removes caPath itself; this covers
	// ecosystem-specific sidecars the engine would otherwise not know about.
	CleanupCAFiles(caPath string)
	// Matcher returns the proxy coordinate matcher for this ecosystem's
	// upstream URL shape.
	Matcher() proxy.Matcher
}

// proxyEnviron returns parent with any caller-supplied proxy variables removed
// and the inspection proxy set for HTTPS traffic only. managerKeys are the
// env-var names the manager's own Environment sets (e.g. NPM_CONFIG_HTTPS_PROXY
// for npm); they are stripped from the inherited env here so the engine stays
// manager-agnostic — it does not hardcode any ecosystem's routing variables.
func proxyEnviron(parent []string, proxyURL string, managerKeys ...string) []string {
	proxyKeys := map[string]struct{}{
		"HTTP_PROXY":  {},
		"HTTPS_PROXY": {},
		// NO_PROXY must be stripped too: managers (and the Go/Node/curl HTTP
		// stacks) honor it, so an inherited NO_PROXY=<registry> or NO_PROXY=*
		// (common in CI images) would route traffic straight past the MITM
		// proxy — no coordinates matched, no verdicts, a silent fail-open. The
		// firewall fails closed everywhere else, so it must here too.
		"NO_PROXY": {},
	}
	for _, k := range managerKeys {
		proxyKeys[strings.ToUpper(k)] = struct{}{}
	}
	env := make([]string, 0, len(parent)+2)
	for _, e := range parent {
		key, _, _ := strings.Cut(e, "=")
		if _, skip := proxyKeys[strings.ToUpper(key)]; skip {
			continue
		}
		env = append(env, e)
	}
	// Point both HTTP_PROXY and HTTPS_PROXY at the inspection proxy. HTTPS is
	// the normal path (registries have served over HTTPS for years). HTTP_PROXY
	// is set — not merely stripped — to fail closed on plaintext: the registry
	// URL is repo-controlled, so a committed ".npmrc registry=http://mirror/"
	// (or the equivalent for any ecosystem) would otherwise fetch over plain
	// HTTP, consult an absent HTTP_PROXY, and connect directly with zero
	// inspection on a green job. Routing plaintext at the proxy breaks it
	// loudly instead: the proxy serves CONNECT only and 405s non-CONNECT
	// traffic, so a plaintext fetch errors rather than silently bypassing.
	// Manager-specific routing variables are re-added by the manager's
	// Environment, appended by Run after this call.
	return append(env, "HTTP_PROXY="+proxyURL, "HTTPS_PROXY="+proxyURL)
}

// envKeys returns the KEY names from a "KEY=value" environment slice.
func envKeys(env []string) []string {
	keys := make([]string, 0, len(env))
	for _, e := range env {
		if k, _, ok := strings.Cut(e, "="); ok {
			keys = append(keys, k)
		}
	}
	return keys
}

// writeCABundle PEM-encodes the proxy CA into a temp file so managers can
// trust the proxy via CATrustEnviron. If the manager names an existing CA
// bundle variable (ExistingBundleVar) and the parent env sets it, those trust
// anchors are prepended so the user's are preserved. Returns the path; the
// caller removes it.
func writeCABundle(ios *iostreams.IOStreams, parent []string, bundleVar string, ca *x509.Certificate) (string, error) {
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw})

	var bundle []byte
	if existing := lookupEnv(parent, bundleVar); bundleVar != "" && existing != "" {
		userPEM, err := os.ReadFile(existing)
		if err != nil {
			ios.LogErrorf("warning: could not read existing %s %q: %v\n", bundleVar, existing, err)
		} else {
			bundle = append(bundle, userPEM...)
			if len(bundle) > 0 && bundle[len(bundle)-1] != '\n' {
				bundle = append(bundle, '\n')
			}
		}
	}
	bundle = append(bundle, pemBytes...)

	f, err := os.CreateTemp("", "glab-df-ca-*.pem")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(bundle); err != nil {
		return "", errors.Join(err, f.Close(), os.Remove(f.Name()))
	}
	if err := f.Close(); err != nil {
		return "", errors.Join(err, os.Remove(f.Name()))
	}
	return f.Name(), nil
}

// dedupEnv collapses an environment slice so each key appears once with its
// last-assigned value, preserving first-seen ordering. This makes the child
// process's view of each variable deterministic regardless of how its
// runtime resolves duplicate envp entries.
func dedupEnv(env []string) []string {
	index := make(map[string]int, len(env))
	out := make([]string, 0, len(env))
	for _, e := range env {
		key, _, ok := strings.Cut(e, "=")
		if !ok {
			out = append(out, e)
			continue
		}
		if i, seen := index[key]; seen {
			out[i] = e
			continue
		}
		index[key] = len(out)
		out = append(out, e)
	}
	return out
}

// lookupEnv returns the value of key in an environment slice ("KEY=value"),
// or "" if absent. The last occurrence wins.
func lookupEnv(env []string, key string) string {
	value := ""
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if ok && k == key {
			value = v
		}
	}
	return value
}

// Executor runs the package-manager binary. Satisfied by cmdutils.Executor.
type Executor interface {
	LookPath(file string) (string, error)
	ExecWithIO(ctx context.Context, name string, args []string, env []string, stdin io.Reader, stdout, stderr io.Writer) error
}

// interruptExitCode is 130 (the SIGINT convention) reported for any interrupt.
// Run detects interruption from context cancellation and cannot recover the
// specific signal (the received value is discarded), so it does not compute
// 128+signal — SIGTERM's 143, for example, is never returned. The sibling df
// package/ci-summary commands document blockExitCode = 3; this code is
// distinct from that and from the generic-failure 1.
const interruptExitCode = 130

// ExitError reports that the package-manager binary exited non-zero, was
// interrupted, or could not be run, carrying the exit Code the process should
// return. pm is a library package and must not import internal/cmdutils
// (command-layer only, enforced by depguard), so the calling command
// translates this into a *cmdutils.ExitError; see dfcmd.run.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }

// RunOptions are the inputs to Run. Registry/token fields are gone: the model
// no longer talks to a GitLab registry.
type RunOptions struct {
	IO        *iostreams.IOStreams
	Executor  Executor
	BaseDir   string
	Client    *gitlab.Client // authenticated API client, for the policy checker
	ProjectID string         // project id or full-path slug, for policy scoping
	Args      []string

	// notify is a test-only seam. When nil (the production path) Run installs
	// no signal handler at all — glab's root command owns signals via
	// fang.WithNotifySignal and cancels ctx on a signal. When non-nil it is
	// invoked in place of signal.Notify so tests can drive the same
	// cancellation deterministically without a real OS signal.
	notify func(c chan<- os.Signal, sig ...os.Signal)
}

// Run executes manager.Binary() with args forwarded verbatim, routing HTTPS
// traffic through a local MITM proxy that policy-checks each artifact/upload
// coordinate. Blocked packages are recorded and the summary is rendered on
// exit.
func Run(ctx context.Context, manager PackageManager, opts RunOptions) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	baseChecker, err := policy.New(opts.Client)
	if err != nil {
		return err
	}
	checker := policy.NewCachingChecker(baseChecker, opts.IO.LogErrorf)

	p, err := proxy.New(manager.Matcher(), checker, opts.ProjectID)
	if err != nil {
		return fmt.Errorf("failed to create inspection proxy: %w", err)
	}
	if err := p.Start(); err != nil {
		return fmt.Errorf("failed to start inspection proxy: %w", err)
	}
	defer p.Stop() //nolint:contextcheck // proxy shutdown manages its own context internally

	proxyURL := "http://" + p.Addr()

	caPath, err := writeCABundle(opts.IO, os.Environ(), manager.ExistingBundleVar(), p.CACertificate())
	if err != nil {
		return fmt.Errorf("failed to write proxy CA bundle: %w", err)
	}
	// The engine always removes the CA bundle; the manager removes any
	// ecosystem-specific sidecars it derived from it (e.g. a JVM truststore).
	defer func() {
		_ = os.Remove(caPath)
		manager.CleanupCAFiles(caPath)
	}()

	// Signal handling: glab's root command (main.go) already installs
	// fang.WithNotifySignal(os.Interrupt, SIGTERM), which cancels ctx on a
	// signal. We do not install a second signal.Notify; a cancelled context
	// surfaces as a context.Canceled execErr below, which we map to the
	// interrupt exit code. The notify hook is retained only so tests can drive
	// the same cancellation deterministically without a real OS signal.
	if opts.notify != nil {
		sigCh := make(chan os.Signal, 1)
		opts.notify(sigCh, os.Interrupt, syscall.SIGTERM)
		defer func() {
			signal.Stop(sigCh)
			close(sigCh)
		}()
		go func() {
			if _, ok := <-sigCh; !ok {
				return
			}
			cancel()
		}()
	}

	binPath, err := opts.Executor.LookPath(manager.Binary())
	if err != nil {
		return fmt.Errorf("%s not found on PATH: %w", manager.Binary(), err)
	}

	managerEnv := manager.Environment(proxyURL)
	// Strip the manager's own routing keys from the inherited env so they
	// can't leak an old value the manager's Environment doesn't overwrite;
	// the manager's values are re-appended immediately below.
	env := proxyEnviron(os.Environ(), proxyURL, envKeys(managerEnv)...)
	env = append(env, managerEnv...)
	env = append(env, manager.CATrustEnviron(caPath)...)
	// Collapse duplicate keys (last wins) so the proxy/CA-trust values we
	// append override any conflicting entry inherited from the parent
	// environment, regardless of how the child runtime parses envp.
	env = dedupEnv(env)

	execErr := opts.Executor.ExecWithIO(ctx, binPath, opts.Args, env, opts.IO.In, opts.IO.StdOut, opts.IO.StdErr)

	p.Stop() //nolint:contextcheck // proxy shutdown manages its own context internally
	verdicts := p.Verdicts()
	writeLog(opts.IO, opts.BaseDir, manager.Name(), opts.Args, verdicts)
	if len(verdicts) > 0 {
		summary.Render(opts.IO, verdicts)
	}

	// A cancelled context (from a signal, via fang or the injected notify
	// hook) means the run was interrupted, regardless of which path fired
	// first. Report the conventional interrupt code rather than the generic
	// failure exitError would derive from context.Canceled.
	if errors.Is(execErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return &ExitError{Code: interruptExitCode, Err: errors.New("interrupted")}
	}
	return exitError(manager.Binary(), execErr)
}

// writeLog merges this run's verdicts into the CI log at
// baseDir/.gitlab/df/ci-log.json. It loads any existing log first and appends
// (cilog dedupes by Entry.Key) so a job that runs the firewall more than once
// — npm then pip, or install then publish — accumulates every run's verdicts.
// Overwriting instead would drop earlier runs, and df ci-summary would then
// exit 0 even though a package was blocked, silently defeating the job gate.
//
// A Load error means a present-but-corrupt log (cilog.Load returns a fresh log
// with a nil error for a missing file). We must NOT start fresh in that case:
// overwriting a log we cannot parse would discard an earlier run's Blocked
// entries and flip ci-summary's fail-closed "unreadable log" exit into a pass.
// So on a Load error we leave the file untouched and skip the write, keeping
// the corrupt log in place for ci-summary to fail on.
//
// The log is written into the working tree on every run, including local
// ones, so it will appear as an untracked .gitlab/df/ci-log.json for
// developers; that is intentional (ci-summary reads it back to gate the job).
func writeLog(ios *iostreams.IOStreams, baseDir, name string, args []string, entries []verdict.Entry) {
	command := strings.Join(append([]string{name}, args...), " ")
	// Hold the sidecar lock across Load -> Append -> Save so two concurrent
	// "glab df run" invocations in one job serialize instead of racing
	// last-writer-wins (which would drop the losing run's Blocked entries and
	// let ci-summary exit 0).
	err := fsx.WithLock(cilog.LockPath(baseDir), func() error {
		log, err := cilog.Load(baseDir)
		if err != nil {
			// A Load error means a present-but-corrupt log (Load returns a
			// fresh log with nil error for a missing file). Leave it in place
			// rather than overwriting: starting fresh would discard an earlier
			// run's Blocked entries and flip ci-summary's fail-closed
			// unreadable-log exit into a pass.
			return fmt.Errorf("existing Dependency Firewall CI log is unreadable; leaving it in place for ci-summary to fail on: %w", err)
		}
		// On the normal path Load returns a zero-value log for a missing file,
		// so its Session (Command/StartedAt) is empty — only cilog.New sets
		// it. Populate it here for the first run so ci-log.json always records
		// the session that created it; a later run leaves an already-set
		// Session alone so the log keeps the originating command.
		if log.Session.Command == "" {
			log.Session = cilog.New(command).Session
		}
		for _, e := range entries {
			log.Append(e)
		}
		if err := cilog.Save(baseDir, log); err != nil {
			return fmt.Errorf("failed to write Dependency Firewall CI log: %w", err)
		}
		return nil
	})
	if err != nil {
		ios.LogErrorf("warning: %v\n", err)
	}
}

// exitError wraps a package-manager execution error as an *ExitError carrying
// the child's exit Code. The calling command translates it into the
// *cmdutils.ExitError that main.go's handler unwraps to set glab's exit
// status; see dfcmd.run.
func exitError(binary string, err error) error {
	if err == nil {
		return nil
	}
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return &ExitError{Code: ee.ExitCode(), Err: fmt.Errorf("%s exited with a non-zero status: %w", binary, err)}
	}
	return &ExitError{Code: 1, Err: fmt.Errorf("failed to run %s: %w", binary, err)}
}
