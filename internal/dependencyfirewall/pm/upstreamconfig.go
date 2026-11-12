package pm

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/dbg"
)

// systemRoot is the root of the machine-wide files pip and git read, such as
// /etc/gitconfig. Tests point it at an empty directory.
var systemRoot = string(filepath.Separator)

// configFiles reads the user and global config files glab takes proxy
// settings from. It skips any file inside the project being installed: the
// git work tree that holds the working directory, else the working directory.
type configFiles struct {
	// root is the project directory, or nil when it cannot be found, in
	// which case no file is read.
	root fs.FileInfo
}

func newConfigFiles(dir string) configFiles {
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		dbg.Debugf("dependency firewall: not reading proxy config files, the project directory is unknown: %v", err)
		return configFiles{}
	}
	root := resolved
	for p := range ancestors(resolved) {
		if exists(filepath.Join(p, ".git")) {
			root = p
			break
		}
	}
	info, err := os.Stat(root)
	if err != nil {
		dbg.Debugf("dependency firewall: not reading proxy config files, the project directory is unknown: %v", err)
		return configFiles{}
	}
	return configFiles{root: info}
}

// resolvePath returns path as the OS opens it. A relative path is joined to
// the working directory without cleaning, so that EvalSymlinks applies a ".."
// after a symlink to the symlink's target, as the OS does.
func resolvePath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		path = wd + string(filepath.Separator) + path
	}
	return filepath.EvalSymlinks(path)
}

// inProject reports whether path is inside the project. Each directory above
// the resolved path is compared with the project root by identity, so neither
// a symlink nor a differently cased spelling hides the project. It reports
// true when that cannot be decided.
func (c configFiles) inProject(path string) bool {
	if c.root == nil {
		return true
	}
	resolved, err := resolvePath(path)
	if err != nil {
		return true
	}
	for p := range ancestors(resolved) {
		info, err := os.Stat(p)
		if err != nil || os.SameFile(info, c.root) {
			return true
		}
	}
	return false
}

// read returns the contents of the config file at path, and false when it
// does not exist or may be inside the project. It opens the resolved path it
// checked, so the file read is always the file checked.
func (c configFiles) read(path string) ([]byte, bool, error) {
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	resolved, err := resolvePath(path)
	if err != nil {
		dbg.Debugf("dependency firewall: not reading proxy settings from %s: %v", path, err)
		return nil, false, nil
	}
	if c.inProject(resolved) {
		dbg.Debugf("dependency firewall: not reading proxy settings from %s, which may be inside the project", path)
		return nil, false, nil
	}
	return readConfigFile(resolved)
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// lookPathIn finds the executable named name in the PATH of environ.
func lookPathIn(environ []string, name string) (string, bool) {
	for _, dir := range filepath.SplitList(lookupEnv(environ, "PATH")) {
		if dir == "" {
			continue
		}
		if path, err := exec.LookPath(filepath.Join(dir, name)); err == nil {
			return path, true
		}
	}
	return "", false
}

// envValue returns the last value of key in environ, and whether it is set
// at all, for the settings where an empty value differs from an absent one.
func envValue(environ []string, key string) (string, bool) {
	value, found := "", false
	for _, e := range environ {
		if k, v, ok := strings.Cut(e, "="); ok && k == key {
			value, found = v, true
		}
	}
	return value, found
}

// lineRe finds the line number that YAML and other parser errors carry.
var lineRe = regexp.MustCompile(`line (\d+)`)

// parseError reports that path failed to parse, at the line err names when it
// names one. It never includes err's text, which can quote the file, and so
// credentials in a proxy URL.
func parseError(path string, err error) error {
	if m := lineRe.FindStringSubmatch(err.Error()); m != nil {
		return errors.New("failed to parse " + path + " line " + m[1])
	}
	return errors.New("failed to parse " + path)
}

// jsonParseError is parseError for encoding/json errors, which carry a byte
// offset instead of a line.
func jsonParseError(path string, data []byte, err error) error {
	var offset int64 = -1
	if se, ok := errors.AsType[*json.SyntaxError](err); ok {
		offset = se.Offset
	} else if te, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		offset = te.Offset
	}
	if offset < 0 || offset > int64(len(data)) {
		return errors.New("failed to parse " + path)
	}
	line := 1 + strings.Count(string(data[:offset]), "\n")
	return errors.New("failed to parse " + path + " line " + strconv.Itoa(line))
}

// parseIni reads the top-level keys of an npm-style ini file (.npmrc,
// auth.ini) as npm's ini package parses them, with values not yet
// env-expanded. A key written as key[] collects a comma-separated list.
func parseIni(data []byte) map[string]string {
	values := map[string]string{}
	for _, line := range strings.FieldsFunc(string(data), func(r rune) bool { return r == '\r' || r == '\n' }) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed[0] == ';' || trimmed[0] == '#' {
			continue
		}
		if line[0] == '[' && strings.HasSuffix(trimmed, "]") {
			// Keys below a section header belong to that section, never to
			// the top level the managers read settings from.
			break
		}
		rawKey, rawValue, hasValue := strings.Cut(line, "=")
		key := iniUnquote(rawKey)
		value := "true"
		if hasValue {
			value = iniUnquote(rawValue)
		}
		if k, isList := strings.CutSuffix(key, "[]"); isList && k != "" {
			key = k
			if prev, ok := values[key]; ok {
				value = prev + "," + value
			}
		}
		values[key] = value
	}
	return values
}

// iniUnquote decodes an ini key or value as npm's ini package does: a quoted
// string is JSON-decoded; otherwise an unescaped ";" or "#" starts a comment
// and "\;", "\#" and "\\" are escapes.
func iniUnquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		if s[0] == '\'' {
			s = s[1 : len(s)-1]
		}
		var decoded string
		if json.Unmarshal([]byte(s), &decoded) == nil {
			return decoded
		}
		return s
	}
	var b strings.Builder
	escaped := false
	for _, c := range s {
		switch {
		case escaped:
			if !strings.ContainsRune(`\;#`, c) {
				b.WriteByte('\\')
			}
			b.WriteRune(c)
			escaped = false
		case c == ';' || c == '#':
			return strings.TrimSpace(b.String())
		case c == '\\':
			escaped = true
		default:
			b.WriteRune(c)
		}
	}
	if escaped {
		b.WriteByte('\\')
	}
	return strings.TrimSpace(b.String())
}

var npmEnvRefRe = regexp.MustCompile(`(\\*)\$\{([^${}?]+)(\?)?\}`)

// npmEnvReplace expands ${NAME} and ${NAME?} in an npm config value as npm
// does: an unset NAME stays literal, or becomes empty with "?", and an odd
// run of backslashes before "$" escapes the reference. It also reports
// whether a reference without "?" named an unset variable.
func npmEnvReplace(s string, environ []string) (string, bool) {
	missing := false
	out := npmEnvRefRe.ReplaceAllStringFunc(s, func(match string) string {
		m := npmEnvRefRe.FindStringSubmatch(match)
		esc, name, optional := m[1], m[2], m[3] == "?"
		if len(esc)%2 == 1 {
			return match[(len(esc)+1)/2:]
		}
		value, ok := envValue(environ, name)
		if !ok && !optional {
			missing = true
			value = "${" + name + "}"
		}
		return esc[:len(esc)/2] + value
	})
	return out, missing
}

var shellEnvRefRe = regexp.MustCompile(`\$\{(\w+)(?:(:?-)([^}]*))?\}`)

// errUnsetEnv reports a ${NAME} reference without a default to an unset
// variable. It does not name the variable, which comes from the file.
var errUnsetEnv = errors.New("a value names an unset environment variable")

// shellEnvReplace expands ${NAME}, ${NAME-default} and ${NAME:-default}, the
// syntax pnpm and Yarn Berry share. ":-" also replaces an empty value.
func shellEnvReplace(s string, environ []string) (string, error) {
	var err error
	out := shellEnvRefRe.ReplaceAllStringFunc(s, func(match string) string {
		m := shellEnvRefRe.FindStringSubmatch(match)
		name, op, fallback := m[1], m[2], m[3]
		value, ok := envValue(environ, name)
		switch {
		case op == ":-" && value == "", op == "-" && !ok:
			return fallback
		case !ok && op == "":
			err = errUnsetEnv
		}
		return value
	})
	return out, err
}
