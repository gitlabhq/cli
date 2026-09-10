//go:build !integration

package bundled

import (
	"path"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/commands/api"
	"gitlab.com/gitlab-org/cli/internal/commands/skills/skill"
)

func TestAll(t *testing.T) {
	t.Parallel()

	skills, err := All()
	require.NoError(t, err)
	require.NotEmpty(t, skills)

	for _, s := range skills {
		assert.NotEmpty(t, s.Name, "skill name should be set")
		assert.NotEmpty(t, s.Description, "skill description should be set")
		assert.Equal(t, skill.SourceBundled, s.Source, "Source should be set to bundled")
		assert.NotEmpty(t, s.Files[skill.FileName], "skill must include %s", skill.FileName)
		assert.NotEmpty(t, s.SkillFile(), "SkillFile() must return SKILL.md content")
	}
}

func TestGet_Known(t *testing.T) {
	t.Parallel()

	s, err := Get("glab")
	require.NoError(t, err)
	assert.Equal(t, "glab", s.Name)
	assert.Equal(t, skill.SourceBundled, s.Source)
	assert.NotEmpty(t, s.Description)
	assert.Contains(t, string(s.SkillFile()), "name: glab")
}

func TestGet_Unknown(t *testing.T) {
	t.Parallel()

	_, err := Get("does-not-exist")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound, "error should wrap ErrNotFound")
}

func TestRelPath(t *testing.T) {
	t.Parallel()

	t.Run("relative under root", func(t *testing.T) {
		t.Parallel()
		rel, err := relPath("assets/glab", "assets/glab/SKILL.md")
		require.NoError(t, err)
		assert.Equal(t, "SKILL.md", rel)
	})

	t.Run("nested under root", func(t *testing.T) {
		t.Parallel()
		rel, err := relPath("assets/glab", "assets/glab/scripts/run.sh")
		require.NoError(t, err)
		assert.Equal(t, "scripts/run.sh", rel)
	})

	t.Run("rejects parent traversal", func(t *testing.T) {
		t.Parallel()
		// path.Clean turns "assets/glab/../other" into "assets/other"
		// which doesn't start with "assets/glab/".
		_, err := relPath("assets/glab", path.Clean("assets/glab/../other/SKILL.md"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not under skill root")
	})

	t.Run("rejects sibling directory", func(t *testing.T) {
		t.Parallel()
		_, err := relPath("assets/glab", "assets/glab-stack/SKILL.md")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not under skill root")
	})

	t.Run("rejects path equal to root", func(t *testing.T) {
		t.Parallel()
		_, err := relPath("assets/glab", "assets/glab")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "equals skill root")
	})

	t.Run("rejects absolute path", func(t *testing.T) {
		t.Parallel()
		_, err := relPath("assets/glab", "/etc/passwd")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not under skill root")
	})
}

func TestParseFrontmatter(t *testing.T) {
	t.Parallel()

	t.Run("valid", func(t *testing.T) {
		t.Parallel()

		content := []byte("---\nname: foo\ndescription: bar baz\n---\nbody\n")
		fm, err := parseFrontmatter(content)
		require.NoError(t, err)
		assert.Equal(t, "foo", fm.Name)
		assert.Equal(t, "bar baz", fm.Description)
	})

	t.Run("missing leading delimiter", func(t *testing.T) {
		t.Parallel()

		_, err := parseFrontmatter([]byte("name: foo\n"))
		require.Error(t, err)
	})

	t.Run("missing closing delimiter", func(t *testing.T) {
		t.Parallel()

		_, err := parseFrontmatter([]byte("---\nname: foo\n"))
		require.Error(t, err)
	})
}

// A bundled skill is instructions an agent follows literally, so an example that
// names a placeholder `glab api` does not expand is not a typo -- the agent sends
// it verbatim and gets HTTP 400. `:iid` shipped in five examples this way (#8531).
//
// The allowed set comes from api.Placeholders rather than a copy of it, so a
// placeholder added or removed there cannot silently disagree with the docs.
func TestBundledSkillsOnlyUseRealPlaceholders(t *testing.T) {
	t.Parallel()

	allowed := map[string]bool{}
	for _, p := range api.Placeholders {
		// Compound entries such as "group/:namespace/:repo" are matched one
		// token at a time by the expander, so record their atoms.
		for atom := range strings.SplitSeq(p, "/:") {
			allowed[atom] = true
		}
	}

	// A placeholder is a colon followed by a bare word. "https://", ": " in a
	// header, and `"key":"value"` in JSON all fail to match, which is what keeps
	// this to actual placeholder positions.
	tokenRE := regexp.MustCompile(`:([A-Za-z_][A-Za-z0-9_]*)`)

	skills, err := All()
	require.NoError(t, err)
	require.NotEmpty(t, skills)

	for _, s := range skills {
		for name, contents := range s.Files {
			if path.Ext(name) != ".md" {
				continue
			}
			lineNo := 0
			for line := range strings.SplitSeq(string(contents), "\n") {
				lineNo++
				if !strings.Contains(line, "glab api") {
					continue
				}
				for _, m := range tokenRE.FindAllStringSubmatch(line, -1) {
					assert.Truef(t, allowed[m[1]],
						"%s:%d uses %q, which glab api does not expand and sends verbatim:\n\t%s",
						name, lineNo, m[0], strings.TrimSpace(line))
				}
			}
		}
	}
}
