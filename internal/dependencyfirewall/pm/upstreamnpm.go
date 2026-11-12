package pm

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// configSetting is one config value and where it came from: an environment
// variable name, or a file path with the key.
type configSetting struct {
	source string
	value  string
}

// configLayers is a manager's config sources, highest precedence first. npm
// and Yarn Classic resolve each key separately: the first layer that sets a
// key wins, even with a value such as "" or "null" that leaves it unset.
type configLayers []map[string]configSetting

func (c configLayers) get(key string) (configSetting, bool) {
	for _, layer := range c {
		if s, ok := layer[key]; ok {
			return s, true
		}
	}
	return configSetting{}, false
}

// value returns the setting for key, and false when it is absent or holds a
// value npm reads as unset.
func (c configLayers) value(key string) (configSetting, bool) {
	s, ok := c.get(key)
	if !ok {
		return configSetting{}, false
	}
	switch s.value {
	case "", "false", "null", "undefined":
		return configSetting{}, false
	}
	return s, true
}

func (s configSetting) proxy() upstreamProxy {
	return upstreamProxy{Source: s.source, URL: s.value}
}

func fileSource(path, key string) string { return path + " (" + key + ")" }

// npmUpstreamProxy picks the proxy npm uses for an HTTPS request: https-proxy
// from any layer, then proxy from any layer, then HTTPS_PROXY. Both noproxy
// and NO_PROXY apply (observed with npm 11.17; @npmcli/agent alone would let
// noproxy replace NO_PROXY).
func npmUpstreamProxy(config configLayers, environ []string) (upstreamProxy, bool) {
	var upstream upstreamProxy
	if s, ok := config.value("https-proxy"); ok {
		upstream = s.proxy()
	} else if s, ok := config.value("proxy"); ok {
		upstream = s.proxy()
	} else if key, value, ok := firstSetEnv(environ, true, "HTTPS_PROXY", "https_proxy"); ok {
		upstream = upstreamProxy{Source: key, URL: value}
	} else {
		return upstreamProxy{}, false
	}
	noproxy, _ := config.value("noproxy")
	upstream.NoProxy = joinNoProxy(noproxy.value, envNoProxy(environ))
	return upstream, true
}

// readNpmConfig loads the npm config layers that can set a proxy: the
// npm_config_* environment, the user config and the global npmrc, as
// @npmcli/config 10 (npm 11) loads them. The project .npmrc, npm's builtin
// npmrc, workspace roots and command-line flags are not read, and neither is
// a user or global file inside the project.
func readNpmConfig(environ []string, dir string) (configLayers, error) {
	files := newConfigFiles(dir)
	home := homeDir(environ)
	config := configLayers{npmEnvLayer(environ)}
	if userFile := npmUserConfigPath(config, home, dir); userFile != "" {
		layer, err := readNpmrc(files, userFile, environ)
		if err != nil {
			return nil, err
		}
		config = append(config, layer)
	}
	// The user file can move the global one through prefix or globalconfig.
	globalFile := npmGlobalConfigPath(config, environ, home, dir)
	if globalFile == "" {
		return config, nil
	}
	layer, err := readNpmrc(files, globalFile, environ)
	if err != nil {
		return nil, err
	}
	return append(config, layer), nil
}

// npmEnvLayer reads npm_config_* variables the way npm and Yarn Classic do:
// the prefix in any case, the key lowercased with "_" after its first
// character meaning "-", and empty values ignored. The last duplicate wins.
func npmEnvLayer(environ []string) map[string]configSetting {
	const prefix = "npm_config_"
	layer := map[string]configSetting{}
	for _, e := range environ {
		k, v, ok := strings.Cut(e, "=")
		if !ok || v == "" || len(k) <= len(prefix) || !strings.EqualFold(k[:len(prefix)], prefix) {
			continue
		}
		key := k[len(prefix):]
		if !strings.HasPrefix(key, "//") {
			key = strings.ToLower(key[:1] + strings.ReplaceAll(key[1:], "_", "-"))
		}
		value, _ := npmEnvReplace(strings.TrimSpace(v), environ)
		layer[key] = configSetting{source: k, value: value}
	}
	return layer
}

func npmUserConfigPath(config configLayers, home, dir string) string {
	if s, ok := config.get("userconfig"); ok {
		return npmPath(s.value, home, dir)
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".npmrc")
}

// npmGlobalConfigPath returns globalconfig, else prefix/etc/npmrc.
func npmGlobalConfigPath(config configLayers, environ []string, home, dir string) string {
	if s, ok := config.get("globalconfig"); ok {
		return npmPath(s.value, home, dir)
	}
	prefix := npmDefaultPrefix(environ)
	if s, ok := config.get("prefix"); ok {
		prefix = npmPath(s.value, home, dir)
	}
	if prefix == "" {
		return ""
	}
	return filepath.Join(prefix, "etc", "npmrc")
}

// npmDefaultPrefix returns $PREFIX, else the prefix of the node binary npm
// runs under: the directory above its bin directory, or its own directory on
// Windows. Yarn Classic uses the same default.
func npmDefaultPrefix(environ []string) string {
	if prefix := lookupEnv(environ, "PREFIX"); prefix != "" {
		return prefix
	}
	node, ok := lookPathIn(environ, "node")
	if !ok {
		return ""
	}
	// process.execPath, which npm derives the prefix from, has symlinks
	// resolved.
	if resolved, err := filepath.EvalSymlinks(node); err == nil {
		node = resolved
	}
	if runtime.GOOS == "windows" {
		return filepath.Dir(node)
	}
	prefix := filepath.Dir(filepath.Dir(node))
	if destdir := lookupEnv(environ, "DESTDIR"); destdir != "" {
		prefix = filepath.Join(destdir, prefix)
	}
	return prefix
}

// npmPath resolves a path-typed npm setting: a leading ~/ is the home
// directory, and a relative path is relative to the working directory.
func npmPath(value, home, dir string) string {
	if home != "" && (strings.HasPrefix(value, "~/") || (runtime.GOOS == "windows" && strings.HasPrefix(value, `~\`))) {
		return filepath.Join(home, value[2:])
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Join(dir, value)
}

// readNpmrc reads an npmrc file as one config layer, expanding ${VAR} as npm
// does. A missing file, or one inside the project, is an empty layer.
func readNpmrc(files configFiles, path string, environ []string) (map[string]configSetting, error) {
	data, ok, err := files.read(path)
	if !ok {
		return nil, err
	}
	values := parseIni(data)
	layer := make(map[string]configSetting, len(values))
	for k, v := range values {
		value, _ := npmEnvReplace(v, environ)
		layer[k] = configSetting{source: fileSource(path, k), value: value}
	}
	return layer, nil
}

// yarnIsBerry reports whether yarn in dir runs Yarn 2 or later: the nearest
// package.json with a packageManager field pins yarn@2 or later, as Corepack
// reads it, or a .yarnrc.yml from dir up sets yarnPath, which Classic hands
// the run to. Otherwise the yarn on PATH is taken to be Classic, the version
// Corepack and the yarn npm package install by default. These project files
// only pick which rules apply, never a proxy. A yarn 1 on PATH without
// Corepack ignores packageManager and still runs Classic, which glab then
// treats as Berry.
func yarnIsBerry(dir string) (bool, error) {
	major, err := yarnPackageManagerMajor(dir)
	if err != nil || major != 0 {
		return major >= 2, err
	}
	for p := range ancestors(dir) {
		var rc struct {
			YarnPath string `yaml:"yarnPath"`
		}
		path := filepath.Join(p, ".yarnrc.yml")
		data, found, err := readConfigFile(path)
		if err != nil {
			return false, err
		}
		if !found {
			continue
		}
		if err := yaml.Unmarshal(data, &rc); err != nil {
			return false, parseError(path, err)
		}
		if rc.YarnPath != "" {
			return true, nil
		}
	}
	return false, nil
}

// yarnPackageManagerMajor returns the yarn major version pinned by the
// "packageManager" field of the nearest package.json that has one, from dir
// up, skipping node_modules as Corepack does. It returns 0 when none pins
// yarn.
func yarnPackageManagerMajor(dir string) (int, error) {
	for p := range ancestors(dir) {
		if filepath.Base(p) == "node_modules" {
			continue
		}
		path := filepath.Join(p, "package.json")
		data, ok, err := readConfigFile(path)
		if err != nil {
			return 0, err
		}
		if !ok {
			continue
		}
		var manifest struct {
			PackageManager string `json:"packageManager"`
		}
		if err := json.Unmarshal(data, &manifest); err != nil {
			return 0, jsonParseError(path, data, err)
		}
		if manifest.PackageManager == "" {
			continue
		}
		version, isYarn := strings.CutPrefix(manifest.PackageManager, "yarn@")
		if !isYarn {
			return 0, nil
		}
		major := 0
		for _, c := range version {
			if c < '0' || c > '9' {
				break
			}
			major = major*10 + int(c-'0')
		}
		return major, nil
	}
	return 0, nil
}

// berryUpstreamProxy follows Yarn 4's httpsProxy lookup: YARN_HTTPS_PROXY,
// then ~/.yarnrc.yml. The .yarnrc.yml files from dir up are not read. An
// empty httpsProxy means a direct connection, and Berry applies no NO_PROXY.
// Per-host networkSettings are not read. With no httpsProxy, Berry connects
// directly; this returns false so the inspection proxy keeps HTTPS_PROXY, in
// case the run is Classic after all.
func berryUpstreamProxy(environ []string, files configFiles) (upstreamProxy, bool, error) {
	// Berry takes an empty YARN_HTTPS_PROXY as set, so it connects directly
	// even when ~/.yarnrc.yml names a proxy (verified against Yarn 4.9.2).
	for _, e := range slices.Backward(environ) {
		if k, v, ok := strings.Cut(e, "="); ok && strings.EqualFold(k, "YARN_HTTPS_PROXY") {
			return upstreamProxy{Source: k, URL: v}, true, nil
		}
	}
	home := homeDir(environ)
	if home == "" {
		return upstreamProxy{}, false, nil
	}
	file := filepath.Join(home, ".yarnrc.yml")
	data, found, err := files.read(file)
	if err != nil || !found {
		return upstreamProxy{}, false, err
	}
	var rc struct {
		HTTPSProxy *string `yaml:"httpsProxy"`
	}
	if err := yaml.Unmarshal(data, &rc); err != nil {
		return upstreamProxy{}, false, parseError(file, err)
	}
	if rc.HTTPSProxy == nil {
		return upstreamProxy{}, false, nil
	}
	value, err := shellEnvReplace(*rc.HTTPSProxy, environ)
	if err != nil {
		return upstreamProxy{}, false, fmt.Errorf("httpsProxy in %s: %w", file, err)
	}
	return upstreamProxy{Source: fileSource(file, "httpsProxy"), URL: value}, true, nil
}

// yarnClassicUpstreamProxy follows Yarn 1.22 for HTTPS requests: https-proxy,
// then proxy. An https-proxy of false means a direct connection. Classic
// applies no NO_PROXY to a configured proxy; with none it falls back to
// HTTPS_PROXY, which the inspection proxy uses by default.
func yarnClassicUpstreamProxy(environ []string, files configFiles) (upstreamProxy, bool, error) {
	config, err := readYarnClassicConfig(environ, files)
	if err != nil {
		return upstreamProxy{}, false, err
	}
	if s, ok := config.get("https-proxy"); ok && s.value == "false" {
		return upstreamProxy{Source: s.source}, true, nil
	}
	if s, ok := config.value("https-proxy"); ok {
		return s.proxy(), true, nil
	}
	s, ok := config.value("proxy")
	return s.proxy(), ok, nil
}

// yarnClassicPlainHTTPProxy follows Yarn 1.22 for plain-HTTP requests, which
// use only proxy and, without it, HTTP_PROXY.
func yarnClassicPlainHTTPProxy(environ []string, files configFiles) (upstreamProxy, bool, error) {
	config, err := readYarnClassicConfig(environ, files)
	if err != nil {
		return upstreamProxy{}, false, err
	}
	s, ok := config.value("proxy")
	return s.proxy(), ok, nil
}

// readYarnClassicConfig loads the config layers Yarn 1.22 reads a proxy from,
// highest precedence first: its own (YARN_HTTPS_PROXY and YARN_PROXY, the user
// .yarnrc, then <prefix>/etc/yarnrc), then npm's (npm_config_*, the user
// .npmrc, then <prefix>/etc/npmrc), which Classic consults only for a key its
// own config leaves undefined. The .yarnrc and .npmrc files of the project and
// its ancestors are not read, and neither is a user or global file inside the
// project.
func readYarnClassicConfig(environ []string, files configFiles) (configLayers, error) {
	home := homeDir(environ)
	// Classic matches these variable names case-insensitively.
	_, yarnUser, _ := firstSetEnv(environ, true, "YARN_USERCONFIG")
	_, npmUser, _ := firstSetEnv(environ, true, "NPM_CONFIG_USERCONFIG")
	if npmUser == "" {
		npmUser = yarnUser
	}
	if home != "" {
		if yarnUser == "" {
			yarnUser = filepath.Join(home, ".yarnrc")
		}
		if npmUser == "" {
			npmUser = filepath.Join(home, ".npmrc")
		}
	}
	var yarnGlobal, npmGlobal string
	if prefix := npmDefaultPrefix(environ); prefix != "" {
		yarnGlobal, npmGlobal = filepath.Join(prefix, "etc", "yarnrc"), filepath.Join(prefix, "etc", "npmrc")
	}

	yarnEnv := map[string]configSetting{}
	for key, name := range map[string]string{"https-proxy": "YARN_HTTPS_PROXY", "proxy": "YARN_PROXY"} {
		if k, v, ok := firstSetEnv(environ, true, name); ok {
			yarnEnv[key] = configSetting{source: k, value: v}
		}
	}
	config := configLayers{yarnEnv}
	for _, file := range []string{yarnUser, yarnGlobal} {
		if file == "" {
			continue
		}
		layer, err := readYarnrc(files, file)
		if err != nil {
			return nil, err
		}
		config = append(config, layer)
	}

	config = append(config, npmEnvLayer(environ))
	for _, file := range []string{npmUser, npmGlobal} {
		if file == "" {
			continue
		}
		data, ok, err := files.read(file)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		values := parseIni(data)
		layer := make(map[string]configSetting, len(values))
		for k, v := range values {
			value, missing := npmEnvReplace(v, environ)
			if missing {
				return nil, fmt.Errorf("failed to read %s: %w", file, errUnsetEnv)
			}
			layer[k] = configSetting{source: fileSource(file, k), value: value}
		}
		config = append(config, layer)
	}
	return config, nil
}
