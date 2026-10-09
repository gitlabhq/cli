// Add (or update) a curated remote skill entry in
// internal/commands/skills/remote/registry.yaml.
//
// Usage:
//
//	go run ./scripts/skills/add-remote <gitlab-url> [--replace] [--name <name>]
//
// By default the entry name is the SKILL.md frontmatter `name`, which must
// match the skill directory. Pass --name to publish the skill under a
// different public name; glab rewrites the frontmatter `name` to it at
// install time.
//
// The URL can be either form:
//
//	https://gitlab.com/<group>/<project>/-/tree/<ref>/<path>
//	https://gitlab.com/<group>/<project>/-/blob/<ref>/<path>/SKILL.md
//
// The script fetches SKILL.md from gitlab.com (anonymously), reads its
// frontmatter, and inserts/updates the registry entry sorted by name.
// Branch refs are recorded as "latest"; tags and SHAs are preserved as-is.
package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/pflag"
	"go.yaml.in/yaml/v3"

	"gitlab.com/gitlab-org/cli/internal/commands/skills/skill"
)

const registryPath = "internal/commands/skills/remote/registry.yaml"

type entry struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Project     string `yaml:"project"`
	Ref         string `yaml:"ref"`
	Path        string `yaml:"path"`
}

type registryFile struct {
	Version int     `yaml:"version"`
	Skills  []entry `yaml:"skills"`
}

func main() {
	// pflag (not stdlib `flag`) so that flags may appear after the
	// positional URL argument — `<url> --replace` and `--replace <url>`
	// both work.
	replace := pflag.Bool("replace", false, "Overwrite an existing entry with the same name.")
	name := pflag.String("name", "", "Public skill name, when it differs from the upstream frontmatter name.")
	pflag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: go run ./scripts/skills/add-remote <gitlab-url> [--replace] [--name <name>]\n")
		pflag.PrintDefaults()
	}
	pflag.Parse()
	if pflag.NArg() != 1 {
		pflag.Usage()
		os.Exit(2)
	}

	if err := run(pflag.Arg(0), *replace, *name); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(rawURL string, replace bool, nameOverride string) error {
	project, ref, path, err := parseURL(rawURL)
	if err != nil {
		return err
	}

	skillMD, err := fetchSkillMD(project, ref, path)
	if err != nil {
		return fmt.Errorf("fetching SKILL.md: %w", err)
	}

	name, description, err := parseFrontmatter(skillMD)
	if err != nil {
		return fmt.Errorf("parsing SKILL.md frontmatter: %w", err)
	}

	if nameOverride != "" {
		name = nameOverride
	} else if base := filepath.Base(path); base != name {
		return fmt.Errorf("frontmatter name %q does not match skill directory %q; pass --name to publish it under a different name", name, base)
	}

	rf, err := readRegistry()
	if err != nil {
		return err
	}

	e := entry{
		Name:        name,
		Description: description,
		Project:     project,
		Ref:         normalizeRef(ref),
		Path:        path,
	}

	if err := upsert(&rf, e, replace); err != nil {
		return err
	}

	if err := writeRegistry(rf); err != nil {
		return err
	}

	fmt.Printf("Added/updated %q in %s:\n", e.Name, registryPath)
	out, _ := yaml.Marshal([]entry{e})
	fmt.Printf("\n%s\n", indent(string(out), "  "))
	return nil
}

// parseURL extracts project, ref, and path from a gitlab.com tree/blob URL.
func parseURL(raw string) (string, string, string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", "", err
	}
	if u.Host != "gitlab.com" {
		return "", "", "", fmt.Errorf("URL host must be gitlab.com, got %q", u.Host)
	}

	idx := strings.Index(u.Path, "/-/")
	if idx == -1 {
		return "", "", "", fmt.Errorf("URL does not contain '/-/'; expected /-/tree/ or /-/blob/")
	}
	project := strings.Trim(u.Path[:idx], "/")
	rest := u.Path[idx+len("/-/"):]

	rest = strings.TrimPrefix(rest, "tree/")
	if after, ok := strings.CutPrefix(rest, "blob/"); ok {
		rest = after
	} else if !strings.Contains(rest[:min(len(rest), 5)], "/") {
		return "", "", "", fmt.Errorf("URL must contain /-/tree/ or /-/blob/")
	}

	ref, after, ok := strings.Cut(rest, "/")
	if !ok {
		return "", "", "", fmt.Errorf("URL is missing the path after the ref")
	}
	path := strings.TrimSuffix(after, "/SKILL.md")
	path = strings.TrimSuffix(path, "/")

	if project == "" || ref == "" || path == "" {
		return "", "", "", fmt.Errorf("could not parse project/ref/path from URL")
	}
	return project, ref, path, nil
}

// normalizeRef maps branch-looking refs to "latest". Tags (vN.N.N
// style) and 40-char SHAs are preserved verbatim. Anything else is
// also passed through — the curator can override post-hoc.
var (
	tagRe = regexp.MustCompile(`^v\d+\.\d+\.\d+(?:[-+].*)?$`)
	shaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

func normalizeRef(ref string) string {
	if tagRe.MatchString(ref) || shaRe.MatchString(ref) {
		return ref
	}
	return "latest"
}

func fetchSkillMD(project, ref, path string) ([]byte, error) {
	u := fmt.Sprintf("https://gitlab.com/%s/-/raw/%s/%s/SKILL.md", project, ref, path)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func parseFrontmatter(content []byte) (string, string, error) {
	fm, err := skill.ParseFrontmatter(content)
	if err != nil {
		return "", "", err
	}
	if fm.Name == "" {
		return "", "", fmt.Errorf("frontmatter is missing 'name'")
	}
	if fm.Description == "" {
		return "", "", fmt.Errorf("frontmatter is missing 'description'")
	}
	return fm.Name, fm.Description, nil
}

func readRegistry() (registryFile, error) {
	body, err := os.ReadFile(registryPath)
	if err != nil {
		return registryFile{}, fmt.Errorf("reading %s: %w", registryPath, err)
	}
	var rf registryFile
	if err := yaml.Unmarshal(body, &rf); err != nil {
		return registryFile{}, fmt.Errorf("parsing %s: %w", registryPath, err)
	}
	if rf.Version == 0 {
		rf.Version = 1
	}
	return rf, nil
}

func writeRegistry(rf registryFile) error {
	sort.Slice(rf.Skills, func(i, j int) bool { return rf.Skills[i].Name < rf.Skills[j].Name })

	header, err := readHeader()
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	buf.Write(header)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(rf); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return os.WriteFile(registryPath, buf.Bytes(), 0o644)
}

// readHeader preserves the leading comment block in registry.yaml so
// re-runs of the generator don't strip the human-written header.
func readHeader() ([]byte, error) {
	body, err := os.ReadFile(registryPath)
	if err != nil {
		return nil, err
	}
	var header bytes.Buffer
	for _, line := range bytes.SplitAfter(body, []byte("\n")) {
		trimmed := bytes.TrimLeft(line, " \t")
		if len(trimmed) == 0 || trimmed[0] == '#' {
			header.Write(line)
			continue
		}
		break
	}
	return header.Bytes(), nil
}

func upsert(rf *registryFile, e entry, replace bool) error {
	for i := range rf.Skills {
		if rf.Skills[i].Name == e.Name {
			if !replace {
				return fmt.Errorf("entry %q already exists; pass --replace to overwrite", e.Name)
			}
			rf.Skills[i] = e
			return nil
		}
	}
	rf.Skills = append(rf.Skills, e)
	return nil
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
