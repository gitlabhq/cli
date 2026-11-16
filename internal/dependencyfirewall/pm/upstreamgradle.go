package pm

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/dbg"
)

// gradleUpstreamProxy follows how Gradle sets the JVM proxy properties
// (verified against Gradle 9.8.2). Each of https.proxyHost, proxyPort,
// proxyUser, proxyPassword and http.nonProxyHosts, which the JVM also applies
// to https, comes from the first of: -D on the command line, then
// systemProp.* in gradle.properties in the Gradle user home and in the Gradle
// installation. An empty https.proxyHost means a direct connection. The JVM
// ignores HTTPS_PROXY. It doesn't read the project's gradle.properties, or a
// user home or installation inside the project.
func gradleUpstreamProxy(environ []string, dir string, args []string) (upstreamProxy, bool, error) {
	cliProps := map[string]configSetting{}
	for k, v := range jvmDefines(gradleSystemPropArgs(args)) {
		cliProps[k] = configSetting{source: "-D" + k, value: v}
	}

	files := newConfigFiles(dir)
	layers := configLayers{cliProps}
	for _, file := range gradlePropertiesFiles(environ, dir, args, cliProps) {
		layer, err := readGradleProperties(files, file)
		if err != nil {
			return upstreamProxy{}, false, err
		}
		layers = append(layers, layer)
	}

	host, ok := layers.get("https.proxyHost")
	if !ok {
		return upstreamProxy{}, false, nil
	}
	if host.value == "" {
		return upstreamProxy{Source: host.source}, true, nil
	}
	port := "443"
	if s, ok := layers.get("https.proxyPort"); ok {
		if _, err := strconv.ParseUint(s.value, 10, 16); err != nil {
			return upstreamProxy{}, false, fmt.Errorf("%s: https.proxyPort is not a number", s.source)
		}
		port = s.value
	}
	u := url.URL{Scheme: "http", Host: net.JoinHostPort(host.value, port)}
	proxyUser, hasUser := layers.get("https.proxyUser")
	if hasUser && proxyUser.value != "" {
		password, _ := layers.get("https.proxyPassword")
		u.User = url.UserPassword(proxyUser.value, password.value)
	}
	upstream := upstreamProxy{Source: host.source, URL: u.String()}
	if s, ok := layers.get("http.nonProxyHosts"); ok {
		upstream.NonProxyHosts = jvmNonProxyHosts(s.value)
	}
	return upstream, true, nil
}

// gradlePropertiesFiles lists the gradle.properties files of the Gradle user
// home and installation, highest precedence first.
func gradlePropertiesFiles(environ []string, dir string, args []string, cliProps map[string]configSetting) []string {
	userHome := gradleUserHomeArg(args)
	if userHome == "" {
		var set bool
		if s, ok := cliProps["gradle.user.home"]; ok {
			userHome, set = s.value, true
		} else {
			userHome, set = envValue(environ, "GRADLE_USER_HOME")
		}
		switch {
		case !set:
			userHome = jvmJoin(javaUserHome(environ, nil), ".gradle")
		case userHome == "":
			// Gradle takes an empty user home as the working directory
			// (verified against Gradle 9.4).
			userHome = dir
		}
	}
	files := []string{jvmJoin(userHome, "gradle.properties")}
	bin, ok := lookPathIn(environ, "gradle")
	if !ok {
		dbg.Debugf("dependency firewall: not reading the gradle installation's gradle.properties, as no gradle is on PATH")
		return files
	}
	resolved, err := filepath.EvalSymlinks(bin)
	if err != nil {
		dbg.Debugf("dependency firewall: not reading the gradle installation's gradle.properties, as the installation of %s is unknown: %v", bin, err)
		return files
	}
	return append(files, filepath.Join(filepath.Dir(filepath.Dir(resolved)), "gradle.properties"))
}

// gradleUserHomeArg returns the value of the last -g in args, as "-g dir",
// "--gradle-user-home dir" or "--gradle-user-home=dir".
func gradleUserHomeArg(args []string) string {
	const long = "--gradle-user-home"
	value := ""
	for i, a := range args {
		switch {
		case (a == "-g" || a == long) && i+1 < len(args):
			value = args[i+1]
		case strings.HasPrefix(a, long+"="):
			value = a[len(long)+1:]
		}
	}
	return value
}

// gradleSystemPropArgs returns the -D arguments of a Gradle command line,
// rewriting "--system-prop k=v" and "-D k=v" as "-Dk=v".
func gradleSystemPropArgs(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case (a == "-D" || a == "--system-prop") && i+1 < len(args):
			i++
			out = append(out, "-D"+args[i])
		case strings.HasPrefix(a, "--system-prop="):
			out = append(out, "-D"+a[len("--system-prop="):])
		case strings.HasPrefix(a, "-D"):
			out = append(out, a)
		}
	}
	return out
}

// jvmDefines returns the system properties set with -D in args, the last one
// winning as in the JVM.
func jvmDefines(args []string) map[string]string {
	props := map[string]string{}
	for _, a := range args {
		kv, ok := strings.CutPrefix(a, "-D")
		if !ok {
			continue
		}
		k, v, _ := strings.Cut(kv, "=")
		props[k] = v
	}
	return props
}

// readGradleProperties returns the systemProp.* entries of a gradle.properties
// file, keyed by the system property they set.
func readGradleProperties(files configFiles, path string) (map[string]configSetting, error) {
	data, ok, err := files.read(path)
	if !ok {
		return nil, err
	}
	props, err := parseJavaProperties(string(data))
	if err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	layer := map[string]configSetting{}
	for k, v := range props {
		if name, ok := strings.CutPrefix(k, "systemProp."); ok {
			layer[name] = configSetting{source: fileSource(path, k), value: v}
		}
	}
	return layer, nil
}

// parseJavaProperties parses the java.util.Properties format: "#" and "!"
// comments, "=", ":" or whitespace between key and value, a trailing
// backslash continuing the line, and backslash escapes including \uXXXX.
// Leading whitespace of a value is dropped and trailing whitespace kept.
func parseJavaProperties(data string) (map[string]string, error) {
	props := map[string]string{}
	lines := strings.Split(strings.ReplaceAll(data, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimLeft(lines[i], " \t\f")
		if line == "" || line[0] == '#' || line[0] == '!' {
			continue
		}
		for continues(line) && i+1 < len(lines) {
			i++
			line = line[:len(line)-1] + strings.TrimLeft(lines[i], " \t\f")
		}
		if continues(line) {
			line = line[:len(line)-1]
		}
		keyEnd := len(line)
		for j := 0; j < len(line); j++ {
			if line[j] == '\\' {
				j++
				continue
			}
			if strings.IndexByte("=: \t\f", line[j]) >= 0 {
				keyEnd = j
				break
			}
		}
		rest := strings.TrimLeft(line[keyEnd:], " \t\f")
		if rest != "" && (rest[0] == '=' || rest[0] == ':') {
			rest = strings.TrimLeft(rest[1:], " \t\f")
		}
		key, err := javaUnescape(line[:keyEnd])
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		value, err := javaUnescape(rest)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		props[key] = value
	}
	return props, nil
}

// continues reports whether line ends in an odd number of backslashes.
func continues(line string) bool {
	n := len(line) - len(strings.TrimRight(line, `\`))
	return n%2 == 1
}

func javaUnescape(s string) (string, error) {
	if !strings.Contains(s, `\`) {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 == len(s) {
			b.WriteByte(c)
			continue
		}
		i++
		switch s[i] {
		case 't':
			b.WriteByte('\t')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 'f':
			b.WriteByte('\f')
		case 'u':
			if i+5 > len(s) {
				return "", errors.New(`malformed \uxxxx escape`)
			}
			r, err := strconv.ParseUint(s[i+1:i+5], 16, 16)
			if err != nil {
				return "", errors.New(`malformed \uxxxx escape`)
			}
			b.WriteRune(rune(r))
			i += 4
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String(), nil
}

// jvmNonProxyHosts converts a JVM http.nonProxyHosts value to
// upstreamProxy.NonProxyHosts. The JVM honours "*" only at either end of a
// pattern, so one with "*" inside never matches a host name and is dropped.
func jvmNonProxyHosts(value string) []string {
	var patterns []string
	for p := range strings.SplitSeq(value, "|") {
		inner := strings.TrimSuffix(strings.TrimPrefix(p, "*"), "*")
		if p == "" || strings.Contains(inner, "*") {
			continue
		}
		patterns = append(patterns, p)
	}
	return patterns
}
