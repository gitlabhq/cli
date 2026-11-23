package config

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"gitlab.com/gitlab-org/cli/internal/dbg"
)

type LocalConfig struct {
	ConfigMap
	Parent Config
	dir    string
}

// for later use we might prefer relative paths
// GitDir returns the git directory path and whether git rev-parse succeeded.
// When git cannot resolve the directory, it returns []string{".git"} and false.
func GitDir(preferRelative bool) ([]string, bool) {
	// `git rev-parse --git-dir` since git v1.2.2
	return revParseDir("--git-dir", preferRelative)
}

// GitCommonDir returns the shared git directory and whether git rev-parse succeeded.
// In a normal checkout it equals GitDir; in a linked worktree GitDir is
// .git/worktrees/<name> while this is the main .git. A submodule keeps its own.
// When git cannot resolve the directory, it returns []string{".git"} and false.
func GitCommonDir(preferRelative bool) ([]string, bool) {
	// `git rev-parse --git-common-dir` since git v2.5, which introduced worktrees
	return revParseDir("--git-common-dir", preferRelative)
}

// revParseDir resolves one `git rev-parse` directory flag to the caller's
// preferred path form, falling back to ".git" when git cannot answer.
func revParseDir(revParseFlag string, preferRelative bool) ([]string, bool) {
	var err error
	var out strings.Builder

	cmd := exec.Command("git", "rev-parse", revParseFlag)

	cmd.Stdout = &out
	err = cmd.Run()
	if err != nil {
		dbg.Debugf("config: git rev-parse %s failed: %v", revParseFlag, err)
		return []string{".git"}, false
	}

	gitDir := strings.TrimSpace(out.String())

	if !filepath.IsAbs(gitDir) {
		// gitDir is relative
		if !preferRelative {
			// but we prefer absolute
			workDir, err := os.Getwd()
			if err == nil {
				return []string{workDir, gitDir}, true
			}
		}
		// relative should work, cleaned because git answers with forward slashes
		return []string{filepath.Clean(gitDir)}, true
	} else {
		// gitDir is absolute
		if preferRelative {
			// but we prefer relative
			var relativeDir string
			workDir, err := os.Getwd()
			if err == nil {
				relativeDir, err = filepath.Rel(workDir, gitDir)
			}
			if err == nil {
				return []string{relativeDir}, true
			}
		}
		// absolute should work
		return []string{gitDir}, true
	}
}

// LocalConfigDir returns the local config path components, which must be joined
// for the complete path. Returns nil when not in a valid Git repository.
//
// The shared git directory is used, so one repository has one local config that
// every linked worktree reads and writes. Git itself shares .git/config the same
// way and makes per-worktree config opt-in, and these keys describe the
// repository rather than the checkout.
//
// A worktree that already has its own file keeps using it, so a config written
// before this change is still found. In a normal checkout the two directories are
// the same and nothing changes.
func LocalConfigDir() []string {
	gitDir, ok := GitDir(true)
	if !ok {
		return nil // no valid repo, skip local config
	}

	perWorktree := slices.Concat(gitDir, []string{"glab-cli"})
	configPath := filepath.Join(slices.Concat(perWorktree, []string{"config.yml"})...)
	if CheckFileExists(configPath) {
		return perWorktree
	}

	commonDir, _ := GitCommonDir(true)

	return append(commonDir, "glab-cli")
}

// LocalConfigFile returns the local config file path.
// Returns an empty string when not in a valid Git repository.
func LocalConfigFile() string {
	dir := LocalConfigDir()
	if dir == nil {
		return ""
	}
	return filepath.Join(append(dir, "config.yml")...)
}

func (a *LocalConfig) Get(key string) (string, bool) {
	key = ConfigKeyEquivalence(key)
	if a.Empty() {
		return "", false
	}
	value, _ := a.GetStringValue(key)

	return value, value != ""
}

func (a *LocalConfig) Set(key, value string) error {
	key = ConfigKeyEquivalence(key)
	err := a.SetStringValue(key, value)
	if err != nil {
		return fmt.Errorf("failed to update config: %w", err)
	}

	err = a.Write()
	if err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

func (a *LocalConfig) Delete(key string) error {
	a.RemoveEntry(key)

	return a.Write()
}

func (a *LocalConfig) Write() error {
	// An empty dir means the config is in-memory only; nothing to persist.
	if a.dir == "" {
		return nil
	}

	// Check if it's a Git repository
	gitDir, ok := GitDir(true)
	if !ok || !CheckPathExists(filepath.Join(gitDir...)) {
		return errors.New("not a Git repository")
	}

	localConfigBytes, err := yaml.Marshal(a.ConfigMap.Root)
	if err != nil {
		return err
	}
	err = writeConfigFile(LocalConfigFile(), yamlNormalize(localConfigBytes))
	if err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

func (a *LocalConfig) All() map[string]string {
	out := map[string]string{}

	if a.Empty() {
		return out
	}

	for i := 0; i < len(a.Root.Content)-1; i += 2 {
		key := a.Root.Content[i].Value
		value := a.Root.Content[i+1].Value
		out[key] = value
	}

	return out
}
