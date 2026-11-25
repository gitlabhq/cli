package pm

import (
	"bytes"
	"crypto/x509"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/proxy"
)

// mavenManager routes mvn through the proxy via a settings.xml <proxy> block,
// since Maven ignores HTTP(S)_PROXY, merged into the caller's own settings.
type mavenManager struct {
	proxyURL string
	args     []string
	// mirrors is shared with the matcher Run hands the proxy before
	// CATrustEnviron runs, and filled once the settings Maven reads are known.
	mirrors  *proxy.MavenMirrors
	lookPath func(file string) (string, error)
}

// Maven returns a fresh maven package manager (it stashes the proxy URL from
// Environment for CATrustEnviron to build the settings.xml and truststore).
func Maven() PackageManager {
	return &mavenManager{mirrors: &proxy.MavenMirrors{}, lookPath: exec.LookPath}
}

func (m *mavenManager) Name() string   { return "maven" }
func (m *mavenManager) Binary() string { return "mvn" }

// SetArgs stashes the mvn arguments so CATrustEnviron can merge the caller's
// settings file into the firewall's.
func (m *mavenManager) SetArgs(args []string) { m.args = args }

// userSettingsFile resolves the settings.xml mvn would read on its own.
func (m *mavenManager) userSettingsFile() string {
	dir, err := os.Getwd()
	if err != nil {
		dbg.Debugf("dependency firewall: could not get the working directory to resolve the maven settings file: %v; treating the caller as having no settings file", err)
		return ""
	}
	return mavenUserSettingsFile(os.Environ(), dir, m.args)
}

// Environment stashes the proxy URL; the settings.xml and truststore are
// emitted from CATrustEnviron (called last by Run, after the CA bundle
// exists) so both the routing and the trust anchor land together.
func (m *mavenManager) Environment(proxyURL string) []string {
	m.proxyURL = proxyURL
	return nil
}

func (m *mavenManager) CATrustEnviron(caPath string) []string {
	var env []string

	// Routing: a settings.xml <proxy> block. Written next to the CA bundle so
	// Run, which removes caPath and its siblings, cleans it up too. Maven
	// ignores HTTP(S)_PROXY and the JVM proxy properties, so this file is the
	// only thing routing mvn through the proxy: surface a failure via dbg so a
	// silent firewall bypass is diagnosable rather than fail-open unnoticed.
	settingsPath, userSettings, ok := writeMavenProxySettings(caPath+".settings.xml", m.proxyURL, m.userSettingsFile(), os.Environ())
	if !ok {
		dbg.Debugf("dependency firewall: failed to write maven proxy settings.xml at %s; maven traffic will not be routed through the proxy", caPath+".settings.xml")
	} else {
		args := "-s " + settingsPath
		if global, ok := m.pinGlobalSettings(userSettings, caPath+".global-settings.xml"); ok {
			args += " -gs " + global
		}
		env = append(env, mavenArgsEnv(args))
	}

	// CA trust: a PKCS#12 truststore folded into MAVEN_OPTS.
	if opts := JVMTrustOpts(caPath); opts != "" {
		env = append(env, "MAVEN_OPTS="+opts)
	}

	return env
}

// ExistingBundleVars is empty: Maven trusts the proxy through a generated JVM
// truststore (MAVEN_OPTS), not a user-provided CA-bundle environment variable,
// so there is nothing for the engine to prepend.
func (m *mavenManager) ExistingBundleVars() []string { return nil }

// CleanupCAFiles removes the settings files and PKCS#12 truststore this
// manager writes next to the CA bundle in CATrustEnviron.
func (m *mavenManager) CleanupCAFiles(caPath string) {
	_ = os.Remove(caPath + ".settings.xml")
	_ = os.Remove(caPath + ".global-settings.xml")
	_ = os.Remove(caPath + ".p12")
}

// upstreamTrustAnchors returns the certificates in the truststore mvn's JVM
// would load. glab replaces that truststore for the child, so the user's
// anchors move to the registry side. Maven 3's launcher passes jvm.config
// before MAVEN_OPTS and MAVEN_DEBUG_OPTS, and Maven 4's passes it after
// MAVEN_OPTS; this follows Maven 3. The JVM reads JAVA_TOOL_OPTIONS first,
// the java launcher puts JDK_JAVA_OPTIONS before the command line, and
// _JAVA_OPTIONS comes last. A -D option in mvn's own arguments fills only a
// property those leave unset, as on Maven 4: on Maven 3 it replaces glab's
// truststore, so the run fails whatever registry connections trust. The
// error also reports a truststore option in MAVEN_DEBUG_OPTS, _JAVA_OPTIONS
// or mvn's own arguments, which replaces glab's in the child.
func (m *mavenManager) upstreamTrustAnchors(env []string, dir string, args []string) ([]*x509.Certificate, error) {
	argProps := mavenArgProperties(env, dir, args)
	certs, err := jvmTrustAnchors(env, dir, mavenTrustProps(env, dir, args, argProps))
	return certs, errors.Join(err,
		jvmTrustStoreOverride(env, "mvn", "MAVEN_OPTS", "MAVEN_DEBUG_OPTS", "_JAVA_OPTIONS"),
		argTrustStoreOverride(argProps, "-D%s in the mvn arguments replaces glab's truststore on Maven 3, so mvn cannot trust the dependency firewall proxy there; set it in MAVEN_OPTS instead"))
}

func mavenTrustProps(env []string, dir string, args []string, argProps map[string]string) map[string]string {
	return jvmTrustProps(argProps,
		lookupEnv(env, "JAVA_TOOL_OPTIONS"), lookupEnv(env, "JDK_JAVA_OPTIONS"), mavenJVMConfig(env, dir, args),
		lookupEnv(env, "MAVEN_OPTS"), lookupEnv(env, "MAVEN_DEBUG_OPTS"), lookupEnv(env, "_JAVA_OPTIONS"))
}

func (m *mavenManager) Matcher() proxy.Matcher { return proxy.MavenMatcher{Mirrors: m.mirrors} }

// pinGlobalSettings lets the matcher read downloads exactly when the user and
// global settings name every mirror Maven can route through, a catch-all
// among them. user is the caller's file, or nil when glab used its own, which
// has no mirrors. It writes the global settings it read to path for Maven's
// -gs: Maven takes the first -gs, so it reads exactly what glab checked.
func (m *mavenManager) pinGlobalSettings(user []byte, path string) (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		dbg.Debugf("dependency firewall: could not get the working directory: %v; guessing maven mirror prefixes", err)
		return "", false
	}
	global, err := m.globalSettings(dir)
	if err != nil {
		dbg.Debugf("dependency firewall: could not read the maven global settings: %v; guessing maven mirror prefixes", err)
		return "", false
	}
	global = mavenExpandEnvURLs(global, os.Environ())
	urls, externalOnly, ok := mavenKnownMirrors(user, global)
	if !ok {
		dbg.Debugf("dependency firewall: the maven settings don't name a known catch-all mirror; guessing maven mirror prefixes")
		return "", false
	}
	// Maven fails on a -gs file that doesn't exist, so an absent one becomes empty.
	if global == nil {
		global = []byte("<settings/>\n")
	}
	if err := os.WriteFile(path, global, 0o600); err != nil {
		dbg.Debugf("dependency firewall: could not write the maven global settings for -gs: %v; guessing maven mirror prefixes", err)
		return "", false
	}
	m.mirrors.Set(urls, externalOnly)
	return path, true
}

// globalSettings returns the global settings.xml Maven reads, or nil when
// there is none.
func (m *mavenManager) globalSettings(dir string) ([]byte, error) {
	mvn, err := m.lookPath(m.Binary())
	if err != nil {
		return nil, fmt.Errorf("could not find %s: %w", m.Binary(), err)
	}
	path, err := mavenGlobalSettingsFile(os.Environ(), dir, m.args, mvn)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return raw, err
}

// mavenSettingsError describes err for the debug log without the XML
// decoder's message, which can quote the settings file, a password included.
func mavenSettingsError(err error) string {
	var syntax *xml.SyntaxError
	if errors.As(err, &syntax) {
		return fmt.Sprintf("XML syntax error on line %d", syntax.Line)
	}
	return err.Error()
}

// mavenArgsEnv prepends extra to any inherited MAVEN_ARGS. Maven takes the
// first occurrence of a repeated option, and its launcher expands MAVEN_ARGS
// ahead of the command line, so putting the firewall's -s first is what makes
// it win over a settings file the caller passed either way.
func mavenArgsEnv(extra string) string {
	if existing := os.Getenv("MAVEN_ARGS"); existing != "" {
		return "MAVEN_ARGS=" + extra + " " + existing
	}
	return "MAVEN_ARGS=" + extra
}

// MavenJobArgsEnv returns the MAVEN_ARGS entry that routes every mvn in a job
// through the proxy at proxyURL. It writes, at path, the install's global
// settings with the proxy entries merged in and passes them as -gs, so the
// image's global mirrors still apply and a job's own -s still layers on top.
// When the global settings can't be resolved or merged, the file holds only
// the proxy entries and warnf says why, because the install's mirrors,
// proxies and credentials then stop applying. No mvn on PATH is not reported:
// it is the normal case for a job that doesn't use Maven.
func MavenJobArgsEnv(path, proxyURL string, warnf func(format string, args ...any)) (string, error) {
	global, err := mavenJobGlobalSettings()
	if err != nil {
		warnf("dependency firewall: could not resolve the maven global settings (%v); mvn will not use the install's conf/settings.xml\n", err)
	}
	settings, merged, ok := writeMavenProxySettings(path, proxyURL, global, os.Environ())
	if !ok {
		return "", fmt.Errorf("could not write maven proxy settings at %s", path)
	}
	if global != "" && merged == nil {
		warnf("dependency firewall: could not merge the proxy into %s; mvn will not use it\n", global)
	}
	// The firewall proxy goes into the global (-gs) settings so a job's own -s
	// still layers on top, but Maven merges user settings over global ones and
	// uses the first active <proxy> per protocol, with user proxies winning. A
	// <proxy> already in the user settings therefore routes mvn around the
	// firewall. We cannot merge ours in first without seeing a job's future -s,
	// so warn rather than silently leave mvn unproxied.
	dir, _ := os.Getwd()
	if mavenUserSettingsHasActiveProxy(mavenUserSettingsFile(os.Environ(), dir, nil)) {
		warnf("dependency firewall: your Maven user settings define a <proxy> that Maven prefers over the firewall's global (-gs) settings; mvn may bypass the firewall\n")
	}
	return mavenArgsEnv("-gs " + settings), nil
}

// mavenUserSettingsHasActiveProxy reports whether the user settings file at
// settingsPath defines a <proxy> that is not explicitly inactive. An omitted
// <active> counts as active, so the check never misses one. Parse errors and a
// missing file report false: this only drives an advisory warning.
func mavenUserSettingsHasActiveProxy(settingsPath string) bool {
	if settingsPath == "" {
		return false
	}
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		return false
	}
	var parsed struct {
		Proxies []struct {
			Active string `xml:"active"`
		} `xml:"proxies>proxy"`
	}
	if err := xml.Unmarshal(raw, &parsed); err != nil {
		return false
	}
	for _, p := range parsed.Proxies {
		if !strings.EqualFold(strings.TrimSpace(p.Active), "false") {
			return true
		}
	}
	return false
}

// mavenJobGlobalSettings returns the global settings file the mvn on PATH
// reads, or "" when there is no mvn on PATH or the install has none.
func mavenJobGlobalSettings() (string, error) {
	mvn, err := exec.LookPath("mvn")
	if err != nil {
		return "", nil //nolint:nilerr // no mvn on PATH: nothing to preserve
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	path, err := mavenGlobalSettingsFile(os.Environ(), dir, nil, mvn)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return path, nil
}

// writeMavenProxySettings writes the settings.xml that routes mvn through the
// proxy at proxyURL and returns its path. When userPath names a readable
// settings file, the <proxy> entries are merged into a copy of it so the
// caller's mirrors, credentials and profiles still apply, with the variables
// in its repository URLs expanded from environ; otherwise the file holds only
// those entries. It also returns the caller's settings when they were merged,
// and ok=false when proxyURL cannot be parsed.
func writeMavenProxySettings(path, proxyURL, userPath string, environ []string) (string, []byte, bool) {
	u, err := url.Parse(proxyURL)
	if err != nil || u.Hostname() == "" || u.Port() == "" {
		return "", nil, false
	}
	proxies := mavenProxyElem("df-https", "https", u.Hostname(), u.Port()) +
		mavenProxyElem("df-http", "http", u.Hostname(), u.Port())

	var settings []byte
	if userPath != "" {
		settings, err = mergeMavenProxies(userPath, proxies)
		if err != nil {
			dbg.Debugf("dependency firewall: could not merge the maven settings at %s: %s; using the firewall's own settings instead", userPath, mavenSettingsError(err))
		}
		settings = mavenExpandEnvURLs(settings, environ)
	}
	merged := settings
	if settings == nil {
		// No usable settings to merge into. Routing the traffic matters more
		// than carrying over a file we could not use, so fall back to a
		// standalone settings.xml rather than leaving mvn unproxied.
		settings = []byte("<settings xmlns=\"http://maven.apache.org/SETTINGS/1.0.0\">\n" +
			"  <proxies>\n" + proxies + "  </proxies>\n" +
			"</settings>\n")
	}
	if err := os.WriteFile(path, settings, 0o600); err != nil {
		return "", nil, false
	}
	return path, merged, true
}

// mergeMavenProxies returns the settings file at path with proxies inserted as
// the first entries of its <proxies> element, creating that element when the
// file has none. The rest of the document is copied byte for byte: re-encoding
// it would rewrite namespaces and self-closing tags, which can break a
// settings file that works today. Inserting first matters because Maven uses
// the first active proxy for a protocol, so the firewall's entry has to precede
// any the caller already had.
func mergeMavenProxies(path, proxies string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	start, end, insert, err := mavenProxiesInsertion(raw, proxies)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(raw)+len(insert))
	out = append(out, raw[:start]...)
	out = append(out, insert...)
	return append(out, raw[end:]...), nil
}

// mavenProxiesInsertion parses raw far enough to find where proxies belongs
// and returns the byte range to replace with the returned text: an empty range
// just inside an existing <proxies>, the "/>" of a self-closed <proxies/>, or
// an empty range before </settings> when the document has no <proxies> at all.
// Offsets come from the XML parser rather than a text search, so a <proxies>
// inside a comment or a CDATA section cannot be mistaken for the real one.
func mavenProxiesInsertion(raw []byte, proxies string) (int, int, string, error) {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	depth, prevEnd := 0, 0
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return 0, 0, "", errors.New("no <settings> element")
		}
		if err != nil {
			return 0, 0, "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth == 2 && t.Name.Local == "proxies" {
				at := int(dec.InputOffset())
				// Reopen a self-closed <proxies/> rather than discarding the
				// document: falling back would lose the caller's whole file.
				if bytes.HasSuffix(raw[:at], []byte("/>")) {
					return at - 2, at, ">\n" + proxies + "  </proxies>", nil
				}
				return at, at, proxies, nil
			}
		case xml.EndElement:
			if depth == 1 && t.Name.Local == "settings" {
				// A self-closed <settings/> reports an end element at the same
				// offset as its start, so prevEnd would sit outside the root.
				if !bytes.HasPrefix(bytes.TrimLeft(raw[prevEnd:], " \t\r\n"), []byte("</")) {
					return 0, 0, "", errors.New("self-closed <settings/> element")
				}
				return prevEnd, prevEnd, "  <proxies>\n" + proxies + "  </proxies>\n", nil
			}
			depth--
		}
		prevEnd = int(dec.InputOffset())
	}
}

// mavenRepositoryElems are the settings.xml elements Maven resolves artifacts
// from, each holding the <url> it fetches from.
var mavenRepositoryElems = [][]string{
	{"settings", "mirrors", "mirror"},
	{"settings", "profiles", "profile", "repositories", "repository"},
	{"settings", "profiles", "profile", "pluginRepositories", "pluginRepository"},
}

var mavenEnvExpression = regexp.MustCompile(`\$\{env\.[^}]*\}`)

// mavenExpandEnvURLs expands ${env.NAME} in raw's mirror and profile repository
// URLs from environ, so Maven fetches from the URL glab checks. A URL it can't
// expand fully, and raw that doesn't parse, are left as they are.
func mavenExpandEnvURLs(raw []byte, environ []string) []byte {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	var path []string
	var text strings.Builder
	var out []byte
	copied, start, pieces := 0, 0, 0
	for {
		end := int(dec.InputOffset())
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			if out == nil {
				return raw
			}
			return append(out, raw[copied:]...)
		}
		if err != nil {
			return raw
		}
		switch t := tok.(type) {
		case xml.StartElement:
			path = append(path, t.Name.Local)
			text.Reset()
			start, pieces = int(dec.InputOffset()), 0
		case xml.CharData:
			text.Write(t)
			pieces++
		case xml.EndElement:
			// A URL in several pieces of text, around a comment or a CDATA
			// section, is left alone.
			if pieces == 1 && t.Name.Local == "url" && mavenIsRepository(path[:len(path)-1]) {
				if u, ok := mavenExpandEnv(strings.TrimSpace(text.String()), environ); ok {
					var escaped bytes.Buffer
					if err := xml.EscapeText(&escaped, []byte(u)); err != nil {
						return raw
					}
					out = append(append(out, raw[copied:start]...), escaped.Bytes()...)
					copied = end
				}
			}
			path = path[:len(path)-1]
		}
	}
}

// mavenExpandEnv expands every ${env.NAME} in s from environ. ok is false when
// s has no expression, holds another kind, or names a variable that is unset or
// empty, and when a value holds an expression Maven would expand again.
func mavenExpandEnv(s string, environ []string) (string, bool) {
	if !strings.Contains(s, "${") {
		return "", false
	}
	ok := true
	expanded := mavenEnvExpression.ReplaceAllStringFunc(s, func(expr string) string {
		value := lookupEnv(environ, expr[len("${env."):len(expr)-1])
		if value == "" {
			ok = false
		}
		return value
	})
	if !ok || strings.Contains(expanded, "${") {
		return "", false
	}
	return expanded, true
}

func mavenIsRepository(path []string) bool {
	return slices.ContainsFunc(mavenRepositoryElems, func(p []string) bool { return slices.Equal(p, path) })
}

func mavenProxyElem(id, protocol, host, port string) string {
	return "    <proxy>\n" +
		"      <id>" + id + "</id>\n" +
		"      <active>true</active>\n" +
		"      <protocol>" + protocol + "</protocol>\n" +
		"      <host>" + host + "</host>\n" +
		"      <port>" + port + "</port>\n" +
		"    </proxy>\n"
}
