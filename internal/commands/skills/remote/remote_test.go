//go:build !integration

package remote

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/commands/skills/skill"
)

func TestAll_LoadsShippedRegistry(t *testing.T) {
	t.Parallel()

	skills, err := All()
	require.NoError(t, err)
	for _, s := range skills {
		assert.NotEmpty(t, s.Name)
		assert.NotEmpty(t, s.Description)
		assert.Equal(t, skill.SourceRemote, s.Source)
		assert.Empty(t, s.Files, "All() returns metadata-only Skills; files come from Get()")
	}
}

func TestGet_UnknownReturnsErrNotFound(t *testing.T) {
	t.Parallel()

	_, err := Get("does-not-exist")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound, "error must wrap ErrNotFound")
}

func TestValidateEntries(t *testing.T) {
	t.Parallel()

	t.Run("valid", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, validateEntries([]Entry{{
			Name: "ok", Description: "d", Project: "g/p", Ref: "latest", Path: "x",
		}}))
	})

	t.Run("missing fields", func(t *testing.T) {
		t.Parallel()
		err := validateEntries([]Entry{{Name: "broken"}})
		require.Error(t, err)
		msg := err.Error()
		assert.Contains(t, msg, "missing 'description'")
		assert.Contains(t, msg, "missing 'project'")
		assert.Contains(t, msg, "missing 'ref'")
		assert.Contains(t, msg, "missing 'path'")
	})

	t.Run("duplicate name", func(t *testing.T) {
		t.Parallel()
		err := validateEntries([]Entry{
			{Name: "dup", Description: "d", Project: "g/p", Ref: "latest", Path: "x"},
			{Name: "dup", Description: "d", Project: "g/p", Ref: "latest", Path: "y"},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "listed more than once")
	})
}

// fakeGitLab spins up an httptest server that emulates the subset of
// gitlab.com endpoints the fetcher uses.
type fakeGitLab struct {
	server        *httptest.Server
	defaultBranch string
	files         map[string]string // path -> contents
}

func newFakeGitLab(t *testing.T, defaultBranch string, files map[string]string) *fakeGitLab {
	t.Helper()
	g := &fakeGitLab{defaultBranch: defaultBranch, files: files}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/", func(w http.ResponseWriter, r *http.Request) {
		// Project metadata: /api/v4/projects/<encoded>
		// Tree:             /api/v4/projects/<encoded>/repository/tree
		switch {
		case strings.HasSuffix(r.URL.Path, "/repository/tree"):
			g.serveTree(w, r)
		default:
			g.serveProject(w)
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Raw: /<project>/-/raw/<ref>/<filepath>
		idx := strings.Index(r.URL.Path, "/-/raw/")
		if idx == -1 {
			http.NotFound(w, r)
			return
		}
		// strip the prefix and the ref segment after /-/raw/
		rest := r.URL.Path[idx+len("/-/raw/"):]
		// rest is "<ref>/<filepath>"; drop the first path segment
		_, after, ok0 := strings.Cut(rest, "/")
		if !ok0 {
			http.NotFound(w, r)
			return
		}
		filePath := after
		body, ok := g.files[filePath]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	})
	g.server = httptest.NewServer(mux)
	t.Cleanup(g.server.Close)
	return g
}

func (g *fakeGitLab) serveProject(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"default_branch": g.defaultBranch})
}

func (g *fakeGitLab) serveTree(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	var entries []map[string]string
	for fp := range g.files {
		if !strings.HasPrefix(fp, path+"/") && fp != path {
			continue
		}
		entries = append(entries, map[string]string{"path": fp, "type": "blob"})
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Next-Page", "")
	_ = json.NewEncoder(w).Encode(entries)
}

func TestFetcher_FetchesAllFiles(t *testing.T) {
	t.Parallel()

	g := newFakeGitLab(t, "main", map[string]string{
		"skills/demo/SKILL.md":          "---\nname: demo\ndescription: a demo\n---\nbody\n",
		"skills/demo/scripts/run.sh":    "#!/bin/sh\necho hi\n",
		"skills/demo/references/REF.md": "# Reference\n",
	})

	f := &fetcher{client: http.DefaultClient, api: g.server.URL + "/api/v4", host: g.server.URL}
	s, err := f.fetch(Entry{
		Name: "demo", Description: "a demo",
		Project: "group/proj", Ref: "v1.0.0", Path: "skills/demo",
	})
	require.NoError(t, err)
	assert.Equal(t, "demo", s.Name)
	assert.Equal(t, skill.SourceRemote, s.Source)
	assert.Equal(t, "---\nname: demo\ndescription: a demo\n---\nbody\n", string(s.Files[skill.FileName]))
	assert.Equal(t, "#!/bin/sh\necho hi\n", string(s.Files["scripts/run.sh"]))
	assert.Equal(t, "# Reference\n", string(s.Files["references/REF.md"]))
}

func TestFetcher_ResolvesLatestToDefaultBranch(t *testing.T) {
	t.Parallel()

	g := newFakeGitLab(t, "main", map[string]string{
		"skills/demo/SKILL.md": "---\nname: demo\ndescription: d\n---\n",
	})

	f := &fetcher{client: http.DefaultClient, api: g.server.URL + "/api/v4", host: g.server.URL}
	ref, err := f.resolveRef("group/proj", "latest")
	require.NoError(t, err)
	assert.Equal(t, "main", ref)
}

func TestFetcher_MissingSkillMD(t *testing.T) {
	t.Parallel()

	g := newFakeGitLab(t, "main", map[string]string{
		"skills/demo/README.md": "wrong file\n",
	})

	f := &fetcher{client: http.DefaultClient, api: g.server.URL + "/api/v4", host: g.server.URL}
	_, err := f.fetch(Entry{
		Name: "demo", Description: "d",
		Project: "g/p", Ref: "v1", Path: "skills/demo",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has no "+skill.FileName)
}

func TestFetcher_RewritesFrontmatterNameToEntryName(t *testing.T) {
	t.Parallel()

	g := newFakeGitLab(t, "trunk", map[string]string{
		"skills/demo-wrapper/SKILL.md": "---\nname: demo-wrapper\ndescription: a demo\nmetadata:\n  name: keep\n---\n\nname: body line\n",
	})

	f := &fetcher{client: http.DefaultClient, api: g.server.URL + "/api/v4", host: g.server.URL}
	s, err := f.fetch(Entry{
		Name: "demo", Description: "a demo",
		Project: "g/p", Ref: "latest", Path: "skills/demo-wrapper",
	})
	require.NoError(t, err)
	assert.Equal(t, "demo", s.Name)
	assert.Equal(t, "---\nname: demo\ndescription: a demo\nmetadata:\n  name: keep\n---\n\nname: body line\n", string(s.Files[skill.FileName]))
}

func TestFetcher_RejectsSkillMDWithoutFrontmatterName(t *testing.T) {
	t.Parallel()

	g := newFakeGitLab(t, "main", map[string]string{
		"skills/demo/SKILL.md": "---\ndescription: a demo\n---\nbody\n",
	})

	f := &fetcher{client: http.DefaultClient, api: g.server.URL + "/api/v4", host: g.server.URL}
	_, err := f.fetch(Entry{
		Name: "demo", Description: "d",
		Project: "g/p", Ref: "v1", Path: "skills/demo",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing 'name'")
}

func TestFetcher_RewritesFrontmatterNamePreservingCRLF(t *testing.T) {
	t.Parallel()

	g := newFakeGitLab(t, "main", map[string]string{
		"skills/demo-wrapper/SKILL.md": "---\r\nname: demo-wrapper\r\ndescription: a demo\r\n---\r\nbody\r\n",
	})

	f := &fetcher{client: http.DefaultClient, api: g.server.URL + "/api/v4", host: g.server.URL}
	s, err := f.fetch(Entry{
		Name: "demo", Description: "a demo",
		Project: "g/p", Ref: "v1", Path: "skills/demo-wrapper",
	})
	require.NoError(t, err)
	assert.Equal(t, "---\r\nname: demo\r\ndescription: a demo\r\n---\r\nbody\r\n", string(s.Files[skill.FileName]))
}

func TestValidateEntries_RejectsInvalidNames(t *testing.T) {
	t.Parallel()

	valid := Entry{Description: "d", Project: "g/p", Ref: "latest", Path: "skills/x"}
	for _, name := range []string{"Orbit", "my_skill", "-orbit", "orbit-", "or--bit", "../x"} {
		e := valid
		e.Name = name
		require.Error(t, validateEntries([]Entry{e}), name)
	}

	e := valid
	e.Name = "orbit-2"
	require.NoError(t, validateEntries([]Entry{e}))
}
