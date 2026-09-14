package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gitlab.com/gitlab-org/cli/internal/commands/skills/skill"
)

// gitlabHost is hardcoded — curated remote skills always live on
// gitlab.com regardless of where the user's `glab` is configured.
const (
	gitlabHost     = "https://gitlab.com"
	apiV4          = gitlabHost + "/api/v4"
	defaultTimeout = 30 * time.Second
	// treePageSize matches GitLab's max per_page for repository listings.
	treePageSize = 100
)

// fetcher pulls a skill's full file tree from gitlab.com via the
// public Repository APIs (no auth). It is constructed per-Get so the
// HTTP client can be swapped in tests.
type fetcher struct {
	client *http.Client
	api    string
	host   string
}

func newFetcher(client *http.Client) *fetcher {
	if client == nil {
		client = http.DefaultClient
	}
	return &fetcher{
		client: client,
		api:    apiV4,
		host:   gitlabHost,
	}
}

// fetch resolves the entry's ref (handling the `latest` shortcut) and
// downloads every blob under entry.Path, returning a Skill keyed by
// paths relative to entry.Path.
func (f *fetcher) fetch(e Entry) (skill.Skill, error) {
	ref, err := f.resolveRef(e.Project, e.Ref)
	if err != nil {
		return skill.Skill{}, err
	}

	tree, err := f.listTree(e.Project, e.Path, ref)
	if err != nil {
		return skill.Skill{}, fmt.Errorf("listing %s@%s:%s: %w", e.Project, ref, e.Path, err)
	}

	files := map[string][]byte{}
	for _, te := range tree {
		if te.Type != "blob" {
			continue
		}
		body, err := f.getRaw(e.Project, ref, te.Path)
		if err != nil {
			return skill.Skill{}, fmt.Errorf("fetching %s: %w", te.Path, err)
		}
		rel, err := relPath(e.Path, te.Path)
		if err != nil {
			return skill.Skill{}, err
		}
		files[rel] = body
	}

	if _, ok := files[skill.FileName]; !ok {
		return skill.Skill{}, fmt.Errorf("remote skill %q at %s@%s:%s has no %s", e.Name, e.Project, ref, e.Path, skill.FileName)
	}

	return skill.Skill{
		Name:        e.Name,
		Description: e.Description,
		Source:      skill.SourceRemote,
		Files:       files,
	}, nil
}

// resolveRef expands the `latest` shortcut to the project's default
// branch. Other ref strings (tags, SHAs, branch names) pass through.
func (f *fetcher) resolveRef(project, ref string) (string, error) {
	if ref != "latest" {
		return ref, nil
	}
	u := f.api + "/projects/" + url.PathEscape(project)
	body, err := f.getJSON(u)
	if err != nil {
		return "", fmt.Errorf("resolving 'latest' for %s: %w", project, err)
	}
	var info struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return "", fmt.Errorf("parsing project metadata for %s: %w", project, err)
	}
	if info.DefaultBranch == "" {
		return "", fmt.Errorf("project %s has no default_branch", project)
	}
	return info.DefaultBranch, nil
}

type treeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"` // "blob" | "tree" | "commit"
}

// listTree pages through /repository/tree until exhausted.
func (f *fetcher) listTree(project, path, ref string) ([]treeEntry, error) {
	var all []treeEntry
	base := f.api + "/projects/" + url.PathEscape(project) + "/repository/tree"

	for page := 1; ; page++ {
		q := url.Values{}
		q.Set("path", path)
		q.Set("ref", ref)
		q.Set("recursive", "true")
		q.Set("per_page", strconv.Itoa(treePageSize))
		q.Set("page", strconv.Itoa(page))

		body, hdr, err := f.getJSONWithHeaders(base + "?" + q.Encode())
		if err != nil {
			return nil, err
		}

		var batch []treeEntry
		if err := json.Unmarshal(body, &batch); err != nil {
			return nil, fmt.Errorf("parsing tree response: %w", err)
		}
		all = append(all, batch...)

		next := hdr.Get("X-Next-Page")
		if next == "" || next == "0" {
			break
		}
	}
	return all, nil
}

// getRaw fetches a single file's raw bytes via the non-API raw URL,
// which avoids URL-encoding the path with %2F separators.
func (f *fetcher) getRaw(project, ref, path string) ([]byte, error) {
	u := f.host + "/" + project + "/-/raw/" + ref + "/" + path
	body, _, err := f.httpGet(u, nil)
	return body, err
}

func (f *fetcher) getJSON(u string) ([]byte, error) {
	body, _, err := f.httpGet(u, http.Header{"Accept": []string{"application/json"}})
	return body, err
}

func (f *fetcher) getJSONWithHeaders(u string) ([]byte, http.Header, error) {
	return f.httpGet(u, http.Header{"Accept": []string{"application/json"}})
}

// httpGet issues a GET against u with the given headers, reads the body,
// and returns the bytes + response headers. The per-request timeout is
// applied via context only when neither the client nor the parent
// context already carries a deadline; the cancel scope spans the body
// read so a default-timed fetch isn't cut off mid-stream.
func (f *fetcher) httpGet(u string, headers http.Header) ([]byte, http.Header, error) {
	ctx := context.Background()
	if _, ok := ctx.Deadline(); !ok && f.client.Timeout == 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultTimeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, nil, err
	}
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("GET %s: %s", u, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	return body, resp.Header, nil
}

func relPath(root, full string) (string, error) {
	if full == root {
		return "", fmt.Errorf("tree entry %q equals skill root", full)
	}
	if !strings.HasPrefix(full, root+"/") {
		return "", fmt.Errorf("tree entry %q is not under skill root %q", full, root)
	}
	return full[len(root)+1:], nil
}
