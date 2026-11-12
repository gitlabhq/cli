package pm

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// cargoUpstreamProxy follows cargo's http.proxy lookup (verified against
// cargo 1.99): CARGO_HTTP_PROXY, then $CARGO_HOME's config, then git's
// http.proxy. The .cargo/config(.toml) files from dir up are not read, and
// neither is a $CARGO_HOME or git config inside the project. An empty value
// means a direct connection. libcurl still skips the proxy for a host in
// no_proxy or NO_PROXY. Config includes and --config are not read.
func cargoUpstreamProxy(environ []string, dir string) (upstreamProxy, bool, error) {
	upstream, ok, err := cargoConfiguredProxy(environ, dir)
	if !ok || err != nil {
		return upstreamProxy{}, false, err
	}
	upstream.NoProxy = envNoProxy(environ)
	return upstream, true, nil
}

func cargoConfiguredProxy(environ []string, dir string) (upstreamProxy, bool, error) {
	if value, ok := envValue(environ, "CARGO_HTTP_PROXY"); ok {
		return upstreamProxy{Source: "CARGO_HTTP_PROXY", URL: value}, true, nil
	}

	cargoHome := lookupEnv(environ, "CARGO_HOME")
	switch {
	case cargoHome != "" && !filepath.IsAbs(cargoHome):
		cargoHome = filepath.Join(dir, cargoHome)
	case cargoHome == "":
		if home := homeDir(environ); home != "" {
			cargoHome = filepath.Join(home, ".cargo")
		}
	}
	files := newConfigFiles(dir)
	if cargoHome != "" {
		file, data, ok, err := readCargoConfig(files, cargoHome)
		if err != nil {
			return upstreamProxy{}, false, err
		}
		if ok {
			var config struct {
				HTTP struct {
					Proxy *string `toml:"proxy"`
				} `toml:"http"`
			}
			if err := toml.Unmarshal(data, &config); err != nil {
				return upstreamProxy{}, false, tomlParseError(file, err)
			}
			if config.HTTP.Proxy != nil {
				return upstreamProxy{Source: fileSource(file, "http.proxy"), URL: *config.HTTP.Proxy}, true, nil
			}
		}
	}
	return gitConfigProxy(environ, files)
}

// tomlParseError is parseError for go-toml, whose errors can quote the file.
func tomlParseError(file string, err error) error {
	if de, ok := errors.AsType[*toml.DecodeError](err); ok {
		row, _ := de.Position()
		return fmt.Errorf("failed to parse %s line %d", file, row)
	}
	return fmt.Errorf("failed to parse %s", file)
}

// readCargoConfig reads dir/config, which cargo prefers, else
// dir/config.toml.
func readCargoConfig(files configFiles, dir string) (string, []byte, bool, error) {
	for _, name := range []string{"config", "config.toml"} {
		file := filepath.Join(dir, name)
		data, ok, err := files.read(file)
		if ok || err != nil {
			return file, data, ok, err
		}
	}
	return "", nil, false, nil
}

// gitConfigProxy returns http.proxy from the git config files cargo's libgit2
// reads when cargo has no proxy of its own: ~/.gitconfig over the XDG git
// config over /etc/gitconfig. include and includeIf are not followed.
func gitConfigProxy(environ []string, files configFiles) (upstreamProxy, bool, error) {
	var paths []string
	home := homeDir(environ)
	if home != "" {
		paths = append(paths, filepath.Join(home, ".gitconfig"))
	}
	if xdg := lookupEnv(environ, "XDG_CONFIG_HOME"); xdg != "" {
		paths = append(paths, filepath.Join(xdg, "git", "config"))
	} else if home != "" {
		paths = append(paths, filepath.Join(home, ".config", "git", "config"))
	}
	if runtime.GOOS != "windows" {
		paths = append(paths, filepath.Join(systemRoot, "etc", "gitconfig"))
	}
	for _, file := range paths {
		data, ok, err := files.read(file)
		if err != nil {
			return upstreamProxy{}, false, err
		}
		if !ok {
			continue
		}
		value, found, err := gitConfigValue(data, "http", "proxy")
		if err != nil {
			return upstreamProxy{}, false, parseError(file, err)
		}
		if found {
			return upstreamProxy{Source: fileSource(file, "http.proxy"), URL: value}, true, nil
		}
	}
	return upstreamProxy{}, false, nil
}

// gitConfigValue returns the last value of section.key in a git config file,
// outside any subsection. Section and key names match case-insensitively.
func gitConfigValue(data []byte, section, key string) (string, bool, error) {
	var value string
	var found, inSection bool
	lines := strings.Split(string(data), "\n")
	for i := 0; i < len(lines); i++ {
		n, line := i, lines[i]
		// n stays the line the entry starts on.
		for i+1 < len(lines) && gitLineContinues(line) {
			i++
			line = strings.TrimSuffix(strings.TrimSuffix(line, "\r"), `\`) + lines[i]
		}
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			end := strings.IndexByte(line, ']')
			if end < 0 {
				return "", false, fmt.Errorf("line %d: invalid section header", n+1)
			}
			inSection = strings.EqualFold(strings.TrimSpace(line[1:end]), section)
			line = strings.TrimSpace(line[end+1:])
			if line == "" || line[0] == '#' || line[0] == ';' {
				continue
			}
		}
		name, raw, hasValue := strings.Cut(line, "=")
		if !inSection || !strings.EqualFold(strings.TrimSpace(name), key) {
			continue
		}
		if !hasValue {
			value, found = "true", true
			continue
		}
		v, err := gitConfigUnquote(raw)
		if err != nil {
			return "", false, fmt.Errorf("line %d: %w", n+1, err)
		}
		value, found = v, true
	}
	return value, found, nil
}

// gitLineContinues reports whether line ends in a backslash that joins it to
// the next line, as in git: an unescaped one that isn't inside a comment.
func gitLineContinues(line string) bool {
	quoted, escaped := false, false
	for _, c := range strings.TrimSuffix(line, "\r") {
		switch {
		case escaped:
			escaped = false
		case c == '\\':
			escaped = true
		case c == '"':
			quoted = !quoted
		case !quoted && (c == '#' || c == ';'):
			return false
		}
	}
	return escaped
}

// gitConfigUnquote decodes a git config value: double quotes group text,
// backslash escapes \\, \", \n, \t and \b, and an unquoted "#" or ";"
// starts a comment.
func gitConfigUnquote(raw string) (string, error) {
	var b strings.Builder
	quoted, escaped := false, false
	for _, c := range strings.TrimSpace(raw) {
		switch {
		case escaped:
			escaped = false
			switch c {
			case '\\', '"':
				b.WriteRune(c)
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'b':
				if s := b.String(); s != "" {
					b.Reset()
					b.WriteString(s[:len(s)-1])
				}
			default:
				return "", errors.New("invalid escape")
			}
		case c == '\\':
			escaped = true
		case c == '"':
			quoted = !quoted
		case !quoted && (c == '#' || c == ';'):
			return strings.TrimSpace(b.String()), nil
		default:
			b.WriteRune(c)
		}
	}
	if quoted || escaped {
		return "", errors.New("unterminated value")
	}
	return strings.TrimSpace(b.String()), nil
}
