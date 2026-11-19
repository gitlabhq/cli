package pm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/fsx"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/proxy"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

// bundleManager runs Bundler through the proxy. Bundler honors the lowercase
// proxy variables and SSL_CERT_FILE/BUNDLE_SSL_CA_CERT for CA trust; the
// Gemfile is left untouched.
//
// Known gap (same as gem): a gemrc "http_proxy:" setting outranks the env
// pins. Bundler applies Gem.configuration[:http_proxy] on top of its :ENV
// proxy (2.5.22 fetcher.rb). Its sources are the system gemrc, ~/.gemrc, or a
// file named by an inherited GEMRC env var — none repo-committable, so only a
// compromised CI image or pipeline env, not a committed repo file, can route
// bundle traffic around inspection this way.
type bundleManager struct{}

// Bundle returns the bundle package manager.
func Bundle() PackageManager { return bundleManager{} }

func (bundleManager) Name() string   { return "bundle" }
func (bundleManager) Binary() string { return "bundle" }

// Environment adds the lowercase proxy variables. The engine already sets
// uppercase HTTP_PROXY/HTTPS_PROXY in proxyEnviron, but Bundler resolves the
// proxy through net-http-persistent constructed with proxy: :ENV, which reads
// the lowercase http_proxy/https_proxy names (it does check both cases, so the
// lowercase pair is defense-in-depth rather than the sole routing path). A
// repo-local .bundle/config BUNDLE_HTTP_PROXY does not override these: Bundler
// never consults Bundler.settings for the proxy at all — its fetcher only uses
// the :ENV mode — so a committed BUNDLE_HTTP_PROXY is simply inert (verified
// against Bundler 2.5.22 fetcher.rb). Only the lowercase pair is added; the
// uppercase names the engine sets are not duplicated.
func (bundleManager) Environment(proxyURL string) []string {
	return []string{
		"https_proxy=" + proxyURL,
		"http_proxy=" + proxyURL,
	}
}

// CATrustEnviron trusts the proxy CA via both the generic OpenSSL SSL_CERT_FILE
// (honored by RubyGems/Net::HTTP) and Bundler's BUNDLE_SSL_CA_CERT. A local
// config's BUNDLE_SSL_CA_CERT outranks this one; Setup handles it.
func (bundleManager) CATrustEnviron(caPath string) []string {
	return []string{
		"SSL_CERT_FILE=" + caPath,
		"BUNDLE_SSL_CA_CERT=" + caPath,
	}
}

// ExistingBundleVars lists both CA vars CATrustEnviron overwrites so a user
// who set only BUNDLE_SSL_CA_CERT keeps their anchors, not just one who set
// SSL_CERT_FILE.
func (bundleManager) ExistingBundleVars() []string {
	return []string{"SSL_CERT_FILE", "BUNDLE_SSL_CA_CERT"}
}

func (bundleManager) CleanupCAFiles(string) {}

func (bundleManager) Matcher() proxy.Matcher { return proxy.GemMatcher{} }

func (bundleManager) Setup(_ context.Context, in setupInput) (setupResult, error) {
	noop := setupResult{stop: func() {}}
	cwd, err := os.Getwd()
	if err != nil {
		return setupResult{}, fmt.Errorf("failed to get the working directory: %w", err)
	}
	dir := bundleAppConfigDir(in.parent, cwd)
	if dir == "" {
		return noop, nil
	}
	user, ok, err := readBundleLocalConfig(dir)
	if err != nil {
		return setupResult{}, err
	}
	if !ok {
		return noop, nil
	}
	cp, err := writeBundleConfigCopy(user, bundleUserCA(in.io, cwd, user), in.caPath)
	if err != nil {
		return setupResult{}, fmt.Errorf("failed to copy the local Bundler config: %w", err)
	}
	return setupResult{env: []string{"BUNDLE_APP_CONFIG=" + cp.appDir}, stop: func() { cp.save(in.io, user) }}, nil
}

type bundleLocalConfig struct {
	dir, path      string
	lines          []string
	userCA, caLine string
}

func readBundleLocalConfig(dir string) (bundleLocalConfig, bool, error) {
	c := bundleLocalConfig{dir: dir, path: filepath.Join(dir, "config")}
	raw, err := os.ReadFile(c.path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, false, nil
	}
	if err != nil {
		return c, false, fmt.Errorf("failed to read the local Bundler config: %w", err)
	}
	c.lines = strings.Split(string(raw), "\n")
	for _, line := range c.lines {
		if v, ok := bundleConfigCACert(line); ok {
			c.userCA, c.caLine = v, line
		}
	}
	return c, c.caLine != "", nil
}

func bundleUserCA(ios *iostreams.IOStreams, cwd string, c bundleLocalConfig) []byte {
	if c.userCA == "" {
		return nil
	}
	path := c.userCA
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	caPEM, err := os.ReadFile(path)
	if err != nil {
		ios.LogErrorf("warning: could not read BUNDLE_SSL_CA_CERT %q from %s: %v\n", c.userCA, c.path, err)
	}
	if len(caPEM) > 0 && caPEM[len(caPEM)-1] != '\n' {
		caPEM = append(caPEM, '\n')
	}
	return caPEM
}

type bundleConfigCopy struct {
	tmp, appDir, caFile string
	generated           []byte
}

func writeBundleConfigCopy(c bundleLocalConfig, userCA []byte, glabCAPath string) (bundleConfigCopy, error) {
	tmp, err := os.MkdirTemp("", "glab-df-bundle-*")
	if err != nil {
		return bundleConfigCopy{}, err
	}
	cp := bundleConfigCopy{tmp: tmp, appDir: filepath.Join(tmp, "bundle"), caFile: filepath.Join(tmp, "ca.pem")}
	if err := cp.fill(c, userCA, glabCAPath); err != nil {
		return bundleConfigCopy{}, errors.Join(err, os.RemoveAll(tmp))
	}
	return cp, nil
}

func (cp *bundleConfigCopy) fill(c bundleLocalConfig, userCA []byte, glabCAPath string) error {
	glabCA, err := os.ReadFile(glabCAPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(cp.caFile, append(userCA, glabCA...), 0o600); err != nil {
		return err
	}
	if err := os.Mkdir(cp.appDir, 0o700); err != nil {
		return err
	}
	copied := slices.Clone(c.lines)
	for i, line := range copied {
		if _, ok := bundleConfigCACert(line); ok {
			copied[i] = `BUNDLE_SSL_CA_CERT: "` + cp.caFile + `"`
		}
	}
	cp.generated = []byte(strings.Join(copied, "\n"))
	if err := os.WriteFile(filepath.Join(cp.appDir, "config"), cp.generated, 0o600); err != nil {
		return err
	}
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == "config" {
			continue
		}
		if err := os.Symlink(filepath.Join(c.dir, e.Name()), filepath.Join(cp.appDir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func (cp bundleConfigCopy) save(ios *iostreams.IOStreams, c bundleLocalConfig) {
	cp.saveConfig(ios, c)
	if !moveAddedEntries(ios, cp.appDir, c.dir) {
		_ = os.RemoveAll(cp.tmp)
	}
}

func (cp bundleConfigCopy) saveConfig(ios *iostreams.IOStreams, c bundleLocalConfig) {
	current, err := os.ReadFile(filepath.Join(cp.appDir, "config"))
	switch {
	case err != nil:
		ios.LogErrorf("warning: could not save Bundler's changes to %s: %v\n", c.path, err)
		return
	case bytes.Equal(current, cp.generated):
		return
	}
	restored := restoreBundleCALine(current, cp.caFile, c.caLine)
	if bytes.Contains(restored, []byte(filepath.Base(cp.tmp))) {
		ios.LogErrorf("warning: could not restore BUNDLE_SSL_CA_CERT in Bundler's changes to %s, so they were not saved\n", c.path)
		return
	}
	path := c.path
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	}
	if err := fsx.WriteOwnerOnly(path, restored); err != nil {
		ios.LogErrorf("warning: could not save Bundler's changes to %s: %v\n", c.path, err)
	}
}

func restoreBundleCALine(config []byte, caFile, caLine string) []byte {
	lines := strings.Split(string(config), "\n")
	for i, line := range lines {
		v, ok := bundleConfigCACert(line)
		if ok && (v == caFile || v == strings.ReplaceAll(caFile, `\`, `\\`)) {
			lines[i] = caLine
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

func moveAddedEntries(ios *iostreams.IOStreams, copyDir, userDir string) bool {
	entries, err := os.ReadDir(copyDir)
	if err != nil {
		ios.LogErrorf("warning: could not check %s for files Bundler added: %v\n", copyDir, err)
		return false
	}
	left := false
	for _, e := range entries {
		if e.Name() == "config" || e.Type()&fs.ModeSymlink != 0 {
			continue
		}
		src, dst := filepath.Join(copyDir, e.Name()), filepath.Join(userDir, e.Name())
		if err := os.Rename(src, dst); err != nil {
			if copyErr := copyEntry(src, dst); copyErr != nil {
				ios.LogErrorf("warning: could not save %s, which Bundler added to %s; it is still in %s: %v\n", e.Name(), userDir, src, errors.Join(err, copyErr))
				left = true
			} else {
				dbg.Debugf("dependency firewall: could not move %s (%v), copied it instead", src, err)
			}
		}
	}
	return left
}

func copyEntry(src, dst string) error {
	switch _, err := os.Lstat(dst); {
	case err == nil:
		return fmt.Errorf("%s: %w", dst, fs.ErrExist)
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		err = os.CopyFS(dst, os.DirFS(src))
	} else {
		var data []byte
		if data, err = os.ReadFile(src); err == nil {
			err = os.WriteFile(dst, data, info.Mode().Perm())
		}
	}
	if err != nil {
		return errors.Join(err, os.RemoveAll(dst))
	}
	return nil
}

func bundleAppConfigDir(env []string, cwd string) string {
	root := bundleRoot(env, cwd)
	if root == "" {
		return ""
	}
	v := lookupEnv(env, "BUNDLE_APP_CONFIG")
	switch {
	case v == "":
		return filepath.Join(root, ".bundle")
	case filepath.IsAbs(v):
		return v
	}
	return filepath.Join(root, v)
}

func bundleRoot(env []string, cwd string) string {
	if gemfile := lookupEnv(env, "BUNDLE_GEMFILE"); gemfile != "" {
		if !filepath.IsAbs(gemfile) {
			gemfile = filepath.Join(cwd, gemfile)
		}
		return filepath.Dir(gemfile)
	}
	for dir := cwd; ; {
		for _, name := range []string{"gems.rb", "Gemfile"} {
			if info, err := os.Stat(filepath.Join(dir, name)); err == nil && info.Mode().IsRegular() {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func bundleConfigCACert(line string) (string, bool) {
	value, ok := strings.CutPrefix(line, "BUNDLE_SSL_CA_CERT:")
	if !ok {
		return "", false
	}
	return strings.Trim(strings.TrimSpace(value), `"'`), true
}
