// Package remote is the source for agent skills hosted on gitlab.com and
// listed in this repo's curated registry. Discovery (`All`) is offline —
// metadata comes from the embedded registry.yaml. Installation (`Get`)
// reaches gitlab.com to fetch the actual SKILL.md and supporting files
// at the pinned ref.
package remote

import (
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"sync"

	"go.yaml.in/yaml/v3"

	"gitlab.com/gitlab-org/cli/internal/commands/skills/skill"
)

//go:embed registry.yaml
var registryYAML []byte

// supportedRegistryVersion is the only registry schema version this
// build understands. Bump it (and the loader) if the format ever needs
// breaking changes; older glab binaries should refuse unknown versions
// rather than silently misread them.
const supportedRegistryVersion = 1

// ErrNotFound is returned by Get when no entry in the curated remote
// registry matches the requested name.
var ErrNotFound = errors.New("not in remote registry")

// Entry is a single row in registry.yaml — the curated pointer to a
// remote skill. The description exists purely so `skills list` can
// render without a network call. Name is the public skill name and
// install directory; when the upstream SKILL.md frontmatter names the
// skill differently, the fetcher rewrites it to Name at install time.
type Entry struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Project     string `yaml:"project"`
	Ref         string `yaml:"ref"`
	Path        string `yaml:"path"`
}

type registryFile struct {
	Version int     `yaml:"version"`
	Skills  []Entry `yaml:"skills"`
}

// skillNamePattern is the name format required by the Agent Skills spec.
var skillNamePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

var (
	loadOnce sync.Once
	entries  []Entry
	loadErr  error
)

// All returns every registered remote skill as metadata-only Skills
// (Files is nil). It performs no network I/O.
func All() ([]skill.Skill, error) {
	loadOnce.Do(load)
	if loadErr != nil {
		return nil, loadErr
	}
	out := make([]skill.Skill, 0, len(entries))
	for _, e := range entries {
		out = append(out, skill.Skill{
			Name:        e.Name,
			Description: e.Description,
			Source:      skill.SourceRemote,
		})
	}
	return out, nil
}

// Get fetches the named remote skill from gitlab.com and returns it
// with files populated. Returns an error if the registry doesn't list
// the name or the fetch fails.
func Get(name string) (skill.Skill, error) {
	loadOnce.Do(load)
	if loadErr != nil {
		return skill.Skill{}, loadErr
	}

	var entry *Entry
	for i := range entries {
		if entries[i].Name == name {
			entry = &entries[i]
			break
		}
	}
	if entry == nil {
		return skill.Skill{}, fmt.Errorf("%w: %q", ErrNotFound, name)
	}

	f := newFetcher(http.DefaultClient)
	return f.fetch(*entry)
}

func load() {
	var rf registryFile
	if err := yaml.Unmarshal(registryYAML, &rf); err != nil {
		loadErr = fmt.Errorf("parsing remote skills registry: %w", err)
		return
	}

	if rf.Version != supportedRegistryVersion {
		loadErr = fmt.Errorf("remote skills registry version %d is unsupported (expected %d)", rf.Version, supportedRegistryVersion)
		return
	}

	if err := validateEntries(rf.Skills); err != nil {
		loadErr = err
		return
	}

	// Defensive: sort so All() output is deterministic regardless of
	// how the generator wrote registry.yaml. The generator already
	// sorts on insert, but we don't want to rely on that contract.
	sort.Slice(rf.Skills, func(i, j int) bool { return rf.Skills[i].Name < rf.Skills[j].Name })
	entries = rf.Skills
}

// validateEntries checks per-entry sanity and cross-entry uniqueness.
// We intentionally don't enforce ref format here — `latest`, tags, and
// SHAs are all valid; per-entry tightening is a hand-review decision.
func validateEntries(es []Entry) error {
	seen := map[string]bool{}
	var errs []error
	for i, e := range es {
		if e.Name == "" {
			errs = append(errs, fmt.Errorf("entry %d: missing 'name'", i))
			continue
		}
		if !skillNamePattern.MatchString(e.Name) {
			errs = append(errs, fmt.Errorf("entry %q: name must be lowercase letters, digits, and single hyphens", e.Name))
		}
		if e.Description == "" {
			errs = append(errs, fmt.Errorf("entry %q: missing 'description'", e.Name))
		}
		if e.Project == "" {
			errs = append(errs, fmt.Errorf("entry %q: missing 'project'", e.Name))
		}
		if e.Ref == "" {
			errs = append(errs, fmt.Errorf("entry %q: missing 'ref'", e.Name))
		}
		if e.Path == "" {
			errs = append(errs, fmt.Errorf("entry %q: missing 'path'", e.Name))
		}
		if seen[e.Name] {
			errs = append(errs, fmt.Errorf("entry %q is listed more than once", e.Name))
		}
		seen[e.Name] = true
	}
	return errors.Join(errs...)
}
