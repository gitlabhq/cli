package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gitlab.com/gitlab-org/cli/internal/fsx"
)

// Each per-manager block below stores registry URLs the firewall should
// route through. The two field names have consistent meaning across
// managers:
//
//   - RepoResolve is the URL the manager should fetch packages FROM
//     (the "resolve" or read side; typically a virtual/proxy registry
//     that pulls upstream through the firewall).
//   - RepoDeploy is the URL the manager should publish packages TO
//     (the "deploy" or write side; typically a local hosted registry).
//
// Not every manager uses both — later stack slices add fetch-only and
// publish-only manager types; each one only carries the fields it uses.

type NPM struct {
	RepoResolve string `json:"repoResolve,omitempty"`
	RepoDeploy  string `json:"repoDeploy,omitempty"`
}

type Config struct {
	NPM NPM `json:"npm"`
}

func Path(baseDir string) string {
	return filepath.Join(baseDir, ".gitlab", "df", "config.json")
}

// Load reads .gitlab/df/config.json under baseDir. A missing file is not an
// error: it returns the zero-value Config so callers treat "no config yet" as
// empty defaults (the first `glab dependency-firewall <manager> config` call
// on a repo hits this path). Any other read or unmarshal failure is returned.
func Load(baseDir string) (Config, error) {
	var c Config
	raw, err := os.ReadFile(Path(baseDir))
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, err
	}
	return c, nil
}

// Merge updates the block named managerKey in the config file, setting only the
// non-nil fields, and leaves all other manager blocks and unknown keys intact.
// Merge is intentionally generic: it does not know which fields a given manager
// supports. Callers are responsible for passing only applicable fields — for
// example, a manager that cannot deploy through the firewall passes a nil
// repoDeploy.
func Merge(baseDir, managerKey string, repoResolve, repoDeploy *string) error {
	if repoResolve == nil && repoDeploy == nil {
		return nil
	}

	raw, err := os.ReadFile(Path(baseDir))
	merged := map[string]any{}
	if err == nil {
		if err := json.Unmarshal(raw, &merged); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	block := map[string]any{}
	if existing, ok := merged[managerKey]; ok {
		m, isObj := existing.(map[string]any)
		if !isObj {
			return fmt.Errorf("config file has unexpected type for %q key: %T", managerKey, existing)
		}
		block = m
	}
	if repoResolve != nil {
		block["repoResolve"] = *repoResolve
	}
	if repoDeploy != nil {
		block["repoDeploy"] = *repoDeploy
	}
	merged[managerKey] = block

	return fsx.WriteJSONFile(Path(baseDir), merged)
}
