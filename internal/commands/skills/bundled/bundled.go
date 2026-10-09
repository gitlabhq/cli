// Package bundled is the source for agent skills that ship inside the
// glab binary via go:embed. The skill type itself lives in the sibling
// `skill` package; this package only knows how to discover and load
// from the embedded FS.
//
// The on-disk layout under assets/ follows the Agent Skills specification
// (https://agentskills.io/specification): each top-level directory is a skill
// whose name matches the directory, and must contain a SKILL.md plus any
// optional supporting files (scripts/, references/, assets/, etc.).
package bundled

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"

	"gitlab.com/gitlab-org/cli/internal/commands/skills/skill"
)

// ErrNotFound is returned by Get when no bundled skill has the
// requested name. Callers can errors.Is against it to distinguish
// "this source doesn't have it" from a registry-load failure.
var ErrNotFound = errors.New("not in bundled skills")

const assetsDir = "assets"

//go:embed all:assets
var fsys embed.FS

var (
	loadOnce sync.Once
	loaded   []skill.Skill
	loadErr  error
)

// All returns every bundled skill, sorted by name. Files are populated.
func All() ([]skill.Skill, error) {
	loadOnce.Do(load)
	if loadErr != nil {
		return nil, loadErr
	}
	out := make([]skill.Skill, len(loaded))
	copy(out, loaded)
	return out, nil
}

// Get returns the bundled skill with the given name.
func Get(name string) (skill.Skill, error) {
	skills, err := All()
	if err != nil {
		return skill.Skill{}, err
	}
	for _, s := range skills {
		if s.Name == name {
			return s, nil
		}
	}
	return skill.Skill{}, fmt.Errorf("%w: %q", ErrNotFound, name)
}

func load() {
	entries, err := fs.ReadDir(fsys, assetsDir)
	if err != nil {
		loadErr = fmt.Errorf("reading bundled skills: %w", err)
		return
	}

	skills := make([]skill.Skill, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		s, err := loadSkill(entry.Name())
		if err != nil {
			loadErr = err
			return
		}
		skills = append(skills, s)
	}

	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	loaded = skills
}

func loadSkill(dirName string) (skill.Skill, error) {
	skillRoot := path.Join(assetsDir, dirName)
	files, err := collectFiles(skillRoot)
	if err != nil {
		return skill.Skill{}, fmt.Errorf("reading skill %q: %w", dirName, err)
	}

	skillMD, ok := files[skill.FileName]
	if !ok {
		return skill.Skill{}, fmt.Errorf("skill %q is missing required %s", dirName, skill.FileName)
	}

	fm, err := skill.ParseFrontmatter(skillMD)
	if err != nil {
		return skill.Skill{}, fmt.Errorf("parsing frontmatter in %s/%s: %w", dirName, skill.FileName, err)
	}
	// Minimum checks needed for load to function — name keys the registry
	// and must match the directory; description is rendered by `skills list`.
	// Full Agent Skills spec compliance (regex, length caps, etc.) is left to
	// hand-review and upstream tooling.
	if fm.Name == "" {
		return skill.Skill{}, fmt.Errorf("%s/%s: frontmatter is missing 'name'", dirName, skill.FileName)
	}
	if fm.Name != dirName {
		return skill.Skill{}, fmt.Errorf("%s/%s: frontmatter name %q does not match directory %q", dirName, skill.FileName, fm.Name, dirName)
	}
	if fm.Description == "" {
		return skill.Skill{}, fmt.Errorf("%s/%s: frontmatter is missing 'description'", dirName, skill.FileName)
	}

	return skill.Skill{
		Name:        fm.Name,
		Description: fm.Description,
		Source:      skill.SourceBundled,
		Files:       files,
	}, nil
}

// collectFiles walks the embedded skill directory and returns every regular
// file keyed by its path relative to the skill root.
//
// Paths are canonicalized with path.Clean and verified to be rooted under
// skillRoot before the file is read. embed.FS doesn't expose symlinks or
// allow ".." references at build time, so today this is defensive coding
// rather than a real attack surface — but it documents the invariant and
// guards against future refactors that swap embed.FS for a real
// filesystem source.
func collectFiles(skillRoot string) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := fs.WalkDir(fsys, skillRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		cleaned := path.Clean(p)
		rel, err := relPath(skillRoot, cleaned)
		if err != nil {
			return err
		}
		content, err := fs.ReadFile(fsys, cleaned)
		if err != nil {
			return err
		}
		files[rel] = content
		return nil
	})
	return files, err
}

// relPath returns full's path relative to root, rejecting anything that
// would resolve outside root (e.g. "..", absolute paths, or sibling
// directories). full must already be cleaned via path.Clean.
func relPath(root, full string) (string, error) {
	if full == root {
		return "", fmt.Errorf("unexpected: file path %q equals skill root", full)
	}
	if !strings.HasPrefix(full, root+"/") {
		return "", fmt.Errorf("path %q is not under skill root %q", full, root)
	}
	return full[len(root)+1:], nil
}
