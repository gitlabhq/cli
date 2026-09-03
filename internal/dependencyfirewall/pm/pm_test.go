//go:build !integration

package pm

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/cilog"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/proxy"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestManagerMetadata(t *testing.T) {
	t.Parallel()
	cases := []struct {
		m         PackageManager
		name, bin string
	}{
		{NPM(), "npm", "npm"},
		{Pnpm(), "pnpm", "pnpm"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.name, c.m.Name())
			assert.Equal(t, c.bin, c.m.Binary())
		})
	}
}

func TestNPMCATrust(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"NODE_EXTRA_CA_CERTS=/tmp/ca.pem"}, NPM().CATrustEnviron("/tmp/ca.pem"))
}

func TestProxyEnvironStripsInheritedProxyVarsAndSetsHTTPS(t *testing.T) {
	t.Parallel()
	parent := []string{
		"PATH=/usr/bin",
		"HTTP_PROXY=http://corp",
		"HTTPS_PROXY=http://corp",
		"https_proxy=http://corp", // lowercase must also be stripped
		"NO_PROXY=registry.npmjs.org",
		"no_proxy=*",
		"NPM_CONFIG_PROXY=http://corp",       // a manager-owned key
		"NPM_CONFIG_HTTPS_PROXY=http://corp", // a manager-owned key
	}
	// Pass the npm manager's own routing keys so the engine strips them too
	// without hardcoding any ecosystem's variables.
	got := proxyEnviron(parent, "http://127.0.0.1:8080", "NPM_CONFIG_PROXY", "NPM_CONFIG_HTTPS_PROXY")

	// Every inherited proxy/no-proxy variable (any case), plus the manager's
	// own routing keys, must be gone. HTTP_PROXY and HTTPS_PROXY are then set
	// to the inspection proxy (not merely stripped): a plaintext-HTTP registry
	// must hit the proxy and fail loudly, and a surviving NO_PROXY would route
	// traffic past the MITM proxy and silently fail open.
	for _, e := range got {
		key, _, _ := strings.Cut(e, "=")
		switch strings.ToUpper(key) {
		case "HTTP_PROXY":
			if e == "HTTP_PROXY=http://127.0.0.1:8080" {
				continue // the one we set (fail-closed on plaintext)
			}
			t.Fatalf("inherited HTTP_PROXY survived sanitization: %q", e)
		case "HTTPS_PROXY":
			if e == "HTTPS_PROXY=http://127.0.0.1:8080" {
				continue // the one we set
			}
			t.Fatalf("inherited HTTPS_PROXY survived sanitization: %q", e)
		case "NO_PROXY", "NPM_CONFIG_PROXY", "NPM_CONFIG_HTTPS_PROXY":
			t.Fatalf("inherited proxy var survived sanitization: %q", e)
		}
	}
	assert.Contains(t, got, "PATH=/usr/bin", "non-proxy vars must be preserved")
	assert.Contains(t, got, "HTTPS_PROXY=http://127.0.0.1:8080", "HTTPS_PROXY must point at the inspection proxy")
	assert.Contains(t, got, "HTTP_PROXY=http://127.0.0.1:8080", "HTTP_PROXY must point at the inspection proxy so plaintext fetches fail closed")
}

func TestProxyEnvironWithoutManagerKeysLeavesThemForRun(t *testing.T) {
	t.Parallel()
	// Without manager keys, only the universal proxy vars are stripped; a
	// manager-specific var the caller didn't declare is left untouched (Run
	// declares them via envKeys(manager.Environment)).
	parent := []string{"NPM_CONFIG_HTTPS_PROXY=http://corp"}
	got := proxyEnviron(parent, "http://127.0.0.1:8080")
	assert.Contains(t, got, "NPM_CONFIG_HTTPS_PROXY=http://corp")
}

func TestLookupEnvLastOccurrenceWins(t *testing.T) {
	t.Parallel()
	env := []string{"K=first", "OTHER=x", "K=second"}
	assert.Equal(t, "second", lookupEnv(env, "K"))
	assert.Empty(t, lookupEnv(env, "MISSING"))
}

func TestNPMEnvironRoutesThroughProxy(t *testing.T) {
	t.Parallel()
	env := NPM().Environment("http://127.0.0.1:9999")
	assert.Contains(t, env, "HTTPS_PROXY=http://127.0.0.1:9999")
	assert.Contains(t, env, "NPM_CONFIG_PROXY=http://127.0.0.1:9999")
	assert.Contains(t, env, "NPM_CONFIG_HTTPS_PROXY=http://127.0.0.1:9999")
	// noproxy must be pinned to a non-empty sentinel that never matches a real
	// registry: npm treats an empty env value as unset and lets a project
	// .npmrc "noproxy=" line win, which would reopen the proxy bypass.
	assert.Contains(t, env, "NPM_CONFIG_NOPROXY=localhost")
	assert.NotContains(t, env, "NPM_CONFIG_NOPROXY=", "empty NOPROXY is overridden by .npmrc; must be a non-empty sentinel")
}

func TestDedupEnvLastWinsPreservesOrder(t *testing.T) {
	t.Parallel()
	in := []string{
		"PATH=/usr/bin",
		"HTTPS_PROXY=http://old",
		"PIP_CERT=/user/ca.pem",
		"HTTPS_PROXY=http://proxy",
		"PIP_CERT=/proxy/ca.pem",
		"NO_EQUALS_ENTRY",
	}
	got := dedupEnv(in)
	assert.Equal(t, []string{
		"PATH=/usr/bin",
		"HTTPS_PROXY=http://proxy",
		"PIP_CERT=/proxy/ca.pem",
		"NO_EQUALS_ENTRY",
	}, got)
}

func TestMatcherTypes(t *testing.T) {
	t.Parallel()
	_, ok := NPM().Matcher().(proxy.NPMMatcher)
	assert.True(t, ok)
}

// fakeExecutor records the env it was invoked with and returns execErr.
type fakeExecutor struct {
	env     []string
	execErr error
}

func (f *fakeExecutor) LookPath(file string) (string, error) { return "/bin/" + file, nil }

func (f *fakeExecutor) ExecWithIO(_ context.Context, _ string, _ []string, env []string, _ io.Reader, _, _ io.Writer) error {
	f.env = env
	return f.execErr
}

func TestRunWritesLogAndSetsProxyEnv(t *testing.T) {
	t.Setenv("GITLAB_CI", "") // honor fake mode even when the suite runs in CI
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
	dir := t.TempDir()
	ios, _, _, _ := cmdtest.TestIOStreams()
	exec := &fakeExecutor{}

	err := Run(t.Context(), NPM(), RunOptions{
		IO:        ios,
		Executor:  exec,
		BaseDir:   dir,
		ProjectID: "group/proj",
		Args:      []string{"install", "left-pad"},
	})
	require.NoError(t, err)

	var sawProxy, sawCA bool
	for _, e := range exec.env {
		if strings.HasPrefix(e, "HTTPS_PROXY=") {
			sawProxy = true
		}
		if strings.HasPrefix(e, "NODE_EXTRA_CA_CERTS=") {
			sawCA = true
		}
	}
	assert.True(t, sawProxy, "child env must set HTTPS_PROXY")
	assert.True(t, sawCA, "child env must set NODE_EXTRA_CA_CERTS")

	_, statErr := os.Stat(cilog.Path(dir))
	require.NoError(t, statErr)

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		assert.NotEqual(t, ".npmrc", e.Name(), "Run must not write .npmrc")
	}
}

func TestRunExitErrorPropagates(t *testing.T) {
	t.Setenv("GITLAB_CI", "") // honor fake mode even when the suite runs in CI
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
	dir := t.TempDir()
	ios, _, _, _ := cmdtest.TestIOStreams()
	exec := &fakeExecutor{execErr: errors.New("boom")}

	err := Run(t.Context(), NPM(), RunOptions{
		IO: ios, Executor: exec, BaseDir: dir, ProjectID: "p", Args: []string{"x"},
	})
	require.Error(t, err)
	var ee *ExitError
	require.ErrorAs(t, err, &ee)
	// A generic (non-*exec.ExitError) failure maps to the generic exit code 1.
	assert.Equal(t, 1, ee.Code)
}

// exitExecutor returns an *exec.ExitError carrying a specific exit code, so we
// can assert the child's real status is propagated (not the generic 1).
type exitExecutor struct{ code int }

func (exitExecutor) LookPath(file string) (string, error) { return "/bin/" + file, nil }
func (e exitExecutor) ExecWithIO(_ context.Context, _ string, _ []string, _ []string, _ io.Reader, _, _ io.Writer) error {
	// Run a real command that exits with e.code to obtain a genuine
	// *exec.ExitError whose ExitCode() the engine must surface.
	cmd := exec.Command("sh", "-c", "exit "+strconv.Itoa(e.code))
	return cmd.Run()
}

func TestRunPropagatesChildExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh to produce a real *exec.ExitError")
	}
	t.Setenv("GITLAB_CI", "") // honor fake mode even when the suite runs in CI
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
	dir := t.TempDir()
	ios, _, _, _ := cmdtest.TestIOStreams()

	err := Run(t.Context(), NPM(), RunOptions{
		IO: ios, Executor: exitExecutor{code: 42}, BaseDir: dir, ProjectID: "p", Args: []string{"x"},
	})
	require.Error(t, err)
	var ee *ExitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 42, ee.Code, "engine must surface the child's exit code, not a generic 1")
}

func TestRunInterruptReturnsExitCode130(t *testing.T) {
	if runtime.GOOS == "windows" {
		// os.CreateTemp("") honors TMP/TEMP (not TMPDIR) on Windows, so the
		// leaked-file assertion below can't be isolated reliably; the signal
		// path is POSIX-oriented anyway.
		t.Skip("temp-dir isolation and signal semantics are POSIX-specific")
	}
	t.Setenv("GITLAB_CI", "") // honor fake mode even when the suite runs in CI
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
	t.Chdir(t.TempDir())

	// Isolate os.CreateTemp("") (used for the CA bundle) to a private dir so
	// the leaked-file assertion below is unaffected by other tests or runs.
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	sigChReady := make(chan chan<- os.Signal, 1)

	fe := &blockingExecutor{started: make(chan struct{})}

	ios, _, _, _ := cmdtest.TestIOStreams()
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(t.Context(), NPM(), RunOptions{
			IO:        ios,
			Executor:  fe,
			BaseDir:   ".",
			ProjectID: "g/p",
			Args:      []string{"install"},
			notify:    func(c chan<- os.Signal, sig ...os.Signal) { sigChReady <- c },
		})
	}()

	<-fe.started
	sigCh := <-sigChReady
	require.NotNil(t, sigCh)
	sigCh <- os.Interrupt

	err := <-errCh
	var ee *ExitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 130, ee.Code)

	// The interrupt path must still run the deferred cleanup that os.Exit
	// previously skipped: no CA bundle temp files may be left behind.
	leaked, _ := filepath.Glob(filepath.Join(tmp, "glab-df-ca-*"))
	assert.Empty(t, leaked, "interrupt path must remove the CA bundle temp file")
}

type blockingExecutor struct{ started chan struct{} }

func (b *blockingExecutor) LookPath(file string) (string, error) { return "/usr/bin/" + file, nil }
func (b *blockingExecutor) ExecWithIO(ctx context.Context, name string, args, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
	close(b.started)
	<-ctx.Done()
	return ctx.Err()
}

// testCA returns a throwaway self-signed certificate for CA-bundle tests.
func testCA(t *testing.T) *x509.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return cert
}

func TestWriteCABundleEmitsValidPEM(t *testing.T) {
	t.Parallel()
	ios, _, _, _ := cmdtest.TestIOStreams()
	path, err := writeCABundle(ios, nil, "", testCA(t))
	require.NoError(t, err)
	defer func() { _ = os.Remove(path) }()

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	block, _ := pem.Decode(raw)
	require.NotNil(t, block, "output must be valid PEM")
	assert.Equal(t, "CERTIFICATE", block.Type)
}

func TestWriteCABundlePrependsExistingBundle(t *testing.T) {
	t.Parallel()
	// A user-provided bundle named by the manager's ExistingBundleVar must be
	// preserved: the output should contain the user's PEM followed by the
	// proxy CA, so the child trusts both.
	userPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: testCA(t).Raw})
	userPath := filepath.Join(t.TempDir(), "user-ca.pem")
	require.NoError(t, os.WriteFile(userPath, userPEM, 0o600))

	ios, _, _, _ := cmdtest.TestIOStreams()
	parent := []string{"NODE_EXTRA_CA_CERTS=" + userPath}
	path, err := writeCABundle(ios, parent, "NODE_EXTRA_CA_CERTS", testCA(t))
	require.NoError(t, err)
	defer func() { _ = os.Remove(path) }()

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.True(t, bytes.HasPrefix(raw, userPEM), "user's bundle must be prepended")
	var blocks int
	for rest := raw; ; {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			break
		}
		blocks++
	}
	assert.Equal(t, 2, blocks, "bundle must contain the user CA and the proxy CA")
}

func TestWriteCABundleWarnsOnUnreadableExisting(t *testing.T) {
	t.Parallel()
	// An unreadable existing bundle must warn and fall back to the proxy CA
	// alone rather than failing the run.
	ios, _, _, errOut := cmdtest.TestIOStreams()
	parent := []string{"NODE_EXTRA_CA_CERTS=" + filepath.Join(t.TempDir(), "does-not-exist.pem")}
	path, err := writeCABundle(ios, parent, "NODE_EXTRA_CA_CERTS", testCA(t))
	require.NoError(t, err)
	defer func() { _ = os.Remove(path) }()

	assert.Contains(t, errOut.String(), "could not read existing NODE_EXTRA_CA_CERTS")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	block, _ := pem.Decode(raw)
	require.NotNil(t, block, "must still emit the proxy CA")
}

func TestWriteLogMergesSequentialRuns(t *testing.T) {
	t.Parallel()
	// Two firewall runs in one CI job (e.g. npm then pip) must accumulate
	// verdicts, not clobber each other, or ci-summary would miss the first
	// run's blocks and wrongly exit 0.
	dir := t.TempDir()
	ios, _, _, _ := cmdtest.TestIOStreams()

	writeLog(ios, dir, "npm", []string{"install"},
		[]verdict.Entry{{Package: "left-pad", Version: "1.3.0", Verdict: verdict.Blocked}})
	writeLog(ios, dir, "pip", []string{"install"},
		[]verdict.Entry{{Package: "requests", Version: "2.31.0", Verdict: verdict.Warning}})

	got, err := cilog.Load(dir)
	require.NoError(t, err)
	require.Len(t, got.Entries, 2, "second run must not overwrite the first")
	names := []string{got.Entries[0].Package, got.Entries[1].Package}
	assert.Contains(t, names, "left-pad")
	assert.Contains(t, names, "requests")
}

func TestWriteLogPopulatesSessionOnFirstRun(t *testing.T) {
	t.Parallel()
	// On the normal (missing-file) path cilog.Load returns a zero-value log,
	// so only writeLog sets the Session. A missing Session.Command would make
	// every ordinary run persist an empty session block.
	dir := t.TempDir()
	ios, _, _, _ := cmdtest.TestIOStreams()

	writeLog(ios, dir, "npm", []string{"install", "left-pad"}, nil)

	got, err := cilog.Load(dir)
	require.NoError(t, err)
	assert.Equal(t, "npm install left-pad", got.Session.Command, "first run must record the originating command")
	assert.NotEmpty(t, got.Session.StartedAt, "first run must record a start time")
}

func TestWriteLogPreservesFirstSessionAcrossRuns(t *testing.T) {
	t.Parallel()
	// A later run must not overwrite the session of the run that created the
	// log, so ci-summary attributes the log to its originating command.
	dir := t.TempDir()
	ios, _, _, _ := cmdtest.TestIOStreams()

	writeLog(ios, dir, "npm", []string{"install"}, nil)
	writeLog(ios, dir, "pip", []string{"install"}, nil)

	got, err := cilog.Load(dir)
	require.NoError(t, err)
	assert.Equal(t, "npm install", got.Session.Command, "session must stay pinned to the first run")
}

func TestWriteLogLeavesCorruptLogInPlace(t *testing.T) {
	t.Parallel()
	// A present-but-corrupt log must NOT be overwritten: starting fresh would
	// discard an earlier run's Blocked entries and flip ci-summary's
	// fail-closed unreadable-log exit into a pass. The corrupt bytes must
	// remain so ci-summary fails on them.
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Dir(cilog.Path(dir)), 0o755))
	corrupt := []byte("{ this is not valid json")
	require.NoError(t, os.WriteFile(cilog.Path(dir), corrupt, 0o600))

	ios, _, _, errOut := cmdtest.TestIOStreams()
	writeLog(ios, dir, "npm", []string{"install"},
		[]verdict.Entry{{Package: "left-pad", Version: "1.3.0", Verdict: verdict.Blocked}})

	raw, err := os.ReadFile(cilog.Path(dir))
	require.NoError(t, err)
	assert.Equal(t, corrupt, raw, "corrupt log must be left untouched for ci-summary to fail on")
	assert.Contains(t, errOut.String(), "unreadable")
}

// TestRunComposedBlockThroughProxy exercises the full engine path end to end:
// Run starts the real inspection proxy, the executor sends a real HTTPS
// request for an npm tarball through the HTTPS_PROXY it received, trusting the
// proxy CA from NODE_EXTRA_CA_CERTS; the fake checker blocks the coordinate.
// It asserts the child sees a 403, the Blocked verdict reaches the saved CI
// log, and the summary is rendered — none of which the fakeExecutor-based
// tests prove, since they never dial the proxy.
func TestRunComposedBlockThroughProxy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on POSIX temp-dir isolation for the CA bundle")
	}
	t.Setenv("GITLAB_CI", "") // honor fake mode even when the suite runs in CI
	t.Setenv("GLAB_DF_FAKE_BLOCK", "npm:left-pad@1.3.0")

	dir := t.TempDir()
	ios, _, _, errOut := cmdtest.TestIOStreams()
	ce := &curlingExecutor{path: "/left-pad/-/left-pad-1.3.0.tgz"}

	err := Run(t.Context(), NPM(), RunOptions{
		IO:        ios,
		Executor:  ce,
		BaseDir:   dir,
		ProjectID: "group/proj",
		Args:      []string{"install", "left-pad"},
	})
	require.Error(t, err, "a blocked fetch must fail the run")

	require.NotNil(t, ce.resp, "executor must have received a response through the proxy")
	assert.Equal(t, http.StatusForbidden, ce.resp.StatusCode, "blocked coordinate must yield a 403 to the child")

	got, loadErr := cilog.Load(dir)
	require.NoError(t, loadErr)
	require.Len(t, got.Entries, 1, "the composed path must record exactly one verdict")
	assert.Equal(t, "left-pad", got.Entries[0].Package)
	assert.Equal(t, verdict.Blocked, got.Entries[0].Verdict)

	assert.Contains(t, errOut.String(), "left-pad", "summary must mention the blocked package")
}

// curlingExecutor ignores the package-manager binary and instead performs a
// real HTTPS GET of path through the HTTPS_PROXY and CA it is handed, mimicking
// what a package manager would do. It records the response so the test can
// assert on the synthesized 403.
type curlingExecutor struct {
	path string
	resp *http.Response
}

func (curlingExecutor) LookPath(file string) (string, error) { return "/bin/" + file, nil }

func (c *curlingExecutor) ExecWithIO(ctx context.Context, _ string, _ []string, env []string, _ io.Reader, _, _ io.Writer) error {
	proxyRaw := lookupEnv(env, "HTTPS_PROXY")
	proxyURL, err := url.Parse(proxyRaw)
	if err != nil {
		return err
	}
	caPath := lookupEnv(env, "NODE_EXTRA_CA_CERTS")
	pemBytes, err := os.ReadFile(caPath)
	if err != nil {
		return err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return errors.New("failed to load proxy CA from NODE_EXTRA_CA_CERTS")
	}
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
	}
	// A blocked coordinate is refused before the upstream round trip, so the
	// registry host is never actually dialed; any resolvable-looking host works.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://registry.npmjs.org"+c.path, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	c.resp = resp
	// Mirror a package manager: a 403 from the registry is a failure.
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("registry returned %d", resp.StatusCode)
	}
	return nil
}
