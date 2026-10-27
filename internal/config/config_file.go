package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"syscall"

	"github.com/adrg/xdg"
	"go.yaml.in/yaml/v3"

	"gitlab.com/gitlab-org/cli/internal/dbg"
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
		// A fresh config is seeded from the current schema defaults
		// (glamour_style: auto already), so it needs no migration.
		if err := cachedConfig.WriteAll(); err != nil {
			return nil, err
		}
		configError = nil
	} else if configError == nil {
		if err := migrateGlamourStyleAutoDetection(cachedConfig); err != nil {
			return nil, err
		}
	}
	return cachedConfig, configError
}

// migrateGlamourStyleAutoDetection flips a seeded `glamour_style: dark` to `auto`
// once, so a later explicit `dark` is kept.
func migrateGlamourStyleAutoDetection(cfg Config) error {
	migrated, _, err := cfg.GetWithSource("", "glamour_style_auto_migrated", false)
	if err != nil {
		return err
	}
	if migrated == "true" {
		return nil
	}

	style, _, err := cfg.GetWithSource("", "glamour_style", false)
	if err != nil {
		return err
	}
	if style == "dark" {
		if err := cfg.Set("", "glamour_style", "auto"); err != nil {
			return err
		}
	}

	if err := cfg.Set("", "glamour_style_auto_migrated", "true"); err != nil {
		return err
	}
	// Read-only config dirs (Nix, containers) must keep working: the
	// in-memory value already applies, and the next run retries the write.
	if err := cfg.Write(); err != nil {
		dbg.Debugf("config: could not persist glamour_style migration: %v", err)
	}
	return nil
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

// isSharedConfigDir reports whether dir is a system-wide configuration
// directory rather than the user's own.
//
// legacyConfigDir() is excluded by name because on macOS xdg.ConfigDirs
// contains $HOME/.config, whose glab-cli subdirectory is that legacy user
// directory -- membership alone would drop the 0600 rule for the file it
// protects. Symlinks are resolved so two names for one directory compare equal.
func isSharedConfigDir(dir string) bool {
	resolved := resolvePath(dir)

	if legacy := legacyConfigDir(); legacy != "" && resolvePath(legacy) == resolved {
		return false
	}

	return slices.ContainsFunc(xdg.ConfigDirs, func(base string) bool {
		return resolvePath(filepath.Join(base, "glab-cli")) == resolved
	})
}

// resolvePath follows symlinks where it can and falls back to an absolute path,
// so two names for one directory compare equal.
func resolvePath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

func ParseConfigFile(filename string) ([]byte, *yaml.Node, error) {
	// we want to check if there actually is a file, sometimes
	// configs are just passed via stubs
	if stat, err := os.Stat(filename); err == nil {
		perm := stat.Mode().Perm()
		// Classify on the directory: parseConfig also reads aliases.yml from it,
		// and a permission error there is not ErrNotExist, so it fails all of glab.
		if isSharedConfigDir(filepath.Dir(filename)) {
			// A config file can run a command through custom_headers.valueFromCommand,
			// so a shared file another account can write is code execution.
			if !HasSafeSharedPerms(perm) {
				return nil, nil, fmt.Errorf(
					"%s has the permissions %o, but glab requires a system-wide configuration file not to be writable by group or others.\nConsider running `chmod go-w %s`",
					filename, perm, filename,
				)
			}
		} else if !HasSecurePerms(perm) {
			return nil, nil, fmt.Errorf(
				"%s has the permissions %o, but glab requires 600.\nConsider running `chmod 600 %s`",
				filename, perm, filename,
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
				root.Content[0].Content = slices.Concat(newContent, restContent)
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
			root.Content[0].Content = slices.Concat(newContent, restContent)
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
