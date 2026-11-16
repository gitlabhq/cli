package pm

import (
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/dbg"
)

// mavenUpstreamProxy follows Maven 3.9's proxy selection (verified against
// Maven 3.9.16 and maven-resolver 1.9.27). Maven reads the <proxy> entries of
// the user settings file, then those of the global settings file whose id the
// user file does not use, and drops the inactive ones. For an https
// repository it takes the first https proxy whose nonProxyHosts do not match
// the host, else the first such http proxy. Other protocols and HTTPS_PROXY
// are ignored, and Maven always reaches the proxy itself over plain HTTP.
// It doesn't read .mvn/maven.config, or a settings file inside the project.
func mavenUpstreamProxy(environ []string, dir string, args []string) (upstreamProxy, bool, error) {
	// mvn's launcher puts MAVEN_ARGS ahead of the command line, and Maven
	// takes the first -s or -gs.
	cli := append(strings.Fields(lookupEnv(environ, "MAVEN_ARGS")), args...)
	props := mavenProperties(environ, cli)

	userFile, userSet := mavenOption(cli, "-s", "--settings", true)
	if !userSet {
		userFile = jvmJoin(javaUserHome(environ, props), ".m2", "settings.xml")
	}
	globalFile, globalSet := mavenOption(cli, "-gs", "--global-settings", false)
	if !globalSet {
		globalFile = mavenGlobalSettings(environ, props)
	}

	files := newConfigFiles(dir)
	var proxies []mavenProxy
	ids := map[string]struct{}{}
	for _, f := range []struct {
		path     string
		explicit bool
	}{{userFile, userSet}, {globalFile, globalSet}} {
		if f.path == "" {
			continue
		}
		fileProxies, err := readMavenSettings(files, f.path, f.explicit, environ, props)
		if err != nil {
			return upstreamProxy{}, false, err
		}
		var added []string
		for _, p := range fileProxies {
			if _, dup := ids[p.ID]; !dup {
				proxies = append(proxies, p)
				added = append(added, p.ID)
			}
		}
		for _, id := range added {
			ids[id] = struct{}{}
		}
	}

	var https, http []upstreamProxy
	for _, p := range proxies {
		if !p.active() {
			continue
		}
		protocol := strings.ToLower(p.Protocol)
		if protocol != "https" && protocol != "http" {
			continue
		}
		upstream, err := p.upstream()
		if err != nil {
			return upstreamProxy{}, false, fmt.Errorf("%s: %w", p.source, err)
		}
		if protocol == "https" {
			https = append(https, upstream)
		} else {
			http = append(http, upstream)
		}
	}
	candidates := slices.Concat(https, http)
	if len(candidates) == 0 {
		return upstreamProxy{}, false, nil
	}
	first := candidates[0]
	first.Fallbacks = candidates[1:]
	if len(first.Fallbacks) == 0 {
		first.Fallbacks = nil
	}
	return first, true, nil
}

// mavenProxy is a <proxy> entry of a Maven settings file, its values trimmed
// and interpolated as Maven does.
type mavenProxy struct {
	ID            string  `xml:"id"`
	Active        *string `xml:"active"`
	Protocol      string  `xml:"protocol"`
	Username      string  `xml:"username"`
	Password      string  `xml:"password"`
	Host          string  `xml:"host"`
	Port          *string `xml:"port"`
	NonProxyHosts string  `xml:"nonProxyHosts"`

	source string
}

// mavenEncryptedRe matches a value Maven passes to its settings decrypter,
// the pattern plexus-cipher 2.0 uses to recognise "{...}".
var mavenEncryptedRe = regexp.MustCompile(`\{.*?[^\\]\}`)

func (p mavenProxy) active() bool {
	return p.Active == nil || strings.EqualFold(*p.Active, "true")
}

func (p mavenProxy) upstream() (upstreamProxy, error) {
	port := "8080"
	if p.Port != nil {
		if _, err := strconv.ParseUint(*p.Port, 10, 16); err != nil {
			return upstreamProxy{}, errors.New("the proxy port is not a number")
		}
		port = *p.Port
	}
	if p.Host == "" {
		return upstreamProxy{}, errors.New("the proxy has no host")
	}
	u := url.URL{Scheme: "http", Host: net.JoinHostPort(p.Host, port)}
	if p.Username != "" || p.Password != "" {
		if mavenEncryptedRe.MatchString(p.Password) {
			return upstreamProxy{}, errors.New("the proxy password is encrypted, which glab does not support. Use the plain-text password, for example from an environment variable with ${env.NAME}")
		}
		u.User = url.UserPassword(p.Username, p.Password)
	}
	var nonProxyHosts []string
	for h := range strings.SplitSeq(p.NonProxyHosts, "|") {
		if h != "" {
			nonProxyHosts = append(nonProxyHosts, h)
		}
	}
	return upstreamProxy{Source: p.source, URL: u.String(), NonProxyHosts: nonProxyHosts}, nil
}

// readMavenSettings returns the <proxy> entries of the settings file at path,
// or none when it is missing or inside the project. A file named with -s or
// -gs must exist, as Maven requires.
func readMavenSettings(files configFiles, path string, explicit bool, environ []string, props map[string]string) ([]mavenProxy, error) {
	if explicit && !exists(path) {
		return nil, fmt.Errorf("the settings file %s does not exist", path)
	}
	data, ok, err := files.read(path)
	if !ok {
		return nil, err
	}
	return mavenProxiesIn(path, data, environ, props)
}

func mavenProxiesIn(path string, data []byte, environ []string, props map[string]string) ([]mavenProxy, error) {
	var settings struct {
		Proxies []struct {
			Proxy []mavenProxy `xml:"proxy"`
		} `xml:"proxies"`
	}
	if err := xml.Unmarshal(data, &settings); err != nil {
		return nil, parseError(path, err)
	}
	if len(settings.Proxies) > 1 {
		return nil, fmt.Errorf("failed to parse %s: duplicate <proxies> element", path)
	}
	if len(settings.Proxies) == 0 {
		return nil, nil
	}
	proxies := settings.Proxies[0].Proxy
	for i := range proxies {
		p := &proxies[i]
		// Maven reads a numeric or boolean value before it interpolates the
		// file, so only the string values expand ${...}.
		expand := func(s string) string { return mavenInterpolate(strings.TrimSpace(s), environ, props) }
		p.ID = strings.TrimSpace(p.ID)
		if p.ID == "" {
			p.ID = "default"
		}
		if p.Active != nil {
			*p.Active = strings.TrimSpace(*p.Active)
		}
		if p.Port != nil {
			*p.Port = strings.TrimSpace(*p.Port)
		}
		p.Protocol = expand(p.Protocol)
		if p.Protocol == "" {
			p.Protocol = "http"
		}
		p.Username = expand(p.Username)
		p.Password = expand(p.Password)
		p.Host = expand(p.Host)
		p.NonProxyHosts = expand(p.NonProxyHosts)
		p.source = fileSource(path, "proxy "+p.ID)
	}
	return proxies, nil
}

var mavenExprRe = regexp.MustCompile(`\$\{([^}]+)\}`)

// mavenInterpolate expands ${env.NAME}, ${user.home} and the -D properties in
// a settings value. Maven leaves an expression it cannot resolve as it is.
func mavenInterpolate(s string, environ []string, props map[string]string) string {
	return mavenExprRe.ReplaceAllStringFunc(s, func(expr string) string {
		name := expr[2 : len(expr)-1]
		if env, ok := strings.CutPrefix(name, "env."); ok {
			if v, ok := envValue(environ, env); ok {
				return v
			}
			return expr
		}
		if name == "user.home" {
			return javaUserHome(environ, props)
		}
		if v, ok := props[name]; ok {
			return v
		}
		return expr
	})
}

// jvmJoin joins names to dir as java.io.File does, without cleaning, so that
// configFiles.read applies a ".." after a symlink in dir as the OS does.
func jvmJoin(dir string, names ...string) string {
	return strings.TrimRight(dir, string(filepath.Separator)) + string(filepath.Separator) + filepath.Join(names...)
}

// mavenGlobalSettings returns conf/settings.xml of the Maven installation
// that runs: -Dmaven.conf, else the installation holding the mvn found on
// PATH. The mvn launcher derives its home from its own location and ignores
// MAVEN_HOME.
func mavenGlobalSettings(environ []string, props map[string]string) string {
	if conf, ok := props["maven.conf"]; ok {
		return jvmJoin(conf, "settings.xml")
	}
	bin, ok := lookPathIn(environ, "mvn")
	if !ok {
		dbg.Debugf("dependency firewall: not reading the maven global settings, as no mvn is on PATH")
		return ""
	}
	home, _, err := mavenHome(bin)
	if err != nil {
		dbg.Debugf("dependency firewall: not reading the maven global settings, as the installation of %s is unknown: %v", bin, err)
		return ""
	}
	return filepath.Join(home, "conf", "settings.xml")
}
