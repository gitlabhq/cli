package pm

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// pipUpstreamProxy follows pip 26's proxy lookup: --isolated aside,
// PIP_PROXY, then the command's section ([install], [download], ...) of the
// config files, then their [global] section. Within a section the last file
// in pip's load order wins, and an empty value there leaves the section unset.
// A proxy from any of these makes pip ignore HTTPS_PROXY and NO_PROXY.
//
// command is "" for pipenv, whose resolver builds pip's options under a
// command name no config section matches.
func pipUpstreamProxy(environ []string, dir, binary, command string, isolated bool) (upstreamProxy, bool, error) {
	if !isolated {
		if key, value, ok := firstSetEnv(environ, false, "PIP_PROXY"); ok {
			return upstreamProxy{Source: key, URL: value}, true, nil
		}
	}

	files := newConfigFiles(dir)
	var global, commandProxy configSetting
	for _, file := range pipConfigFiles(environ, dir, binary, isolated) {
		data, ok, err := files.read(file)
		if err != nil {
			return upstreamProxy{}, false, err
		}
		if !ok {
			continue
		}
		sections, err := parsePipConfig(data)
		if err != nil {
			return upstreamProxy{}, false, parseError(file, err)
		}
		if v, ok := sections["global"]["proxy"]; ok {
			global = configSetting{source: fileSource(file, "global.proxy"), value: v}
		}
		if v, ok := sections[command]["proxy"]; ok && command != "" {
			commandProxy = configSetting{source: fileSource(file, command+".proxy"), value: v}
		}
	}
	for _, s := range []configSetting{commandProxy, global} {
		if s.value != "" {
			return s.proxy(), true, nil
		}
	}
	return upstreamProxy{}, false, nil
}

// pipValueOptions are pip's general options that take a value (pip 26.0).
var pipValueOptions = map[string]struct{}{
	"--cache-dir": {}, "--cert": {}, "--client-cert": {}, "--default-timeout": {},
	"--exists-action": {}, "--keyring-provider": {}, "--local-log": {}, "--log": {},
	"--log-file": {}, "--proxy": {}, "--python": {}, "--resume-retries": {},
	"--retries": {}, "--timeout": {}, "--trusted-host": {}, "--use-deprecated": {},
	"--use-feature": {},
}

// pipCommand returns pip's subcommand, the first argument that is neither an
// option nor an option's value, which names the config section pip reads
// after [global].
func pipCommand(args []string) string {
	for i := 0; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "-") {
			return args[i]
		}
		if _, ok := pipValueOptions[args[i]]; ok {
			i++
		}
	}
	return ""
}

// pipConfigFiles lists the files pip reads, lowest precedence first: the
// global files, the user files, the site file of the virtualenv the pip on
// PATH belongs to, then PIP_CONFIG_FILE. PIP_CONFIG_FILE set to the null
// device disables every file; set to an existing file, it replaces the user
// files. --isolated also skips the user files.
func pipConfigFiles(environ []string, dir, binary string, isolated bool) []string {
	configFile := lookupEnv(environ, "PIP_CONFIG_FILE")
	if configFile == os.DevNull {
		return nil
	}
	if configFile != "" && !filepath.IsAbs(configFile) {
		configFile = filepath.Join(dir, configFile)
	}

	name := "pip.conf"
	if runtime.GOOS == "windows" {
		name = "pip.ini"
	}
	home := homeDir(environ)
	var files, user []string
	switch runtime.GOOS {
	case "windows":
		if programData := lookupEnv(environ, "PROGRAMDATA"); programData != "" {
			files = append(files, filepath.Join(programData, "pip", name))
		}
		if home != "" {
			user = append(user, filepath.Join(home, "pip", name))
		}
		if appData := lookupEnv(environ, "APPDATA"); appData != "" {
			user = append(user, filepath.Join(appData, "pip", name))
		}
	case "darwin":
		files = append(files, filepath.Join(systemRoot, "Library", "Application Support", "pip", name))
		if home != "" {
			// pip uses the Application Support file whenever its directory
			// exists, and ~/.config only otherwise.
			user = append(user, filepath.Join(home, ".pip", name))
			appSupport := filepath.Join(home, "Library", "Application Support", "pip")
			if isDir(appSupport) {
				user = append(user, filepath.Join(appSupport, name))
			} else {
				user = append(user, filepath.Join(home, ".config", "pip", name))
			}
		}
	default:
		xdgDirs := lookupEnv(environ, "XDG_CONFIG_DIRS")
		if xdgDirs == "" {
			xdgDirs = filepath.Join(systemRoot, "etc", "xdg")
		}
		for _, d := range filepath.SplitList(xdgDirs) {
			files = append(files, filepath.Join(d, "pip", name))
		}
		files = append(files, filepath.Join(systemRoot, "etc", name))
		if home != "" {
			user = append(user, filepath.Join(home, ".pip", name))
		}
		if xdgHome := lookupEnv(environ, "XDG_CONFIG_HOME"); xdgHome != "" {
			user = append(user, filepath.Join(xdgHome, "pip", name))
		} else if home != "" {
			user = append(user, filepath.Join(home, ".config", "pip", name))
		}
	}

	if !isolated && (configFile == "" || !isFile(configFile)) {
		files = append(files, user...)
	}
	if prefix, ok := pipVirtualenv(environ, binary); ok {
		files = append(files, filepath.Join(prefix, name))
	}
	if configFile != "" {
		files = append(files, configFile)
	}
	return files
}

// pipVirtualenv returns the virtualenv holding the binary found on PATH,
// which is pip's sys.prefix and so where its site config file lives. pip
// outside a virtualenv has a site file under the Python installation, which
// is not read here.
func pipVirtualenv(environ []string, binary string) (string, bool) {
	if binary != "pip" {
		return "", false
	}
	path, ok := lookPathIn(environ, binary)
	if !ok {
		return "", false
	}
	prefix := filepath.Dir(filepath.Dir(path))
	return prefix, isFile(filepath.Join(prefix, "pyvenv.cfg"))
}

// parsePipConfig parses a pip config file as Python's RawConfigParser does,
// with pip's key normalization: keys are lowercased, "_" means "-" and a
// leading "--" is dropped. Like pip, it rejects a duplicate section or key.
func parsePipConfig(data []byte) (map[string]map[string]string, error) {
	sections := map[string]map[string]string{}
	var section map[string]string
	var rawKeys map[string]struct{}
	var lastKey string
	lastIndent := -1
	for n, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		switch {
		case trimmed == "" || trimmed[0] == '#' || trimmed[0] == ';':
			continue
		case lastKey != "" && indent > lastIndent:
			section[lastKey] += "\n" + trimmed
			continue
		}
		lastKey = ""
		if strings.HasPrefix(trimmed, "[") {
			end := strings.LastIndex(trimmed, "]")
			if end < 2 {
				return nil, fmt.Errorf("line %d: invalid section header", n+1)
			}
			name := trimmed[1:end]
			if _, dup := sections[name]; dup {
				return nil, fmt.Errorf("line %d: duplicate section", n+1)
			}
			section, rawKeys = map[string]string{}, map[string]struct{}{}
			sections[name] = section
			continue
		}
		if section == nil {
			return nil, fmt.Errorf("line %d: key outside a section", n+1)
		}
		i := strings.IndexAny(trimmed, "=:")
		if i <= 0 {
			return nil, fmt.Errorf("line %d: expected key = value", n+1)
		}
		raw := strings.ToLower(strings.TrimSpace(trimmed[:i]))
		if _, dup := rawKeys[raw]; dup {
			return nil, fmt.Errorf("line %d: duplicate key", n+1)
		}
		rawKeys[raw] = struct{}{}
		key := strings.ReplaceAll(strings.TrimPrefix(raw, "--"), "_", "-")
		section[key] = strings.TrimSpace(trimmed[i+1:])
		lastKey, lastIndent = key, indent
	}
	return sections, nil
}

// pipIsolated reports whether args carry pip's --isolated, which pip checks
// for literally.
func pipIsolated(args []string) bool { return slices.Contains(args, "--isolated") }
