package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/adrg/xdg"
	"go.yaml.in/yaml/v3"
)

var (
	cachedConfig Config
	configError  error
)

// legacyConfigDir returns the legacy config directory (~/.config/glab-cli).
// This was the default location before XDG platform-specific paths were adopted.
// Uses os.UserHomeDir() for cross-platform compatibility (works on Windows, macOS, Linux).
func legacyConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "glab-cli")
}

// ConfigDir returns the config directory for writing configuration.
// It respects GLAB_CONFIG_DIR as the highest priority override.
// For backward compatibility, if a legacy config exists at ~/.config/glab-cli/,
// that location continues to be used. Otherwise, uses XDG_CONFIG_HOME.
func ConfigDir() string {
	glabDir := os.Getenv("GLAB_CONFIG_DIR")
	if glabDir != "" {
		return glabDir
	}

	// Check for legacy config location for backward compatibility
	legacyDir := legacyConfigDir()
	if legacyDir != "" {
		legacyConfigFile := filepath.Join(legacyDir, "config.yml")
		if _, err := os.Stat(legacyConfigFile); err == nil {
			return legacyDir
		}
	}

	return filepath.Join(xdg.ConfigHome, "glab-cli")
}

// ConfigFile returns the config file path.
// It respects GLAB_CONFIG_DIR as the highest priority override,
// otherwise returns the XDG-compliant user config file path.
// This function only determines the path without creating directories.
func ConfigFile() string {
	return filepath.Join(ConfigDir(), "config.yml")
}

// SearchConfigFile searches for an existing config file across all config paths.
// It respects GLAB_CONFIG_DIR as the highest priority override.
// Search order:
// 1. $GLAB_CONFIG_DIR/config.yml (if GLAB_CONFIG_DIR is set)
// 2. ~/.config/glab-cli/config.yml (legacy location, for backward compatibility)
// 3. $XDG_CONFIG_HOME/glab-cli/config.yml (platform-specific XDG location)
// 4. $XDG_CONFIG_DIRS/glab-cli/config.yml (system-wide configs)
//
// Returns the path to the first config file found, or an error if none exist.
func SearchConfigFile() (string, error) {
	// HIGHEST PRIORITY: GLAB_CONFIG_DIR completely bypasses XDG
	if os.Getenv("GLAB_CONFIG_DIR") != "" {
		configPath := ConfigFile()
		if _, err := os.Stat(configPath); err == nil {
			return configPath, nil
		}
		// If GLAB_CONFIG_DIR is set but file doesn't exist,
		// still return this path (don't fall through to XDG)
		return configPath, os.ErrNotExist
	}

	// Check legacy location first for backward compatibility
	legacyDir := legacyConfigDir()
	if legacyDir != "" {
		legacyConfigPath := filepath.Join(legacyDir, "config.yml")
		if _, err := os.Stat(legacyConfigPath); err == nil {
			return legacyConfigPath, nil
		}
	}

	// XDG search: user config → system configs
	configPath, err := xdg.SearchConfigFile("glab-cli/config.yml")
	if err != nil {
		return "", err
	}
	return configPath, nil
}

// checkForDuplicateConfigs warns if multiple config files exist across different locations.
// Since we don't support config merging (yet), only the first file found is used, which can
// be confusing if users have configs in multiple locations.
func checkForDuplicateConfigs(out io.Writer) {
	// Only check if GLAB_CONFIG_DIR is not set
	if os.Getenv("GLAB_CONFIG_DIR") != "" {
		return
	}

	type configEntry struct {
		path string
		info os.FileInfo
	}
	var existingConfigs []configEntry

	// addIfUnique adds a config path if it points to a distinct file.
	// Uses os.SameFile to handle symlinks (e.g., /home -> /var/home on Silverblue).
	addIfUnique := func(configPath string) {
		info, err := os.Stat(configPath)
		if err != nil {
			return
		}
		for _, entry := range existingConfigs {
			if os.SameFile(entry.info, info) {
				return // Same file, skip
			}
		}
		existingConfigs = append(existingConfigs, configEntry{configPath, info})
	}

	// Check legacy location first
	if legacyDir := legacyConfigDir(); legacyDir != "" {
		addIfUnique(filepath.Join(legacyDir, "config.yml"))
	}

	// Check XDG user config
	addIfUnique(filepath.Join(xdg.ConfigHome, "glab-cli", "config.yml"))

	// Check system-wide XDG configs
	for _, dir := range xdg.ConfigDirs {
		if dir == xdg.ConfigHome {
			continue
		}
		addIfUnique(filepath.Join(dir, "glab-cli", "config.yml"))
	}

	// Warn if multiple configs exist
	if len(existingConfigs) > 1 {
		fmt.Fprintf(out, "Warning: Multiple config files found. Only the first one will be used.\n")
		fmt.Fprintf(out, "  Using: %s\n", existingConfigs[0].path)
		for _, entry := range existingConfigs[1:] {
			fmt.Fprintf(out, "  Ignoring: %s\n", entry.path)
		}
		fmt.Fprintf(out, "Consider consolidating to one location to avoid confusion.\n")
	}
}

// Init initialises and returns the cached configuration
func Init() (Config, error) {
	if cachedConfig != nil || configError != nil {
		return cachedConfig, configError
	}

	// Ensure the config directory exists before attempting to read/write config files.
	// This is especially important on Windows where os.ReadFile returns a different error
	// when the parent directory doesn't exist vs. when just the file doesn't exist.
	configDir := ConfigDir()
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		return nil, fmt.Errorf("failed to create config directory: %w", err)
	}

	// Check for duplicate configs and warn user
	checkForDuplicateConfigs(os.Stderr)

	cachedConfig, configError = ParseDefaultConfig()

	if os.IsNotExist(configError) {
		if err := cachedConfig.WriteAll(); err != nil {
			return nil, err
		}
		configError = nil
	}
	return cachedConfig, configError
}

func ParseDefaultConfig() (Config, error) {
	// Try to find existing config first (searches all XDG paths)
	configPath, err := SearchConfigFile()
	if err != nil {
		// No config found, use default writable location
		configPath = ConfigFile()
	}

	// Merge the git-based local config; this is the production path where a
	// per-repository .git/glab-cli/config.yml may override global settings.
	cfg, cfgErr := parseConfig(configPath, LocalConfigFile())

	// SearchConfigFile may locate the config in a read-only system-wide XDG
	// directory, but glab always persists to the user's writable config dir.
	// Pin the persistence target to ConfigDir() so Write() keeps writing there
	// even when the config was read from elsewhere.
	if fc, ok := cfg.(*fileConfig); ok {
		fc.dir = ConfigDir()
	}

	return cfg, cfgErr
}

func readConfigFile(filename string) ([]byte, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, pathError(err)
	}

	return data, nil
}

func writeConfigFile(filename string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(filename), 0o750); err != nil {
		return pathError(err)
	}
	return WriteFile(filename, data, 0o600)
}

func ParseConfigFile(filename string) ([]byte, *yaml.Node, error) {
	stat, err := os.Stat(filename)
	// we want to check if there actually is a file, sometimes
	// configs are just passed via stubs
	if err == nil {
		if !HasSecurePerms(stat.Mode().Perm()) {
			return nil, nil,
				fmt.Errorf("%s has the permissions %o, but glab requires 600.\nConsider running `chmod 600 %s`",
					filename,
					stat.Mode(),
					filename,
				)
		}
	}

	data, err := readConfigFile(filename)
	if err != nil {
		return nil, nil, err
	}

	root, err := parseConfigData(data)
	if err != nil {
		return nil, nil, err
	}
	return data, root, err
}

func parseConfigData(data []byte) (*yaml.Node, error) {
	var root yaml.Node
	err := yaml.Unmarshal(data, &root)
	if err != nil {
		return nil, err
	}

	if len(root.Content) == 0 {
		return &yaml.Node{
			Kind:    yaml.DocumentNode,
			Content: []*yaml.Node{{Kind: yaml.MappingNode}},
		}, nil
	}
	if root.Content[0].Kind != yaml.MappingNode {
		return &root, fmt.Errorf("expected a top level map")
	}
	return &root, nil
}

// ParseConfig reads the main config from filename and the aliases file from the
// same directory. It does not read a separate local config file; callers that
// need local (per-repository) overrides merged in pass the path explicitly via
// parseConfig.
func ParseConfig(filename string) (Config, error) {
	return parseConfig(filename, "")
}

// parseConfig reads the main config from filename and the aliases file from the
// same directory. When localPath is non-empty, the local config file at that
// path is merged in under a "local" key (production passes the git-based path;
// tests pass a temp file). The returned config persists back to the directory
// it was parsed from, so a config read from a temp dir (tests) or a custom
// GLAB_CONFIG_DIR writes back to the same place instead of recomputing a global
// path on Write().
func parseConfig(filename, localPath string) (Config, error) {
	dir := filepath.Dir(filename)

	_, root, err := ParseConfigFile(filename)
	var confError error
	if err != nil {
		if os.IsNotExist(err) {
			root = NewBlankRoot()
			confError = os.ErrNotExist
		} else {
			return nil, err
		}
	}

	// Merge the local (per-repository) config file when a path is given.
	if localPath != "" {
		if _, localRoot, err := ParseConfigFile(localPath); err == nil {
			if len(localRoot.Content[0].Content) > 0 {
				newContent := []*yaml.Node{
					{Value: "local"},
					localRoot.Content[0],
				}
				restContent := root.Content[0].Content
				root.Content[0].Content = append(newContent, restContent...)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}

	// Load the aliases file from the same directory as the main config.
	if _, aliasesRoot, err := ParseConfigFile(filepath.Join(dir, "aliases.yml")); err == nil {
		if len(aliasesRoot.Content[0].Content) > 0 {
			newContent := []*yaml.Node{
				{Value: "aliases"},
				aliasesRoot.Content[0],
			}
			restContent := root.Content[0].Content
			root.Content[0].Content = append(newContent, restContent...)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	return newConfig(root, dir, localPath), confError
}

func pathError(err error) error {
	var pathError *os.PathError
	if errors.As(err, &pathError) && errors.Is(pathError.Err, syscall.ENOTDIR) {
		if p := findRegularFile(pathError.Path); p != "" {
			return fmt.Errorf("remove or rename regular file `%s` (must be a directory)", p)
		}
	}
	return err
}

func findRegularFile(p string) string {
	for {
		if s, err := os.Stat(p); err == nil && s.Mode().IsRegular() {
			return p
		}
		newPath := filepath.Dir(p)
		if newPath == p || newPath == "/" || newPath == "." {
			break
		}
		p = newPath
	}
	return ""
}
