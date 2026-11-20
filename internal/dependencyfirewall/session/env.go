// Package session supports the job-level Dependency Firewall proxy
// (`glab dependency-firewall job-proxy`). Unlike the per-command package managers in
// package pm — each of which starts a proxy for the length of one command — a
// session runs one inspection proxy for a whole CI job and emits the
// environment that routes every tool through it and makes every tool trust it,
// without clobbering a caller's existing proxy or CA configuration.
package session

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/pm"
)

// routingManagers are the package managers whose own routing variables the
// session sets, so a manager that does not simply follow HTTP(S)_PROXY (pip
// reads PIP_PROXY, npm reads NPM_CONFIG_*, bundler reads BUNDLE_*) is still
// routed through the proxy. Trust is supplied once, additively, by trustVars —
// not per manager — so the managers' CATrustEnviron is deliberately not called
// here. Maven and Gradle are omitted: routing them needs a generated
// settings.xml and JVM trust store, which JVMTrustEnv writes instead of an
// environment variable.
func routingManagers() []pm.PackageManager {
	return []pm.PackageManager{
		pm.NPM(), pm.Pnpm(), pm.Yarn(),
		pm.Pip(), pm.Pipenv(), pm.Poetry(), pm.Uv(),
		pm.Bundle(), pm.Gem(),
		pm.Cargo(),
	}
}

// BuildJobEnv returns the environment, as "KEY=value" strings, that routes a
// job's tools through the proxy at proxyURL and makes them trust its MITM CA.
//
// Routing points HTTP(S)_PROXY (and the per-manager routing variables) at the
// proxy, and preserves the caller's NO_PROXY so their internal hosts still go
// direct, adding only loopback so local traffic is never proxied.
//
// Trust is additive: mergedBundlePath holds the system roots, the caller's
// existing anchors and the proxy CA, so pointing a tool's CA variable at it
// never drops a CA the tool trusted before. Installing the proxy CA into the
// system trust store — done separately by the command — covers everything
// that reads the system store (git, wget, OpenSSL, apt).
func BuildJobEnv(proxyURL, mergedBundlePath, callerNoProxy string) []string {
	vars := map[string]string{}

	// Routing. Both cases are set because tools disagree on which they read.
	// HTTP_PROXY is set too, so a plain-HTTP registry is checked rather than
	// fetched directly; the proxy forwards other plain-HTTP traffic unchanged.
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		vars[k] = proxyURL
	}
	noProxy := mergeNoProxy(callerNoProxy)
	vars["NO_PROXY"] = noProxy
	vars["no_proxy"] = noProxy

	for _, m := range routingManagers() {
		for _, kv := range m.Environment(proxyURL) {
			if k, v, ok := strings.Cut(kv, "="); ok {
				vars[k] = v
			}
		}
	}

	// Trust, all additive.
	maps.Copy(vars, trustVars(mergedBundlePath))

	env := make([]string, 0, len(vars))
	for k, v := range vars {
		env = append(env, k+"="+v)
	}
	sort.Strings(env)
	return env
}

// trustVars are the only trust variables the job gets. Each points at the
// merged bundle, which adds the proxy CA to what a tool already trusted (see
// WriteMergedBundle) rather than replacing it.
func trustVars(mergedBundlePath string) map[string]string {
	return map[string]string{
		// Node adds these anchors to its built-in roots. The merged bundle keeps
		// a caller's own NODE_EXTRA_CA_CERTS, which this replaces. NODE_USE_SYSTEM_CA
		// makes newer Node also honor the system store the command installs into.
		"NODE_EXTRA_CA_CERTS": mergedBundlePath,
		"NODE_USE_SYSTEM_CA":  "1",
		// requests/certifi ignore the system store, so they get the merged
		// bundle; pip reads REQUESTS_CA_BUNDLE and also honors PIP_CERT.
		"REQUESTS_CA_BUNDLE": mergedBundlePath,
		"PIP_CERT":           mergedBundlePath,
		// uv verifies with the system store rather than its bundled roots.
		"UV_NATIVE_TLS": "true",
		// cargo and git read their own CA file variables.
		"CARGO_HTTP_CAINFO": mergedBundlePath,
		"GIT_SSL_CAINFO":    mergedBundlePath,
		// curl prefers CURL_CA_BUNDLE over the system store, and without it curl
		// trusts the proxy only if the system-store install (root only) worked.
		"CURL_CA_BUNDLE": mergedBundlePath,
		// RubyGems reads OpenSSL's SSL_CERT_FILE and Bundler its own variable;
		// without them, gem and bundle trust the proxy only if the system-store
		// install succeeded, which needs root.
		"SSL_CERT_FILE":      mergedBundlePath,
		"BUNDLE_SSL_CA_CERT": mergedBundlePath,
	}
}

// mergeNoProxy keeps the caller's NO_PROXY entries and ensures loopback is
// present, so the job's own local traffic is never routed through the proxy
// while the caller's internal hosts still go direct.
func mergeNoProxy(callerNoProxy string) string {
	seen := map[string]struct{}{}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		if _, dup := seen[s]; dup {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for e := range strings.SplitSeq(callerNoProxy, ",") {
		add(e)
	}
	for _, e := range []string{"localhost", "127.0.0.1", "::1"} {
		add(e)
	}
	return strings.Join(out, ",")
}

// WriteMergedBundle writes a CA bundle containing the system roots, any
// caller-supplied CA bundles (userBundlePaths), and the proxy CA at
// proxyCAPath, to stateDir/ca-bundle.pem, and returns its path. Tools that
// replace their trust from a single bundle (Python requests, cargo) can be
// pointed at it without losing the roots they had, which is what makes the
// proxy's trust additive rather than a replacement. A system or caller bundle
// that can't be read is reported through logf, because the tools pointed at
// the merged bundle lose those roots.
func WriteMergedBundle(stateDir, proxyCAPath string, userBundlePaths []string, logf func(string, ...any)) (string, error) {
	var bundle []byte
	appendPEM := func(b []byte) {
		if len(b) == 0 {
			return
		}
		bundle = append(bundle, b...)
		if bundle[len(bundle)-1] != '\n' {
			bundle = append(bundle, '\n')
		}
	}

	foundSystem := false
	for _, p := range pm.SystemCABundlePaths {
		if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
			appendPEM(b)
			foundSystem = true
			break
		}
	}
	if !foundSystem {
		logf("dependency firewall: no system CA bundle found; tools that use the merged bundle will trust only the proxy and your own CA bundles\n")
	}
	seen := map[string]struct{}{}
	for _, p := range userBundlePaths {
		if p == "" {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		b, err := os.ReadFile(p)
		if err != nil {
			logf("dependency firewall: could not read CA bundle %s, so it is left out of the merged bundle: %v\n", p, err)
			continue
		}
		appendPEM(b)
	}
	if b, err := os.ReadFile(proxyCAPath); err == nil {
		appendPEM(b)
	} else {
		return "", fmt.Errorf("reading proxy CA at %s: %w", proxyCAPath, err)
	}

	path := filepath.Join(stateDir, "ca-bundle.pem")
	if err := os.WriteFile(path, bundle, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// writeCAPEM writes the proxy CA certificate to path as a single PEM block.
func writeCAPEM(path string, ca *x509.Certificate) error {
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw}), 0o644)
}

// callerBundleEnvVars names the environment variables whose values point at a
// caller's existing CA bundle, so WriteMergedBundle can fold those anchors in.
var callerBundleEnvVars = []string{
	"REQUESTS_CA_BUNDLE", "SSL_CERT_FILE", "PIP_CERT",
	"NODE_EXTRA_CA_CERTS", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO", "CARGO_HTTP_CAINFO",
	"BUNDLE_SSL_CA_CERT",
}

// CallerBundlePaths returns the non-empty values of callerBundleEnvVars from
// env ("KEY=value" strings), for seeding WriteMergedBundle.
func CallerBundlePaths(env []string) []string {
	want := map[string]struct{}{}
	for _, k := range callerBundleEnvVars {
		want[k] = struct{}{}
	}
	var out []string
	for _, e := range env {
		if k, v, ok := strings.Cut(e, "="); ok && v != "" {
			if _, ok := want[k]; ok {
				out = append(out, v)
			}
		}
	}
	return out
}

// systemTrustTarget is an OS trust-store anchor directory and the tool that
// rebuilds the store after a certificate is added to or removed from it.
type systemTrustTarget struct {
	anchorDir string
	tool      string
	toolArgs  []string
}

func systemTrustTargets() []systemTrustTarget {
	return []systemTrustTarget{
		{anchorDir: "/usr/local/share/ca-certificates", tool: "update-ca-certificates"},                         // Debian, Ubuntu, Alpine
		{anchorDir: "/etc/pki/ca-trust/source/anchors", tool: "update-ca-trust", toolArgs: []string{"extract"}}, // Fedora, RHEL
	}
}

// systemTrustAnchorName is the per-run file the proxy CA is staged under in an
// anchor directory. It includes the state dir's base name (unique per run) so
// concurrent jobs on one host do not overwrite each other's CA, and so stop can
// remove exactly the file its own start added instead of a shared fixed name.
func systemTrustAnchorName(stateDir string) string {
	return "glab-df-proxy-" + filepath.Base(stateDir) + ".crt"
}

// InstallSystemTrust installs the proxy CA at proxyCAPath into the OS trust
// store, so tools that read it (curl, git, wget, OpenSSL, apt) trust the proxy
// without any per-tool variable. It is best-effort: on an unsupported image or
// without privilege it logs and returns, leaving the env-var bundles as the
// fallback. It reports whether the install succeeded.
func InstallSystemTrust(stateDir, proxyCAPath string, logf func(string, ...any)) bool {
	for _, t := range systemTrustTargets() {
		if !dirExists(t.anchorDir) {
			continue
		}
		toolPath, err := exec.LookPath(t.tool)
		if err != nil {
			continue
		}
		dst := filepath.Join(t.anchorDir, systemTrustAnchorName(stateDir))
		if err := copyFile(proxyCAPath, dst); err != nil {
			logf("dependency firewall: could not stage proxy CA at %s: %v\n", dst, err)
			continue
		}
		if out, err := exec.Command(toolPath, t.toolArgs...).CombinedOutput(); err != nil {
			logf("dependency firewall: %s failed: %v: %s\n", t.tool, err, strings.TrimSpace(string(out)))
			continue
		}
		logf("dependency firewall: installed proxy CA into the system trust store via %s\n", t.tool)
		return true
	}
	logf("dependency firewall: could not install the proxy CA into a system trust store; tools that only read it (for example wget and apt) may not trust the proxy\n")
	return false
}

// RemoveSystemTrust removes the per-run proxy CA that InstallSystemTrust staged
// for stateDir from every anchor directory and rebuilds the store, so a job's CA
// does not linger in the host trust store after stop. It is best-effort and, like
// InstallSystemTrust, stays silent when there was nothing to remove.
func RemoveSystemTrust(stateDir string, logf func(string, ...any)) {
	for _, t := range systemTrustTargets() {
		if !dirExists(t.anchorDir) {
			continue
		}
		dst := filepath.Join(t.anchorDir, systemTrustAnchorName(stateDir))
		if err := os.Remove(dst); err != nil {
			if !os.IsNotExist(err) {
				logf("dependency firewall: could not remove proxy CA at %s: %v\n", dst, err)
			}
			continue
		}
		toolPath, err := exec.LookPath(t.tool)
		if err != nil {
			continue
		}
		if out, err := exec.Command(toolPath, t.toolArgs...).CombinedOutput(); err != nil {
			logf("dependency firewall: %s failed: %v: %s\n", t.tool, err, strings.TrimSpace(string(out)))
		}
	}
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

// ShellExports renders env ("KEY=value" strings) as POSIX `export` lines that a
// shell can eval. Values are single-quoted with embedded single quotes escaped,
// so a value with spaces or shell metacharacters is passed through verbatim.
func ShellExports(env []string) string {
	var b strings.Builder
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "export %s=%s\n", k, singleQuote(v))
	}
	return b.String()
}

func singleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
