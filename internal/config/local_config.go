package config

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

type LocalConfig struct {
	ConfigMap
	Parent Config
	dir    string
}

// for later use we might prefer relative paths
// GitDir will find a directory or just return ".git"
func GitDir(preferRelative bool) []string {
	// `git rev-parse --git-dir` since git v1.2.2
	return revParseDir("--git-dir", preferRelative)
}

// GitCommonDir will find the directory every linked worktree of a repository has
// in common, or just return ".git". In a normal checkout it is the same directory
// GitDir returns; in a linked worktree GitDir is .git/worktrees/<name> while this
// is the main repository's .git. A submodule keeps its own, .git/modules/<name>.
func GitCommonDir(preferRelative bool) []string {
	// `git rev-parse --git-common-dir` since git v2.5, which introduced worktrees
	return revParseDir("--git-common-dir", preferRelative)
}

// revParseDir resolves one `git rev-parse` directory flag to the caller's
// preferred path form, falling back to ".git" when git cannot answer.
func revParseDir(revParseFlag string, preferRelative bool) []string {
	var err error
	var out strings.Builder

	cmd := exec.Command("git", "rev-parse", revParseFlag)

	cmd.Stdout = &out
	err = cmd.Run()
	if err != nil {
		// should fail
		return []string{".git"}
	}

	gitDir := strings.TrimSpace(out.String())

	if !filepath.IsAbs(gitDir) {
		// gitDir is relative
		if !preferRelative {
			// but we prefer absolute
			workDir, err := os.Getwd()
			if err == nil {
				return []string{workDir, gitDir}
			}
		}
		// relative should work, cleaned because git answers with forward slashes
		return []string{filepath.Clean(gitDir)}
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
				return []string{relativeDir}
			}
		}
		// absolute should work
		return []string{gitDir}
	}
}

// LocalConfigDir returns the local config path in map
// which must be joined for complete path.
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
	perWorktree := append(GitDir(true), "glab-cli")
	if CheckFileExists(filepath.Join(append(perWorktree, "config.yml")...)) {
		return perWorktree
	}

	return append(GitCommonDir(true), "glab-cli")
}

// LocalConfigFile returns the config file name with full path
func LocalConfigFile() string {
	configFile := append(LocalConfigDir(), "config.yml")
	return filepath.Join(configFile...)
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
	if !CheckPathExists(filepath.Join(GitDir(true)...)) {
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
