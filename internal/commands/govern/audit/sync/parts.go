package sync

import (
	"cmp"
	"context"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/gaig"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/dbg"
)

// projectRef is a GitLab project as glab names it from a Git remote.
type projectRef struct {
	Host              string `json:"host"`
	PathWithNamespace string `json:"path_with_namespace"`
}

// String returns the project as host/path, the way the sync status lists it.
func (p projectRef) String() string {
	return p.Host + "/" + p.PathWithNamespace
}

// key identifies the project in maps. GitLab hosts and paths are
// case-insensitive, and remotes can spell them differently.
func (p projectRef) key() string {
	return strings.ToLower(p.String())
}

// folderProjects finds the GitLab project of the folders that agents work in,
// from each folder's origin remote. A folder is looked up once per run.
type folderProjects struct {
	executor  cmdutils.Executor
	config    func() config.Config
	found     map[string]projectRef
	topLevels map[string]string
}

func newFolderProjects(executor cmdutils.Executor, cfg func() config.Config) *folderProjects {
	return &folderProjects{executor: executor, config: cfg, found: map[string]projectRef{}, topLevels: map[string]string{}}
}

// topLevel returns the top-level folder of the repository dir is in, or ""
// when it is in none. A folder is looked up once per run.
func (f *folderProjects) topLevel(ctx context.Context, dir string) string {
	if top, ok := f.topLevels[dir]; ok {
		return top
	}
	top, err := gaig.TopLevel(ctx, f.executor, dir)
	if err != nil {
		dbg.Debugf("%v", err)
	}
	f.topLevels[dir] = cleanCWD(top)
	return f.topLevels[dir]
}

// lookup returns the project of dir, or a zero projectRef when dir is not in a
// repository on a GitLab host glab is logged in to.
func (f *folderProjects) lookup(ctx context.Context, dir string) projectRef {
	if p, ok := f.found[dir]; ok {
		return p
	}
	host, path, err := gaig.ProjectForDir(ctx, f.executor, f.config(), dir)
	if err != nil {
		dbg.Debugf("no GitLab project for %s: %v", dir, err)
	}
	p := projectRef{Host: host, PathWithNamespace: path}
	f.found[dir] = p
	return p
}

// sessionRouter decides which project each entry of a session goes to.
type sessionRouter struct {
	// known maps the folders a hook ran in to the project the hook resolved
	// for them, with glab's remote priority rather than the origin remote. A
	// recorded folder also covers its subfolders in the same repository, so
	// a subfolder of a fork goes where the hook sent its repository, and a
	// remote that is not named origin or uses an SSH host alias still counts.
	// A repository cloned inside a recorded one is looked up on its own.
	known map[string]projectRef
	// session is the project of entries that record no folder.
	session  projectRef
	lookup   func(dir string) projectRef
	topLevel func(dir string) string
}

func (r sessionRouter) project(dir string) projectRef {
	if dir == "" {
		return r.session
	}
	if p, ok := r.known[dir]; ok {
		return p
	}
	top := ""
	for d := filepath.Dir(dir); ; d = filepath.Dir(d) {
		if p, ok := r.known[d]; ok {
			if top == "" {
				top = r.topLevel(dir)
			}
			if top != "" && r.topLevel(d) == top {
				return p
			}
		}
		if filepath.Dir(d) == d {
			return r.lookup(dir)
		}
	}
}

type projectData struct {
	project projectRef
	data    *sessionData
}

// sessionSplit is a session's data divided by project.
type sessionSplit struct {
	parts []projectData
	// dropped counts the entries from folders without a GitLab project.
	dropped int
	// pending maps the calls that have no result yet to the project their
	// result goes to, or to a zero projectRef when the call was left out.
	pending map[string]projectRef
	// answered holds the calls whose result is in data.
	answered map[string]struct{}
}

// splitByProject divides data into one part for each project its entries were
// recorded in, ordered by project. Entries whose folder has no GitLab project
// are left out. A tool result goes where its call went: the readers give a
// result its call's folder when both are in data, and pending holds the
// project of calls read in an earlier sync.
func splitByProject(data *sessionData, projectOf func(dir string) projectRef, pending map[string]projectRef) sessionSplit {
	parts := map[string]*projectData{}
	split := sessionSplit{pending: map[string]projectRef{}, answered: map[string]struct{}{}}
	add := func(p projectRef) *sessionData {
		if p.PathWithNamespace == "" {
			split.dropped++
			return nil
		}
		part, ok := parts[p.key()]
		if !ok {
			part = &projectData{project: p, data: &sessionData{
				SessionID:    data.SessionID,
				AgentVersion: data.AgentVersion,
				IsComplete:   data.IsComplete,
				EndedAt:      data.EndedAt,
			}}
			parts[p.key()] = part
		}
		return part.data
	}
	for _, p := range data.Prompts {
		if d := add(projectOf(p.CWD)); d != nil {
			d.Prompts = append(d.Prompts, p)
		}
	}
	calls := map[string]struct{}{}
	for _, c := range data.ToolCalls {
		calls[c.ID] = struct{}{}
		if d := add(projectOf(c.CWD)); d != nil {
			d.ToolCalls = append(d.ToolCalls, c)
		}
	}
	answered := split.answered
	for _, r := range data.ToolResults {
		answered[r.CallID] = struct{}{}
		p, earlier := pending[r.CallID]
		if _, ok := calls[r.CallID]; ok || !earlier {
			p = projectOf(r.CWD)
		}
		if d := add(p); d != nil {
			d.ToolResults = append(d.ToolResults, r)
		}
	}
	for _, r := range data.Responses {
		if d := add(projectOf(r.CWD)); d != nil {
			d.Responses = append(d.Responses, r)
		}
	}
	for id, p := range pending {
		if _, ok := answered[id]; !ok {
			split.pending[id] = p
		}
	}
	for _, c := range data.ToolCalls {
		// A call without a folder goes to the session's project, and so does
		// its result, so it needs no entry.
		if _, ok := answered[c.ID]; !ok && c.CWD != "" {
			split.pending[c.ID] = projectOf(c.CWD)
		}
	}

	sorted := slices.SortedFunc(maps.Values(parts), func(a, b *projectData) int {
		return cmp.Compare(a.project.key(), b.project.key())
	})
	for _, part := range sorted {
		part.data.Goal = partGoal(data, part.data)
		part.data.StartedAt = part.data.earliest()
		if len(sorted) == 1 && !data.StartedAt.IsZero() {
			part.data.StartedAt = data.StartedAt
		}
		split.parts = append(split.parts, *part)
	}
	return split
}

// partGoal returns the goal of a part: its first prompt, so that a project
// shows no prompt typed while the agent worked elsewhere. Agents whose
// transcript holds no prompts, such as OpenCode, keep the session's goal,
// because they work in one folder.
func partGoal(session, part *sessionData) string {
	if len(session.Prompts) == 0 {
		return session.Goal
	}
	for _, p := range part.Prompts {
		if goal := goalFromText(p.Text); goal != "" {
			return goal
		}
	}
	return ""
}

func (d *sessionData) earliest() time.Time {
	var first time.Time
	consider := func(ts time.Time) {
		if !ts.IsZero() && (first.IsZero() || ts.Before(first)) {
			first = ts
		}
	}
	for _, p := range d.Prompts {
		consider(p.Timestamp)
	}
	for _, c := range d.ToolCalls {
		consider(c.Timestamp)
	}
	for _, r := range d.ToolResults {
		consider(r.Timestamp)
	}
	for _, r := range d.Responses {
		consider(r.Timestamp)
	}
	return first
}
