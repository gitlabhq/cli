package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/zalando/go-keyring"
	"go.yaml.in/yaml/v3"

	"gitlab.com/gitlab-org/cli/internal/dbg"
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
	localPath                 string
	customHeaderCommandValues sync.Map
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
			if hostname == "" || !envScopedToTrustedHosts(key) || IsTrustedHost(c, hostname) {
				return value, source, nil
			}
			dbg.Debugf("config: ignoring environment variable %s for host %q: it is neither the default host nor a configured host", source, hostname)
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

// envCredentialKeys are the keys whose environment variables resolve only for
// trusted hosts. Other per-host keys (api_host, client_id, ...) are not secrets
// and keep resolving globally, because `glab auth login` reads them for a host
// that is not configured yet.
var envCredentialKeys = map[string]struct{}{
	"token":     {},
	"job_token": {},
	"is_oauth2": {},
}

// envScopedToTrustedHosts reports whether key (or one of its aliases) is a
// credential whose environment variables only resolve for trusted hosts.
func envScopedToTrustedHosts(key string) bool {
	_, ok := envCredentialKeys[resolveAlias(key)]
	return ok
}

// IsTrustedHost reports whether environment-provided credentials may be used
// for hostname: either it is the default host (the global host key, which may
// itself come from GITLAB_HOST/GITLAB_URI/GL_HOST, or from CI_SERVER_FQDN
// under CI auto-login, falling back to gitlab.com), the host of a GITLAB_REPO
// URL, or a host present in the configuration's hosts map. Environment
// variables are process-global, so credentials must not resolve for an
// arbitrary host taken from a command argument (for example
// `glab repo view https://attacker.example.com/acme/awesome-tool`).
func IsTrustedHost(c Config, hostname string) bool {
	hostname = trustedHostKey(hostname)

	defaultHost, _ := c.Get("", "host")
	if defaultHost == "" {
		defaultHost = glinstance.DefaultHostname
	}
	if hostname == trustedHostKey(defaultHost) {
		return true
	}
	if repoHost := gitlabRepoHost(); repoHost != "" && hostname == trustedHostKey(repoHost) {
		return true
	}

	hosts, err := c.Hosts()
	if err != nil {
		return false
	}
	return slices.ContainsFunc(hosts, func(h string) bool {
		return trustedHostKey(h) == hostname
	})
}

// gitlabRepoHost returns the host of a GITLAB_REPO given as a URL or an
// scp-like Git URL. GITLAB_REPO is user-set like GITLAB_HOST, so its host is
// trusted too. The HOST/OWNER/REPO form is not parsed here: glrepo only treats
// its first segment as a host when that host is already in the hosts map.
// internal/git cannot be imported here (it imports config), so this mirrors
// git.ParseURL, which drops the port of an SSH URL.
func gitlabRepoHost() string {
	repo := strings.TrimSpace(os.Getenv("GITLAB_REPO"))
	if strings.Contains(repo, "://") {
		u, err := url.Parse(repo)
		if err != nil {
			return ""
		}
		if u.Scheme == "ssh" || u.Scheme == "git+ssh" {
			return u.Hostname()
		}
		return u.Host
	}
	if rest, ok := strings.CutPrefix(repo, "git@"); ok {
		host, _, _ := strings.Cut(rest, ":")
		return host
	}
	return ""
}

// trustedHostKey reduces a host as spelled by GITLAB_HOST, a hosts key, or a
// command argument to the lowercase host[:port] that IsTrustedHost compares.
// The scheme is cut only at "://" because StripHostProtocol also trims a bare
// leading "http" or "https", which would turn "httpsgit.corp.test" into
// "git.corp.test".
func trustedHostKey(h string) string {
	h = glinstance.NormalizeHostname(h)
	if _, rest, ok := strings.Cut(h, "://"); ok {
		h = rest
	}
	h, _ = glinstance.ExtractSubfolder(h)
	return h
}

// keyringProbePrefix names the sentinel entries used to check whether a working
// OS keyring backend is present.
const keyringProbePrefix = "glab:__keyring_probe__"

// keyringProbeSeq disambiguates concurrent probes within one process, since the
// PID alone cannot.
var keyringProbeSeq atomic.Uint64

// keyringProbeService returns a sentinel service name unique to this probe, so
// concurrent probes never contend for one entry. Backends vary in how badly they
// take that: the macOS shell-out searches then creates as two steps, so the loser
// of the race gets a duplicate-item error on a healthy keyring and the command
// refuses to refresh, while the Secret Service and Windows backends replace
// atomically. A sentinel leaked by a process that died mid-probe is harmless,
// since every backend's write replaces an existing entry.
func keyringProbeService() string {
	return fmt.Sprintf("%s:%d:%d", keyringProbePrefix, os.Getpid(), keyringProbeSeq.Add(1))
}

// KeyringAvailable reports whether a usable OS keyring backend is present by
// performing a lightweight write/delete of a sentinel entry. It returns false
// on platforms without a keyring (for example, headless Linux without a Secret
// Service, or CI runners), which lets callers fall back to file storage.
func KeyringAvailable() bool {
	return keyringWriteError() == nil
}

// keyringWriteError probes the keyring the same way KeyringAvailable does, but
// returns the underlying failure so callers can report why the keyring rejected
// the write instead of only that it did.
func keyringWriteError() error {
	service := keyringProbeService()
	if err := keyring.Set(service, "", "1"); err != nil {
		return err
	}
	// Best effort cleanup; a lingering sentinel is harmless.
	_ = keyring.Delete(service, "")
	return nil
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

// lockedSyncKeyring runs syncKeyring while holding the config-directory lock so
// a keyring write is serialized against other processes' writes. In-memory
// configs (no backing dir) run without a lock.
func (c *fileConfig) lockedSyncKeyring(hostname, key, value string) (string, error) {
	if c.dir == "" {
		return c.syncKeyring(hostname, key, value)
	}

	var out string
	err := withLock(c.dir, func() error {
		var e error
		out, e = c.syncKeyring(hostname, key, value)
		return e
	})
	return out, err
}

// syncKeyring reconciles the keyring copy of a credential with the value being
// written and returns the value to persist in the config file. When storing to
// the keyring, the plaintext copy is dropped from the file (empty return). When
// storing to the file or clearing the value, any stale keyring copy is removed
// so that switching keyring->file, or logging out, never orphans a secret,
// regardless of the order in which use_keyring and the credential are written.
// The keyring cleanup is skipped in CI, where the keyring is left untouched.
func (c *fileConfig) syncKeyring(hostname, key, value string) (string, error) {
	useKeyring, _ := c.Get(hostname, "use_keyring")
	switch {
	case useKeyring == "true" && value != "":
		// Keyring mode: store in the keyring and drop the plaintext copy from the
		// config file by returning an empty value.
		if err := keyring.Set(buildKeyringKey(hostname, key), "", value); err != nil {
			// Name the keyring and the host, mirroring the read path in
			// GetWithSource. On macOS the backend shells out to
			// /usr/bin/security, so the bare error is an unexplained
			// "exit status <n>" with nothing tying it to the keyring.
			return value, fmt.Errorf("failed to store %q in the operating system's keyring for host %q: %w", key, hostname, err)
		}
		return "", nil
	case useKeyring == "true" || !InCI():
		// Keyring mode clearing the value, or file mode storing/clearing: remove any
		// keyring copy so switching keyring->file, or logging out, does not orphan a
		// secret. Skipped in CI, where the keyring is left untouched (and file mode
		// is the default there anyway).
		deleteFromKeyring(hostname, key)
	}
	return value, nil
}

func (c *fileConfig) Set(hostname, key, value string) error {
	key = ConfigKeyEquivalence(key)

	// Keep the keyring-backed and file-backed copies of a credential from
	// diverging, serialized against concurrent writers via the config-directory
	// lock so a keyring write cannot interleave with another process's refresh.
	if isKeyringEligibleKey(key) && hostname != "" {
		var err error
		value, err = c.lockedSyncKeyring(hostname, key, value)
		if err != nil {
			return err
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

	// Serialize the read-merge-write against other processes. renameio makes the
	// write itself atomic, but not the merge that precedes it. If the lock cannot
	// be taken (wedged peer), withLock degrades to writing unlocked.
	return withLock(c.dir, func() error {
		// A long-lived process (for example, `glab mcp serve`) writing its whole
		// in-memory document for an unrelated reason (an update-check timestamp
		// bump) could otherwise roll back OAuth credentials another process just
		// rotated. Adopt any fresher on-disk credentials before serializing.
		c.mergeCredentialsFromDisk()
		return c.writeLocked()
	})
}

// writeLocked serializes the in-memory document to config.yml. Callers hold the
// config-directory lock.
func (c *fileConfig) writeLocked() error {
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

// CredentialMerger copies newer credentials from a host's on-disk entry into its
// in-memory entry. It must only touch the YAML document, never the keyring.
type CredentialMerger func(mem, disk *HostConfig)

var credentialMerger atomic.Pointer[CredentialMerger]

// SetCredentialMerger registers the merger Write runs before saving.
func SetCredentialMerger(m CredentialMerger) {
	credentialMerger.Store(&m)
}

// mergeCredentialsFromDisk runs the registered merger on each host. Callers must
// hold the config-directory lock.
func (c *fileConfig) mergeCredentialsFromDisk() {
	merge := credentialMerger.Load()
	if merge == nil || *merge == nil {
		return
	}

	onDisk, err := ParseConfig(filepath.Join(c.dir, "config.yml"))
	if err != nil {
		// A missing or unreadable file has nothing fresher to adopt.
		dbg.Debugf("config: could not re-read config.yml before write, skipping credential merge: %v", err)
		return
	}
	diskCfg, ok := onDisk.(*fileConfig)
	if !ok {
		return
	}

	hosts, err := c.Hosts()
	if err != nil {
		return
	}

	for _, host := range hosts {
		diskHost, err := diskCfg.configForHost(host)
		if err != nil {
			continue
		}
		memHost, err := c.configForHost(host)
		if err != nil {
			continue
		}
		(*merge)(memHost, diskHost)
	}
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
			newContent = slices.Concat(c.Root().Content, []*yaml.Node{keyNode, valueNode})
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
			newContent = slices.Concat(c.Root().Content, []*yaml.Node{keyNode, valueNode})
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

	for i := 0; i < len(hostsEntry.Content)-1; i += 2 {
		hostname := hostsEntry.Content[i].Value
		hostRoot := hostsEntry.Content[i+1]

		// Resolve YAML alias nodes (e.g. "*gitlab") to their anchored target.
		// yaml.Unmarshal into yaml.Node preserves aliases as AliasNode with an
		// Alias pointer; the node's own Content is empty until dereferenced.
		if hostRoot.Kind == yaml.AliasNode {
			hostRoot = hostRoot.Alias
		}
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
	u, err := url.Parse(urlStr)
	if err != nil {
		return ""
	}

	// Extract and clean the path
	subfolder := strings.Trim(u.Path, "/")
	return subfolder
}
