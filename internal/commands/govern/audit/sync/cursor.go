package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/gaig"
	"gitlab.com/gitlab-org/cli/internal/dbg"
)

// cursor syncs Cursor agent sessions, which its stop and sessionEnd hooks
// record and the scheduled job also finds by scanning Cursor's agent
// transcripts. Their transcript locator is the transcript path.
type cursor struct {
	executor cmdutils.Executor
}

func (cursor) name() string { return "cursor" }

func (cursor) sessionID() string {
	id, _ := hookSession("cursor")
	return id
}

// current opens the transcript that the hook passed. Subagent transcripts, in
// <id>/subagents, are skipped, as discover skips them.
func (cursor) current(sessionID string) (transcript, string, error) {
	_, path := hookSession("cursor")
	if filepath.Base(filepath.Dir(path)) != sessionID {
		return nil, "", fmt.Errorf("%s is not the main transcript of a Cursor session", path)
	}
	return cursorTranscript{sessionID: sessionID, path: path}, path, nil
}

func (cursor) recorded(sessionID, path string) (transcript, string, error) {
	return cursorTranscript{sessionID: sessionID, path: path}, path, nil
}

func (cursor) canForget(src transcript, _ time.Time) (bool, error) {
	t, ok := src.(cursorTranscript)
	if !ok {
		return false, fmt.Errorf("unexpected Cursor transcript type %T", src)
	}
	return fileDeleted(t.path)
}

func (cursor) pausedReason() (string, error) {
	return discoveryPausedReason("cursor")
}

// discover finds the transcripts of Cursor sessions. Subagent transcripts are
// not synced in this version.
func (c cursor) discover(ctx context.Context, known func(string) bool) ([]discoveredSession, error) {
	transcripts, err := gaig.CursorTranscripts()
	if err != nil && transcripts == nil {
		return nil, err
	}
	if err != nil {
		// One unreadable workspace must not stop discovery of the others.
		dbg.Debugf("%v", err)
	}
	byWorkspace := map[string][]discoveredSession{}
	var workspaces []string
	for _, t := range transcripts {
		if known(t.ID) {
			continue
		}
		if _, ok := byWorkspace[t.Workspace]; !ok {
			workspaces = append(workspaces, t.Workspace)
		}
		byWorkspace[t.Workspace] = append(byWorkspace[t.Workspace], discoveredSession{id: t.ID, locator: t.Path})
	}

	var found []discoveredSession
	for _, workspace := range workspaces {
		sessions := byWorkspace[workspace]
		remote, skip := c.workspaceRemote(ctx, workspace, sessions)
		for i := range sessions {
			sessions[i].remote = remote
			sessions[i].skip = skip
		}
		found = append(found, sessions...)
	}
	return found, nil
}

// workspaceRemote returns the origin remote of the workspace Cursor named
// slug, or a reason to skip its sessions when the workspace is ambiguous.
func (c cursor) workspaceRemote(ctx context.Context, slug string, sessions []discoveredSession) (string, string) {
	candidates := workspaceCandidates(string(filepath.Separator), strings.TrimPrefix(slug, "-"))
	if len(candidates) == 0 {
		dbg.Debugf("could not find the Cursor workspace for %s", slug)
		return "", ""
	}

	// The folder name cannot be decoded with certainty: several directories
	// can share it, such as code/cli-docs and code/cli/docs, and a matching
	// directory can be a different one than the workspace if the workspace
	// moved. Uploading to the wrong directory's project would send the
	// sessions to another project, so the absolute paths in the sessions'
	// tool calls must point at the directory. Sessions without such paths
	// are trusted only when one directory matches.
	var paths []string
	for _, s := range sessions {
		paths = append(paths, toolCallPaths(s.locator)...)
	}
	confirmed := slices.DeleteFunc(slices.Clone(candidates), func(c string) bool { return !anyUnder(paths, c) })
	dir := ""
	switch {
	case len(confirmed) == 1:
		dir = confirmed[0]
	case len(paths) == 0 && len(candidates) == 1:
		dir = candidates[0]
	case len(candidates) == 1:
		return "", fmt.Sprintf("the Cursor workspace %s matches %s, but its sessions' files are elsewhere, so it may have moved", slug, candidates[0])
	default:
		return "", fmt.Sprintf("the Cursor workspace %s matches %d directories (%s), and its sessions do not show which", slug, len(candidates), strings.Join(candidates, ", "))
	}

	remote, err := gaig.OriginRemote(ctx, c.executor, dir)
	if err != nil {
		dbg.Debugf("cursor workspace %s: %v", dir, err)
	}
	return remote, ""
}

// anyUnder reports whether any of paths is dir or inside it.
func anyUnder(paths []string, dir string) bool {
	return slices.ContainsFunc(paths, func(p string) bool {
		return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator))
	})
}

// toolCallPaths returns the absolute paths in a Cursor transcript's tool call
// arguments.
func toolCallPaths(transcriptPath string) []string {
	var paths []string
	_, err := scanJSONL(transcriptPath, 0, func(line []byte, _ int64) {
		var entry struct {
			Message struct {
				Content []struct {
					Type  string         `json:"type"`
					Input map[string]any `json:"input"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &entry) != nil {
			return
		}
		for _, block := range entry.Message.Content {
			if block.Type != "tool_use" {
				continue
			}
			for _, v := range block.Input {
				if s, ok := v.(string); ok && filepath.IsAbs(s) {
					paths = append(paths, filepath.Clean(s))
				}
			}
		}
	})
	if err != nil {
		dbg.Debugf("could not read %s: %v", transcriptPath, err)
	}
	return paths
}

var nonAlphanumeric = regexp.MustCompile(`[^A-Za-z0-9]`)

// cursorWorkspaceRoot returns the folder of a multi-root workspace that the
// transcript at path belongs to, the way workspaceRemote decides: the one root
// that Cursor encodes as the workspace slug in path, unless the transcript's
// tool calls point elsewhere, or when no single root matches the slug, the one
// root the tool calls point into. Every later turn of a session then returns
// the same root or "", so a session never moves to another project.
func cursorWorkspaceRoot(path string, roots []string) string {
	if len(roots) == 1 {
		return roots[0]
	}
	var named []string
	sessionDir := filepath.Dir(path)
	if filepath.Base(filepath.Dir(sessionDir)) == "agent-transcripts" {
		slug := strings.TrimPrefix(filepath.Base(filepath.Dir(filepath.Dir(sessionDir))), "-")
		for _, root := range roots {
			if strings.TrimPrefix(nonAlphanumeric.ReplaceAllString(filepath.Clean(root), "-"), "-") == slug {
				named = append(named, root)
			}
		}
	}
	paths := toolCallPaths(path)
	used := slices.DeleteFunc(slices.Clone(roots), func(r string) bool { return !anyUnder(paths, filepath.Clean(r)) })
	if len(named) == 1 {
		if len(paths) == 0 || slices.Contains(used, named[0]) {
			return named[0]
		}
		return ""
	}
	if len(used) == 1 {
		return used[0]
	}
	return ""
}

// workspaceCandidates returns every directory under dir that Cursor could
// have named slug. Cursor replaces every non-alphanumeric character of the
// workspace path with "-", which cannot be reversed, so this walks the
// directories that exist and keeps the paths whose encoding matches.
func workspaceCandidates(dir, slug string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var found []string
	for _, entry := range entries {
		child := filepath.Join(dir, entry.Name())
		if ok, err := gaig.IsDir(child); err != nil || !ok {
			continue
		}
		encoded := nonAlphanumeric.ReplaceAllString(entry.Name(), "-")
		if slug == encoded {
			found = append(found, child)
			continue
		}
		if rest, ok := strings.CutPrefix(slug, encoded+"-"); ok {
			found = append(found, workspaceCandidates(child, rest)...)
		}
	}
	return found
}

// cursorTranscript is a Cursor agent transcript. Its cursor is a byte offset.
// Cursor records neither tool call IDs nor timestamps, so a call's ID is
// derived from its position in the file, and its time is the transcript's
// last modification.
type cursorTranscript struct {
	sessionID string
	path      string
}

func (t cursorTranscript) String() string { return t.path }

func (t cursorTranscript) read(_ context.Context, from int64) (*sessionData, int64, error) {
	modified, err := fileModTime(t.path)
	if err != nil {
		return nil, from, err
	}
	data := &sessionData{}
	offset, err := scanJSONL(t.path, from, func(line []byte, lineOffset int64) {
		t.parseLine(line, lineOffset, modified, data, from > 0)
	})
	return data, offset, err
}

var userQueryRE = regexp.MustCompile(`(?s)<user_query>\s*(.*?)\s*</user_query>`)

func (t cursorTranscript) parseLine(line []byte, lineOffset int64, modified time.Time, data *sessionData, goalFound bool) {
	var entry struct {
		Role    string `json:"role"`
		Message struct {
			Content []struct {
				Type  string          `json:"type"`
				Text  string          `json:"text"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &entry) != nil {
		return
	}
	for i, block := range entry.Message.Content {
		id := fmt.Sprintf("cursor:%s:%d:%d", t.sessionID, lineOffset, i)
		switch {
		case entry.Role == "user" && block.Type == "text":
			text := block.Text
			// Cursor wraps the prompt in <user_query> after injected context.
			if m := userQueryRE.FindStringSubmatch(text); m != nil {
				text = m[1]
			}
			text = strings.TrimSpace(text)
			if text == "" || skipPrompt(text) {
				continue
			}
			data.Prompts = append(data.Prompts, prompt{ID: id, Text: text, Timestamp: modified})
			if !goalFound && data.Goal == "" {
				data.Goal = goalFromText(text)
			}
		case entry.Role == "assistant" && block.Type == "tool_use":
			data.ToolCalls = append(data.ToolCalls, toolCall{
				ID:        id,
				Name:      block.Name,
				Input:     block.Input,
				Timestamp: modified,
			})
		}
	}
}

func (t cursorTranscript) lastActivity(context.Context) (time.Time, error) {
	return fileModTime(t.path)
}

func (t cursorTranscript) checksum(context.Context) (string, error) {
	return sha256File(t.path)
}
