package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/zalando/go-keyring"
	"go.yaml.in/yaml/v3"

	"gitlab.com/gitlab-org/cli/internal/glinstance"
)

var keyringEligibleKeys = func() map[string]struct{} {
	out := map[string]struct{}{}
	for i := range KeySchema {
		if KeySchema[i].Keyring {
			out[KeySchema[i].Name] = struct{}{}
		}
	}
	return out
}()

// A Config reads and writes persistent configuration for glab.
type Config interface {
	Get(string, string) (string, error)
	GetWithSource(string, string, bool) (string, string, error)
	Set(string, string, string) error
	Hosts() ([]string, error)
	Aliases() (*AliasConfig, error)
	Local() (*LocalConfig, error)
	// Write writes to the config.yml file
	Write() error
	// WriteAll saves all the available configuration file types
	WriteAll() error
	// Reload re-reads the configuration from its backing files and returns the
	// refreshed Config, reflecting changes written by other processes since this
	// Config was parsed. It does not mutate the receiver. Configs with no backing
	// files (in-memory configs) return themselves unchanged.
	Reload() (Config, error)
}

// NotFoundError is returned when a config entry is not found.
type NotFoundError struct {
	error
}

func isNotFoundError(err error) bool {
	var nfe *NotFoundError
	return errors.As(err, &nfe)
}

// HostConfig represents the configuration for a single host.
type HostConfig struct {
	ConfigMap
	Host string
}

// ConfigMap type implements a low-level get/set config that is backed by an in-memory tree of YAML
// nodes. It allows us to interact with a YAML-based config programmatically, preserving any
// comments that were present when the YAML was parsed.
type ConfigMap struct {
	Root *yaml.Node
}

func (cm *ConfigMap) Empty() bool {
	return cm.Root == nil || len(cm.Root.Content) == 0
}

func (cm *ConfigMap) GetStringValue(key string) (string, error) {
	entry, err := cm.FindEntry(key)
	if err != nil {
		return "", err
	}
	return entry.ValueNode.Value, nil
}

func (cm *ConfigMap) SetStringValue(key, value string) error {
	entry, err := cm.FindEntry(key)

	valueNode := entry.ValueNode

	if err != nil && isNotFoundError(err) {
		keyNode := &yaml.Node{
			Kind:  yaml.ScalarNode,
			Value: key,
		}
		valueNode = &yaml.Node{
			Kind:  yaml.ScalarNode,
			Tag:   "!!str",
			Value: "",
		}

		cm.Root.Content = append(cm.Root.Content, keyNode, valueNode)
	} else if err != nil {
		return err
	}

	valueNode.Value = value

	// A blank entry (a key written with no value, e.g. `token:`) parses as a
	// null-tagged scalar. Assigning a value without clearing that tag produces
	// `token: !!null <value>`, which leaves the value in the file yet makes it
	// decode as null. Reset the tag so string values round-trip correctly.
	if valueNode.Tag == "!!null" {
		valueNode.Tag = "!!str"
	}

	return nil
}

type ConfigEntry struct {
	KeyNode   *yaml.Node
	ValueNode *yaml.Node
	Index     int
}

func (cm *ConfigMap) FindEntry(key string) (*ConfigEntry, error) {
	ce := &ConfigEntry{}

	topLevelKeys := cm.Root.Content
	for i, v := range topLevelKeys {
		if v.Value == key {
			ce.KeyNode = v
			ce.Index = i
			if i+1 < len(topLevelKeys) {
				ce.ValueNode = topLevelKeys[i+1]
			}
			return ce, nil
		}
	}

	return ce, &NotFoundError{errors.New("not found")}
}

func (cm *ConfigMap) RemoveEntry(key string) {
	var newContent []*yaml.Node

	content := cm.Root.Content
	for i := 0; i < len(content); i++ {
		if content[i].Value == key {
			i++ // skip the next node which is this key's value
		} else {
			newContent = append(newContent, content[i])
		}
	}

	cm.Root.Content = newContent
}

func NewConfig(root *yaml.Node) Config {
	return newConfig(root, "", "")
}

// newConfig builds a fileConfig that persists to dir. An empty dir means the
// config is in-memory only and Write()/WriteAll() are no-ops. localPath is the
// per-repository config file merged in at parse time, if any; it is retained so
// Reload() re-merges the same local overrides.
func newConfig(root *yaml.Node, dir, localPath string) Config {
	return &fileConfig{
		ConfigMap:    ConfigMap{Root: root.Content[0]},
		documentRoot: root,
		dir:          dir,
		localPath:    localPath,
	}
}

// NewFromString initializes an in-memory Config from a yaml string. It has no
// directory behind it, so Write()/WriteAll() are no-ops.
func NewFromString(str string) Config {
	return newConfigFromString(str, "")
}

// NewFromStringInDir initializes a Config from a yaml string that persists to
// dir. Intended for tests that need to inspect what would be written to disk.
func NewFromStringInDir(str, dir string) Config {
	return newConfigFromString(str, dir)
}

func newConfigFromString(str, dir string) Config {
	root, err := parseConfigData([]byte(str))
	if err != nil {
		panic(err)
	}
	return newConfig(root, dir, "")
}

// NewBlankConfig initializes an in-memory config pre-populated with comments and
// default values. It has no directory behind it, so Write()/WriteAll() are no-ops.
func NewBlankConfig() Config {
	return newConfig(NewBlankRoot(), "", "")
}

// NewBlankConfigInDir initializes a blank config that persists to dir.
func NewBlankConfigInDir(dir string) Config {
	return newConfig(NewBlankRoot(), dir, "")
}

func NewBlankRoot() *yaml.Node {
	return rootConfig()
}

// A fileConfig reads and writes glab configuration to a file on disk. An empty
// dir means the config is in-memory only and its writers are no-ops.
type fileConfig struct {
	ConfigMap
	documentRoot *yaml.Node
	dir          string
	// localPath is the per-repository config file merged in at parse time, if
	// any. Retained so Reload() re-merges the same local overrides.
	localPath string
}

func (c *fileConfig) Root() *yaml.Node {
	return c.ConfigMap.Root
}

// Reload re-parses the config from the files it was read from, so a caller that
// must not act on a stale in-memory copy (for example, the OAuth token source in
// a long-lived process) can pick up changes written by another process. It does
// not mutate the receiver: an in-memory config (no backing directory) returns
// itself unchanged. The keyring is always read live, so a reloaded config also
// reflects the freshest keyring secrets.
func (c *fileConfig) Reload() (Config, error) {
	if c.dir == "" {
		return c, nil
	}
	return parseConfig(filepath.Join(c.dir, "config.yml"), c.localPath)
}

func (c *fileConfig) Get(hostname, key string) (string, error) {
	val, _, err := c.GetWithSource(hostname, key, true)
	return val, err
}

func (c *fileConfig) GetWithSource(hostname, key string, searchENVVars bool) (string, string, error) {
	if searchENVVars {
		value, source := GetFromEnvWithSource(key)
		if value != "" {
			return value, source, nil
		}
	}

	key = ConfigKeyEquivalence(key)

	var cfgError error

	if hostname != "" {
		hostCfg, err := c.configForHost(hostname)
		if err != nil && !isNotFoundError(err) {
			return "", "", err
		}

		var hostValue string
		if hostCfg != nil {
			// Check if use_keyring field is enabled for token keys
			if isKeyringEligibleKey(key) {
				useKeyring, _ := hostCfg.GetStringValue("use_keyring")

				if useKeyring == "true" {
					// Keyring enabled - retrieve from platform-native secure storage
					token, err := getFromKeyring(hostname, key)
					if err == nil {
						return token, "keyring", nil
					}
					// A missing entry is not a failure: treat it like an unset
					// value and fall through to normal resolution. Any other
					// error (locked keyring, denied access, unavailable backend)
					// is surfaced so callers do not silently proceed with an
					// empty credential.
					if !errors.Is(err, keyring.ErrNotFound) {
						return "", "keyring", fmt.Errorf("failed to read %q from the operating system keyring for host %q: %w", key, hostname, err)
					}
				}
			}

			hostValue, err = hostCfg.GetStringValue(key)
			if err != nil && !isNotFoundError(err) {
				return "", "", err
			}

			// Fallback: check keyring if token not in config (backward compat for existing users)
			// Only check for PAT (token) and OAuth2 refresh tokens, not job_token, since job tokens
			// were not commonly stored in keyring historically and the legacy format is ambiguous.
			if (err != nil || hostValue == "") && (key == "token" || key == "oauth2_refresh_token") {
				token, err := getFromKeyring(hostname, key)
				if err == nil {
					return token, "keyring", nil
				}
			}

		}

		if hostValue != "" {
			return hostValue, ConfigFile(), nil
		}
	}

	source := ConfigFile()

	l, _ := c.Local()
	value, err := l.GetStringValue(key)

	if (err != nil && isNotFoundError(err)) || value == "" {
		value, err = c.GetStringValue(key)
		if err != nil && isNotFoundError(err) {
			return defaultFor(key), source, cfgError
		} else if err != nil {
			if hostname != "" {
				err = cfgError
			}
			return "", LocalConfigFile(), err
		}
	} else if value != "" {
		source = LocalConfigFile()
	}

	if value == "" {
		return defaultFor(key), source, cfgError
	}

	return value, source, cfgError
}

// isKeyringEligibleKey returns true if the key can be stored in keyring
func isKeyringEligibleKey(key string) bool {
	_, eligible := keyringEligibleKeys[key]
	return eligible
}

// keyringProbeService is the sentinel service name used to check whether a
// working OS keyring backend is present.
const keyringProbeService = "glab:__keyring_probe__"

// KeyringAvailable reports whether a usable OS keyring backend is present by
// performing a lightweight write/delete of a sentinel entry. It returns false
// on platforms without a keyring (for example, headless Linux without a Secret
// Service, or CI runners), which lets callers fall back to file storage.
func KeyringAvailable() bool {
	if err := keyring.Set(keyringProbeService, "", "1"); err != nil {
		return false
	}
	// Best effort cleanup; a lingering sentinel is harmless.
	_ = keyring.Delete(keyringProbeService, "")
	return true
}

// buildKeyringKey constructs the keyring key for a given hostname and config key
func buildKeyringKey(hostname, key string) string {
	// Always suffix with the key name to avoid collisions
	// e.g., "glab:gitlab.com:token", "glab:gitlab.com:oauth2_refresh_token"
	return "glab:" + hostname + ":" + key
}

// deleteFromKeyring best-effort removes a credential from the keyring, trying
// both the current and legacy key formats. Missing entries are ignored.
func deleteFromKeyring(hostname, key string) {
	_ = keyring.Delete(buildKeyringKey(hostname, key), "")
	if legacyKey := buildLegacyKeyringKey(hostname, key); legacyKey != "" {
		_ = keyring.Delete(legacyKey, "")
	}
}

// InCI reports whether glab is running in a CI environment, based on the
// conventional GITLAB_CI and CI variables. A value that parses as boolean true
// (for example "true" or "1") counts; empty, "false", "0", and unparseable
// values do not. GitLab and GitHub both set these to "true".
func InCI() bool {
	return ciEnvEnabled("GITLAB_CI") || ciEnvEnabled("CI")
}

func ciEnvEnabled(name string) bool {
	enabled, err := strconv.ParseBool(os.Getenv(name))
	return err == nil && enabled
}

// getFromKeyring attempts to retrieve a value from the keyring, trying both
// new and legacy key formats for backward compatibility.
func getFromKeyring(hostname, key string) (string, error) {
	// Try new format first
	keyringKey := buildKeyringKey(hostname, key)
	token, err := keyring.Get(keyringKey, "")
	if err == nil {
		return token, nil
	}

	// Fallback to legacy key format for backward compatibility (if one exists)
	legacyKey := buildLegacyKeyringKey(hostname, key)
	if legacyKey != "" {
		return keyring.Get(legacyKey, "")
	}

	// No legacy format exists for this key type
	return "", err
}

// buildLegacyKeyringKey constructs the old keyring key format for backward compatibility.
// Legacy format used "glab:hostname" for token (PAT only)
// and "glab:hostname:refresh_token" for oauth2_refresh_token.
// Returns empty string for keys that did not have a legacy format (e.g., job_token).
// Note: The old code used "glab:hostname" ambiguously for both token and job_token,
// but we only check it for token (PAT) since that was the common use case for keyring.
func buildLegacyKeyringKey(hostname, key string) string {
	if key == "oauth2_refresh_token" {
		return "glab:" + hostname + ":refresh_token"
	}
	if key == "token" {
		// Only PATs were commonly stored in keyring
		return "glab:" + hostname
	}
	// No legacy format for job_token and other keys
	return ""
}

func (c *fileConfig) Set(hostname, key, value string) error {
	key = ConfigKeyEquivalence(key)

	// Keep the keyring-backed and file-backed copies of a credential from
	// diverging. When storing to the keyring, the plaintext copy is dropped from
	// the file (below). When storing to the file or clearing the value, any
	// stale keyring copy is removed so that switching keyring->file, or logging
	// out, never orphans a secret regardless of the order in which use_keyring
	// and the credential are written. The keyring cleanup is skipped in CI,
	// where the keyring is intentionally left untouched.
	if isKeyringEligibleKey(key) && hostname != "" {
		useKeyring, _ := c.Get(hostname, "use_keyring")
		switch {
		case useKeyring == "true" && value != "":
			// Keyring mode: store in the keyring. Setting value to "" removes the
			// plaintext copy from the config file below.
			keyringKey := buildKeyringKey(hostname, key)
			if err := keyring.Set(keyringKey, "", value); err != nil {
				return err
			}
			value = ""
		case useKeyring == "true" || !InCI():
			// Keyring mode clearing the value, or file mode storing/clearing:
			// remove any keyring copy so switching keyring->file, or logging out,
			// does not orphan a secret. Skipped in CI, where the keyring is left
			// untouched (and file mode is the default there anyway).
			deleteFromKeyring(hostname, key)
		}
	}

	var cfg interface {
		SetStringValue(string, string) error
		RemoveEntry(string)
	}

	switch hostname {
	case "":
		cfg = c
	default:
		var err error
		cfg, err = c.configForHost(hostname)
		if err != nil {
			if isNotFoundError(err) {
				cfg = c.makeConfigForHost(hostname)
				break
			}
			return err
		}
	}

	switch value {
	case "":
		cfg.RemoveEntry(key)
		return nil
	default:
		return cfg.SetStringValue(key, value)
	}
}

func (c *fileConfig) Write() error {
	if c.dir == "" {
		return nil
	}

	mainData := yaml.Node{Kind: yaml.MappingNode}

	nodes := c.documentRoot.Content[0].Content
	for i := 0; i < len(nodes)-1; i += 2 {
		if nodes[i].Value == "aliases" || nodes[i].Value == "local" {
			continue
		} else {
			mainData.Content = append(mainData.Content, nodes[i], nodes[i+1])
		}
	}

	mainBytes, err := yaml.Marshal(&mainData)
	if err != nil {
		return err
	}

	return writeConfigFile(filepath.Join(c.dir, "config.yml"), yamlNormalize(mainBytes))
}

func (c *fileConfig) WriteAll() error {
	err := c.Write()
	if err != nil {
		return err
	}

	aliases, err := c.Aliases()
	if err != nil {
		return err
	}
	return aliases.Write()
}

func yamlNormalize(b []byte) []byte {
	if bytes.Equal(b, []byte("{}\n")) {
		return []byte{}
	}
	return b
}

func (c *fileConfig) Local() (*LocalConfig, error) {
	entry, err := c.FindEntry("local")
	notFound := isNotFoundError(err)
	if err != nil && !notFound {
		return nil, err
	}

	var toInsert []*yaml.Node

	keyNode := entry.KeyNode
	valueNode := entry.ValueNode

	if keyNode == nil {
		keyNode = &yaml.Node{
			Kind:  yaml.ScalarNode,
			Value: "local",
		}
		toInsert = append(toInsert, keyNode)
	}

	if valueNode == nil || valueNode.Kind != yaml.MappingNode {
		valueNode = &yaml.Node{
			Kind:  yaml.MappingNode,
			Value: "",
		}
		toInsert = append(toInsert, valueNode)
	}

	if len(toInsert) > 0 {
		var newContent []*yaml.Node
		if notFound {
			newContent = append(c.Root().Content, keyNode, valueNode)
		} else {
			for i := 0; i < len(c.Root().Content); i++ {
				if i == entry.Index {
					newContent = append(newContent, keyNode, valueNode)
					i++
				} else {
					newContent = append(newContent, c.Root().Content[i])
				}
			}
		}
		c.Root().Content = newContent
	}
	return &LocalConfig{
		Parent:    c,
		ConfigMap: ConfigMap{Root: valueNode},
		dir:       c.dir,
	}, nil
}

func (c *fileConfig) Aliases() (*AliasConfig, error) {
	// The complexity here is for dealing with either a missing or empty aliases key. It's something
	// we'll likely want for other config sections at some point.
	entry, err := c.FindEntry("aliases")
	notFound := isNotFoundError(err)
	if err != nil && !notFound {
		return nil, err
	}

	var toInsert []*yaml.Node

	keyNode := entry.KeyNode
	valueNode := entry.ValueNode

	if keyNode == nil {
		keyNode = &yaml.Node{
			Kind:  yaml.ScalarNode,
			Value: "aliases",
		}
		toInsert = append(toInsert, keyNode)
	}

	if valueNode == nil || valueNode.Kind != yaml.MappingNode {
		valueNode = &yaml.Node{
			Kind:  yaml.MappingNode,
			Value: "",
		}
		toInsert = append(toInsert, valueNode)
	}

	if len(toInsert) > 0 {
		var newContent []*yaml.Node
		if notFound {
			newContent = append(c.Root().Content, keyNode, valueNode)
		} else {
			for i := 0; i < len(c.Root().Content); i++ {
				if i == entry.Index {
					newContent = append(newContent, keyNode, valueNode)
					i++
				} else {
					newContent = append(newContent, c.Root().Content[i])
				}
			}
		}
		c.Root().Content = newContent
	}

	return &AliasConfig{
		Parent:    c,
		ConfigMap: ConfigMap{Root: valueNode},
		dir:       c.dir,
	}, nil
}

func (c *fileConfig) hostEntries() ([]*HostConfig, error) {
	entry, err := c.FindEntry("hosts")
	if err != nil {
		return nil, fmt.Errorf("could not find hosts config: %w", err)
	}

	hostConfigs, err := c.parseHosts(entry.ValueNode)
	if err != nil {
		return nil, fmt.Errorf("could not parse hosts config: %w", err)
	}

	return hostConfigs, nil
}

// Hosts returns a list of all known hostnames configured in hosts.yml
func (c *fileConfig) Hosts() ([]string, error) {
	entries, err := c.hostEntries()
	if err != nil {
		return nil, err
	}

	var hostnames []string
	for _, entry := range entries {
		hostnames = append(hostnames, entry.Host)
	}

	sort.SliceStable(hostnames, func(i, j int) bool { return hostnames[i] == glinstance.DefaultHostname })

	return hostnames, nil
}

func (c *fileConfig) makeConfigForHost(hostname string) *HostConfig {
	hostRoot := &yaml.Node{Kind: yaml.MappingNode}
	hostCfg := &HostConfig{
		Host:      hostname,
		ConfigMap: ConfigMap{Root: hostRoot},
	}
	hostsEntry, err := c.FindEntry("hosts")
	if isNotFoundError(err) {
		hostsEntry.KeyNode = &yaml.Node{
			Kind:  yaml.ScalarNode,
			Value: "hosts",
		}
		hostsEntry.ValueNode = &yaml.Node{Kind: yaml.MappingNode}
		root := c.Root()
		root.Content = append(root.Content, hostsEntry.KeyNode, hostsEntry.ValueNode)
	} else if err != nil {
		panic(err)
	}

	hostsEntry.ValueNode.Content = append(hostsEntry.ValueNode.Content,
		&yaml.Node{
			Kind:  yaml.ScalarNode,
			Value: hostname,
		}, hostRoot)

	return hostCfg
}

func (c *fileConfig) parseHosts(hostsEntry *yaml.Node) ([]*HostConfig, error) {
	var hostConfigs []*HostConfig

	for i := 0; i < len(hostsEntry.Content)-1; i = i + 2 {
		hostname := hostsEntry.Content[i].Value
		hostRoot := hostsEntry.Content[i+1]
		hostConfig := HostConfig{
			ConfigMap: ConfigMap{Root: hostRoot},
			Host:      hostname,
		}
		hostConfigs = append(hostConfigs, &hostConfig)
	}

	if len(hostConfigs) == 0 {
		return nil, &NotFoundError{errors.New("could not find any host configurations")}
	}

	return hostConfigs, nil
}

// GetFromEnv is just a wrapper for os.GetEnv but checks for matching names used in previous glab versions and
// retrieves the value of the environment if any of the matching names have been set.
// It returns the value, which will be empty if the variable is not present.
func GetFromEnv(key string) string {
	value, _ := GetFromEnvWithSource(key)
	return value
}

// GetFromEnvWithSource works like GetFromEnv but also returns the name of the environment variable that was
// set as the source.
func GetFromEnvWithSource(key string) (string, string) {
	envEq := EnvKeyEquivalence(key)
	for _, e := range envEq {
		if val := os.Getenv(e); val != "" {
			// Special handling for CI autologin variables that need parsing
			switch {
			case key == "subfolder" && e == "CI_SERVER_URL":
				// Extract subfolder from CI_SERVER_URL
				// is like "https://example.com/gitlab" or "https://example.com"
				if subfolder := extractSubfolderFromURL(val); subfolder != "" {
					return subfolder, e
				}
				// If no subfolder in URL, continue checking other env vars
				continue
			case key == "ssh_host" && e == "CI_SERVER_SHELL_SSH_HOST":
				// Combine SSH host and port if port is set
				sshHost := val
				if sshPort := os.Getenv("CI_SERVER_SHELL_SSH_PORT"); sshPort != "" && sshPort != "22" {
					// Only include port if it's non-default (not 22)
					sshHost = sshHost + ":" + sshPort
				}
				return sshHost, e
			default:
				return val, e
			}
		}
	}
	return "", ""
}

// extractSubfolderFromURL parses a URL and extracts the path component (subfolder).
// Returns empty string if URL has no path or only "/".
func extractSubfolderFromURL(urlStr string) string {
	// Parse the URL
	u, err := url.Parse(urlStr)
	if err != nil {
		return ""
	}

	// Extract and clean the path
	subfolder := strings.Trim(u.Path, "/")
	return subfolder
}
