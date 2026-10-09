package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/dbg"
)

// cursor syncs Cursor agent sessions, which the scheduled job finds by
// scanning Cursor's agent transcripts. Their transcript locator is the
// transcript path.
type cursor struct {
	executor cmdutils.Executor
}

func (cursor) name() string { return "cursor" }

// sessionID reports no session, because Cursor sessions are only discovered.
func (cursor) sessionID() string { return "" }

func (cursor) current(string) (transcript, string, error) {
	return nil, "", errors.New("cursor sessions are discovered, not recorded by a hook")
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

func cursorProjectsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cursor", "projects"), nil
}

// discover finds transcripts at
// projects/<workspace slug>/agent-transcripts/<id>/<id>.jsonl. Subagent
// transcripts, under <id>/subagents, are not synced in this version.
func (c cursor) discover(ctx context.Context, known func(string) bool) ([]discoveredSession, error) {
	root, err := cursorProjectsDir()
	if err != nil {
		return nil, err
	}
	projects, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var found []discoveredSession
	for _, project := range projects {
		if !project.IsDir() {
			continue
		}
		transcripts := filepath.Join(root, project.Name(), "agent-transcripts")
		entries, err := os.ReadDir(transcripts)
		if err != nil {
			continue
		}
		var sessions []discoveredSession
		for _, entry := range entries {
			id := entry.Name()
			path := filepath.Join(transcripts, id, id+".jsonl")
			if !entry.IsDir() || known(id) {
				continue
			}
			if _, err := os.Stat(path); err != nil {
				continue
			}
			sessions = append(sessions, discoveredSession{id: id, locator: path})
		}
		if len(sessions) == 0 {
			continue
		}
		remote, skip := c.workspaceRemote(ctx, project.Name(), sessions)
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

	remote, err := gitRemote(ctx, c.executor, dir)
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
		if !isDir(entry, child) {
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

// isDir reports whether entry is a directory or a symlink to one, such as
// /var and /tmp on macOS.
func isDir(entry fs.DirEntry, path string) bool {
	if entry.IsDir() {
		return true
	}
	if entry.Type()&fs.ModeSymlink == 0 {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
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
