package pm

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/proxy"
)

// mavenUserSettingsFile returns the settings.xml mvn would read, or "" when
// there is none, following Maven's own precedence.
func mavenUserSettingsFile(environ []string, dir string, args []string) string {
	cli := append(strings.Fields(lookupEnv(environ, "MAVEN_ARGS")), args...)
	cli = append(cli, mavenConfigArgs(dir)...)

	if path, ok := mavenOption(cli, "-s", "--settings", true); ok {
		if path == "" {
			return ""
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		return path
	}

	home := javaUserHome(environ, mavenProperties(environ, cli))
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".m2", "settings.xml")
}

// mavenSystemRCFiles are the rc files Maven's Unix launcher sources from
// fixed paths, unless MAVEN_SKIP_RC is set.
var mavenSystemRCFiles = []string{"/usr/local/etc/mavenrc", "/etc/mavenrc"}

// mavenGlobalSettingsFile returns the global settings.xml the caller means
// mvn to read: an explicit -gs or --global-settings, found the way -s is, or
// else the conf/settings.xml of the install that mvnPath, the resolved mvn
// binary, belongs to. glab then hands Maven that file with its own -gs, so an
// error here means the caller's choice is unclear, or the launcher could put
// a -gs ahead of glab's.
func mavenGlobalSettingsFile(environ []string, dir string, args []string, mvnPath string) (string, error) {
	if mvnPath == "" {
		return "", errors.New("mvn not found")
	}
	home, rcFiles, err := mavenHome(mvnPath)
	if err != nil {
		return "", err
	}
	if err := mavenRCOverride(environ, rcFiles); err != nil {
		return "", err
	}
	cli := append(strings.Fields(lookupEnv(environ, "MAVEN_ARGS")), args...)
	cli = append(cli, mavenConfigArgs(dir)...)
	// The launcher then reads .mvn/maven.config from a directory glab doesn't.
	if lookupEnv(environ, "MAVEN_BASEDIR") != "" || slices.Contains(cli, "-f") || slices.Contains(cli, "--file") {
		return "", errors.New("mvn may use another project base directory")
	}
	// Maven strips the quotes around an argument, so glab could miss a -gs.
	if slices.ContainsFunc(cli, func(a string) bool { return strings.ContainsAny(a, `"'`) }) {
		return "", errors.New("quoted maven arguments")
	}
	if path, ok := mavenOption(cli, "-gs", "--global-settings", false); ok {
		if path == "" {
			return "", errors.New("empty -gs value")
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		// Maven fails on a -gs file that doesn't exist, and should say so.
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			return "", errors.New("the -gs file doesn't exist")
		}
		return path, nil
	}
	// Pinning the install's settings would override a conf directory the
	// caller moved.
	if mavenConfMoved(environ, mavenProjectBaseDir(dir), cli) {
		return "", errors.New("maven.conf is set")
	}
	return filepath.Join(home, "conf", "settings.xml"), nil
}

// mavenConfMoved reports whether the JVM options, the Maven arguments or the
// project's .mvn/jvm.config set maven.conf. It keeps such a build as it was
// rather than guarding the firewall: Maven reads glab's -gs either way.
func mavenConfMoved(environ []string, base string, cli []string) bool {
	jvm, _, err := readConfigFile(filepath.Join(base, ".mvn", "jvm.config"))
	if err != nil {
		dbg.Debugf("dependency firewall: %v; not checking it for maven.conf", err)
	}
	sources := append([]string{string(jvm)}, cli...)
	for _, name := range []string{"MAVEN_OPTS", "MAVEN_DEBUG_OPTS", "JAVA_TOOL_OPTIONS", "JDK_JAVA_OPTIONS", "_JAVA_OPTIONS"} {
		sources = append(sources, lookupEnv(environ, name))
	}
	return slices.ContainsFunc(sources, mavenConfOption)
}

// mavenConfOption reports whether options set the maven.conf property. The
// JVM drops quotes inside an option, so they're dropped here too.
func mavenConfOption(options string) bool {
	options = strings.NewReplacer(`"`, "", `'`, "").Replace(options)
	return slices.ContainsFunc(strings.Fields(options), mavenConfProperty.MatchString)
}

// mavenConfProperty matches -Dmaven.conf=x, --define=maven.conf=x, and
// maven.conf=x after a separate -D.
var mavenConfProperty = regexp.MustCompile(`^(?:-D|--define=)?maven\.conf(?:=|$)`)

// mavenProjectBaseDir returns the nearest ancestor of dir with a .mvn
// directory, or dir when there is none.
func mavenProjectBaseDir(dir string) string {
	for d := range ancestors(dir) {
		if isDir(filepath.Join(d, ".mvn")) {
			return d
		}
	}
	return dir
}

// mavenHome returns the Maven 3 install that mvnPath launches, which has
// bin/m2.conf, and the fixed-path rc files its launcher sources. Homebrew's
// mvn is a wrapper that runs libexec/bin/mvn, patched to source Homebrew's
// own etc/mavenrc.
func mavenHome(mvnPath string) (string, []string, error) {
	resolved, err := filepath.EvalSymlinks(mvnPath)
	if err != nil {
		return "", nil, err
	}
	home := filepath.Dir(filepath.Dir(resolved))
	rcFiles := mavenSystemRCFiles
	if !exists(filepath.Join(home, "bin", "m2.conf")) {
		brew := filepath.Join(home, "libexec")
		if !exists(filepath.Join(brew, "bin", "m2.conf")) {
			return "", nil, errors.New("the mvn on PATH isn't in a Maven installation")
		}
		// <prefix>/Cellar/maven/<version>/libexec
		prefix := filepath.Dir(filepath.Dir(filepath.Dir(home)))
		rcFiles = append(slices.Clone(rcFiles), filepath.Join(prefix, "etc", "mavenrc"))
		home = brew
	}
	if err := mavenCheckInstall(home); err != nil {
		return "", nil, err
	}
	return home, rcFiles, nil
}

var mavenCoreJar = regexp.MustCompile(`^maven-core-3\.(\d+)\..*\.jar$`)

// mavenCheckInstall returns an error unless home is Maven 3.9 or a later 3.x,
// whose launcher passes MAVEN_ARGS on, and its bin/m2.conf keeps maven.conf
// at the default. Maven 4 also reads project and installation settings.
func mavenCheckInstall(home string) error {
	entries, err := os.ReadDir(filepath.Join(home, "lib"))
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(entries, func(e fs.DirEntry) bool {
		m := mavenCoreJar.FindStringSubmatch(e.Name())
		if len(m) != 2 {
			return false
		}
		minor, err := strconv.Atoi(m[1])
		return err == nil && minor >= 9
	}) {
		return errors.New("the mvn on PATH isn't Maven 3.9 or a later 3.x")
	}
	m2, err := os.ReadFile(filepath.Join(home, "bin", "m2.conf"))
	if err != nil {
		return err
	}
	for line := range strings.SplitSeq(string(m2), "\n") {
		f := strings.Fields(line)
		if len(f) > 0 && f[0] == "set" && !slices.Equal(f, []string{"set", "maven.conf", "default", "${maven.home}/conf"}) {
			return errors.New("the install's bin/m2.conf changes its properties")
		}
	}
	return nil
}

// mavenRCOverride returns an error when an rc file mvn's launcher runs could
// change the arguments glab passes it. Shell rc files are read; a Windows
// one counts whenever it exists.
func mavenRCOverride(environ []string, rcFiles []string) error {
	if lookupEnv(environ, "MAVEN_SKIP_RC") != "" {
		return nil
	}
	// The launcher reads "$HOME/.mavenrc", which is /.mavenrc without HOME.
	for _, rc := range append(slices.Clone(rcFiles), lookupEnv(environ, "HOME")+"/.mavenrc") {
		if err := mavenShellRCOverride(rc); err != nil {
			return err
		}
	}
	if profile := lookupEnv(environ, "USERPROFILE"); profile != "" {
		for _, name := range []string{"mavenrc_pre.bat", "mavenrc_pre.cmd"} {
			if rc := filepath.Join(profile, name); exists(rc) {
				return fmt.Errorf("mvn runs %s", rc)
			}
		}
	}
	return nil
}

// mavenRCAssignment matches an rc file line that sets M2_HOME, which the
// launcher ignores, JAVA_HOME or MAVEN_OPTS to a literal value.
var mavenRCAssignment = regexp.MustCompile(`^(?:export\s+)?(?:M2_HOME|JAVA_HOME|MAVEN_OPTS)=(?:"[^"$` + "`" + `\\]*"|'[^']*'|[^\s"'$` + "`" + `\\;&|<>(){}]*)$`)

// mavenShellRCOverride returns an error when the shell rc file at path exists
// and does more than mavenRCAssignment allows. Alpine's package ships an
// /etc/mavenrc that only sets M2_HOME.
func mavenShellRCOverride(path string) error {
	data, ok, err := readConfigFile(path)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if _, value, _ := strings.Cut(line, "="); !mavenRCAssignment.MatchString(line) || mavenConfOption(value) {
			return fmt.Errorf("mvn sources %s, which could change its arguments", path)
		}
	}
	return nil
}

// mavenMirror is a <mirror> entry from a settings file.
type mavenMirror struct {
	id, url, mirrorOf, layouts string
	blocked                    bool
}

// mavenMirrorsIn returns the <mirror> entries of a settings document, in order.
func mavenMirrorsIn(raw []byte) ([]mavenMirror, error) {
	var doc struct {
		Mirrors []struct {
			ID       *string `xml:"id"`
			URL      string  `xml:"url"`
			MirrorOf string  `xml:"mirrorOf"`
			Layouts  string  `xml:"mirrorOfLayouts"`
			Blocked  string  `xml:"blocked"`
		} `xml:"mirrors>mirror"`
	}
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	mirrors := make([]mavenMirror, 0, len(doc.Mirrors))
	for _, m := range doc.Mirrors {
		// Maven's id for a mirror without an <id>.
		id := "default"
		if m.ID != nil {
			id = strings.TrimSpace(*m.ID)
		}
		mirrors = append(mirrors, mavenMirror{
			id:       id,
			url:      strings.TrimSpace(m.URL),
			mirrorOf: strings.TrimSpace(m.MirrorOf),
			layouts:  strings.TrimSpace(m.Layouts),
			blocked:  strings.EqualFold(strings.TrimSpace(m.Blocked), "true"),
		})
	}
	return mirrors, nil
}

// mavenMirrorsDefaultLayout reports whether a mirror's mirrorOfLayouts
// covers the default repository layout, as DefaultMirrorSelector.matchesType
// decides it.
func mavenMirrorsDefaultLayout(layouts string) bool {
	if layouts == "" || layouts == "*" || layouts == "default" {
		return true
	}
	covered := false
	for layout := range strings.SplitSeq(layouts, ",") {
		switch layout {
		case "!default":
			return false
		case "default":
			return true
		case "*":
			covered = true
		}
	}
	return covered
}

// mavenKnownMirrors returns the URLs of every mirror in the user and global
// settings when one of them is a catch-all, so that every repository download
// goes through a URL in the list. Maven picks a mirror whose mirrorOf names
// the repository's id before trying patterns, so a mirror listed after the
// catch-all can still be used: every mirror must be known, or ok is false.
// externalOnly reports a catch-all of external:*, which Maven doesn't apply
// to repositories on localhost.
func mavenKnownMirrors(user, global []byte) ([]*url.URL, bool, bool) {
	var mirrors []mavenMirror
	for _, raw := range [][]byte{user, global} {
		if raw == nil {
			continue
		}
		ms, err := mavenMirrorsIn(raw)
		if err != nil {
			return nil, false, false
		}
		mirrors = append(mirrors, ms...)
	}
	var urls []*url.URL
	seen := map[string]struct{}{}
	catchAll, externalOnly := false, true
	for _, m := range mirrors {
		// Maven keeps the first mirror with each id, user settings first, and
		// compares interpolated ids, so an id with ${ can't be matched here.
		if strings.Contains(m.id, "${") {
			return nil, false, false
		}
		_, dropped := seen[m.id]
		seen[m.id] = struct{}{}
		// Maven never contacts a blocked mirror.
		if m.blocked {
			continue
		}
		u, err := url.Parse(m.url)
		if err != nil || strings.Contains(m.url, "${") || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			return nil, false, false
		}
		// An extra URL only adds readings, so a mirror Maven drops stays known.
		urls = append(urls, u)
		// Maven doesn't mirror a repository whose layout the mirror leaves out.
		if dropped || !mavenMirrorsDefaultLayout(m.layouts) {
			continue
		}
		switch m.mirrorOf {
		case "*":
			catchAll, externalOnly = true, false
		case "external:*":
			// Local repositories bypass it, so a mirror on localhost shares
			// its host with repositories it doesn't cover.
			if !proxy.IsLoopbackHost(u.Hostname()) {
				catchAll = true
			}
		}
	}
	if !catchAll {
		return nil, false, false
	}
	return urls, externalOnly, true
}

// mavenOption returns the value of the first short or long option in args,
// in the forms commons-cli accepts: "-s file", "-s=file", "--settings file"
// and "--settings=file", and "-sfile" when concatenated is set.
func mavenOption(args []string, short, long string, concatenated bool) (string, bool) {
	for i, a := range args {
		switch {
		case a == short || a == long:
			if i+1 < len(args) {
				return args[i+1], true
			}
			return "", false
		case strings.HasPrefix(a, short+"="):
			return a[len(short)+1:], true
		case strings.HasPrefix(a, long+"="):
			return a[len(long)+1:], true
		case concatenated && len(a) > len(short) && strings.HasPrefix(a, short) && !strings.HasPrefix(a, "--"):
			return a[len(short):], true
		}
	}
	return "", false
}

// mavenProperties returns the -D system properties from MAVEN_OPTS and the
// Maven command line, which Maven applies last.
func mavenProperties(environ []string, cli []string) map[string]string {
	props := map[string]string{}
	set := func(kv string) {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			v = "true"
		}
		props[k] = v
	}
	for a := range strings.FieldsSeq(lookupEnv(environ, "MAVEN_OPTS")) {
		if kv, ok := strings.CutPrefix(a, "-D"); ok {
			set(kv)
		}
	}
	for i := 0; i < len(cli); i++ {
		switch a := cli[i]; {
		case (a == "-D" || a == "--define") && i+1 < len(cli):
			i++
			set(cli[i])
		case strings.HasPrefix(a, "--define="):
			set(a[len("--define="):])
		case strings.HasPrefix(a, "-D") && len(a) > 2:
			set(a[2:])
		}
	}
	return props
}

// mavenConfigArgs returns the arguments in the nearest ancestor .mvn/maven.config.
func mavenConfigArgs(dir string) []string {
	for d := range ancestors(dir) {
		if !isDir(filepath.Join(d, ".mvn")) {
			continue
		}
		data, ok, err := readConfigFile(filepath.Join(d, ".mvn", "maven.config"))
		if err != nil {
			dbg.Debugf("dependency firewall: %v; not merging .mvn/maven.config", err)
			return nil
		}
		if !ok {
			return nil
		}
		var args []string
		for line := range strings.SplitSeq(string(data), "\n") {
			// Maven skips a line that starts with #.
			if strings.HasPrefix(line, "#") {
				continue
			}
			if line = strings.TrimSpace(line); line != "" {
				args = append(args, line)
			}
		}
		return args
	}
	return nil
}

// javaUserHome returns Java's user.home, which the JVM reads from the
// password database rather than HOME.
func javaUserHome(environ []string, props map[string]string) string {
	if home, ok := props["user.home"]; ok {
		return home
	}
	u, err := user.Current()
	if err != nil {
		dbg.Debugf("dependency firewall: could not look up the current user: %v; using HOME for maven's user.home", err)
	} else if u.HomeDir != "" {
		return u.HomeDir
	}
	return homeDir(environ)
}

// readConfigFile returns path's contents, and false when it doesn't exist;
// any other failure is an error rather than silent.
func readConfigFile(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		return data, true, nil
	case errors.Is(err, fs.ErrNotExist):
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("failed to read %s: %w", path, err)
	}
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// exists reports whether path may exist: only a not-exist error rules it out.
func exists(path string) bool {
	_, err := os.Stat(path)
	return !errors.Is(err, fs.ErrNotExist)
}

// ancestors yields dir and each of its parents up to the filesystem root.
func ancestors(dir string) iter.Seq[string] {
	return func(yield func(string) bool) {
		for {
			if !yield(dir) {
				return
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				return
			}
			dir = parent
		}
	}
}

// homeDir returns the user's home directory as the package managers read it
// from environ, or "" when environ does not name one.
func homeDir(environ []string) string {
	if home := lookupEnv(environ, "HOME"); home != "" {
		return home
	}
	if runtime.GOOS == "windows" {
		return lookupEnv(environ, "USERPROFILE")
	}
	return ""
}
