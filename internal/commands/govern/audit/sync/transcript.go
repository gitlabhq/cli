package sync

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/claudehooks"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/gaig"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/redact"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/syncstate"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/fsx"
)

// claudeProjectsDir returns the base directory for Claude Code project transcripts.
func claudeProjectsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not find home directory: %w", err)
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// cwdToProjectHash converts the current working directory path to the
// directory name Claude Code uses for storing transcripts.
// e.g. /Users/jean/gdk/gitlab -> -Users-jean-gdk-gitlab
func cwdToProjectHash(cwd string) string {
	return strings.ReplaceAll(cwd, string(filepath.Separator), "-")
}

// transcriptPath returns the path to the JSONL transcript for a given session.
func transcriptPath(sessionID, cwd string) (string, error) {
	base, err := claudeProjectsDir()
	if err != nil {
		return "", err
	}
	projectHash := cwdToProjectHash(cwd)
	return filepath.Join(base, projectHash, sessionID+".jsonl"), nil
}

// transcriptEntry represents a single line in a Claude Code JSONL transcript.
type transcriptEntry struct {
	Type      string          `json:"type"`
	SessionID string          `json:"sessionId"`
	Timestamp string          `json:"timestamp"`
	UUID      string          `json:"uuid"`
	Message   json.RawMessage `json:"message"`
	IsMeta    bool            `json:"isMeta"`
	// IsCompactSummary marks the summary Claude Code writes of the
	// conversation so far when it compacts it, which can be from before setup.
	IsCompactSummary          bool   `json:"isCompactSummary"`
	IsVisibleInTranscriptOnly bool   `json:"isVisibleInTranscriptOnly"`
	Subtype                   string `json:"subtype"`
	CWD                       string `json:"cwd"`
	Version                   string `json:"version"`

	// user entry fields
	PromptID string `json:"promptId"`

	// system summary fields
	DurationMs   int `json:"durationMs"`
	MessageCount int `json:"messageCount"`
}

// assistantMessage represents the message field of an assistant entry.
type assistantMessage struct {
	ID      string         `json:"id"`
	Model   string         `json:"model"`
	Content []contentBlock `json:"content"`
	Usage   tokenUsage     `json:"usage"`
}

type tokenUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

// contentBlock represents a single content block in an assistant message.
type contentBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
	Text  string          `json:"text"`

	// tool_result fields
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

// sessionData holds extracted data from the transcript for a single session.
type sessionData struct {
	SessionID   string
	Goal        string
	StartedAt   time.Time
	ToolCalls   []toolCall
	ToolResults []toolResult
	Prompts     []prompt
	Responses   []llmResponse
	EndedAt     time.Time
	IsComplete  bool

	AgentVersion string
}

// empty reports whether there is nothing to post.
func (d *sessionData) empty() bool {
	return len(d.ToolCalls) == 0 && len(d.ToolResults) == 0 && len(d.Prompts) == 0 && len(d.Responses) == 0
}

// toolCall represents a single tool invocation extracted from the transcript.
type toolCall struct {
	ID   string
	Name string
	// Namespace is the tool's group as the agent recorded it, for example a Codex
	// MCP server. Empty for agents that record only a name.
	Namespace string
	Input     json.RawMessage
	Timestamp time.Time
	// CWD is the folder the agent worked in when it recorded the entry, or
	// empty when the transcript does not say. Results, prompts and responses
	// carry the same field.
	CWD string
}

// toolResult is the outcome of a tool call. Name and Duration are empty when
// the call was synced in an earlier run.
type toolResult struct {
	CallID    string
	Name      string
	Namespace string
	// Outcome overrides the outcome derived from IsError, for agents that do
	// not record whether a call succeeded.
	Outcome   string
	IsError   bool
	Error     string
	Duration  time.Duration
	Timestamp time.Time
	CWD       string
}

// prompt is a prompt the user typed.
type prompt struct {
	ID        string
	Text      string
	Timestamp time.Time
	CWD       string
}

// llmResponse is the metadata of one model response, without its content.
type llmResponse struct {
	ID        string
	Model     string
	Usage     tokenUsage
	Timestamp time.Time
	CWD       string
}

// unknownFolder is the folder of an entry that the transcript recorded in a
// folder glab could not find, such as one too far back in a Codex rollout.
// Unlike an entry that records no folder, it does not go to the session's
// project, because it may have run anywhere. No path can hold a NUL byte.
const unknownFolder = "\x00unknown"

// cleanCWD cleans a recorded folder, so the same folder always compares equal
// whichever agent or line recorded it.
func cleanCWD(dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Clean(dir)
}

// sessionMeta is written by the hooks so that `--all` can sync the session
// later without the agent's environment or working directory.
type sessionMeta struct {
	AgentType         string `json:"agent_type"`
	PathWithNamespace string `json:"path_with_namespace"`
	Host              string `json:"host"`
	// Transcript is the agent's locator for the session transcript.
	Transcript  string     `json:"transcript,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	// Parts are the GitLab sessions created for the session, one for each
	// project it worked in. The key is written even when there are none, so
	// that a record from a glab that created one GitLab session per agent
	// session can be told apart.
	Parts []sessionPart `json:"parts"`
	// FirstProject is the first project the session was recorded with. Its
	// host and top-level group bound where the session's activity goes, and it
	// never changes, even when a later hook runs in another project.
	FirstProject projectRef `json:"first_project,omitzero"`
	// Folders maps the folders the hooks ran in to the project they resolved.
	Folders map[string]projectRef `json:"folders,omitempty"`
	// PendingCalls maps the tool calls synced without their result to the
	// project their result goes to.
	PendingCalls map[string]projectRef `json:"pending_calls,omitempty"`
}

// project returns the project the session was last recorded in, which entries
// that record no folder go to.
func (m *sessionMeta) project() projectRef {
	return projectRef{Host: m.Host, PathWithNamespace: m.PathWithNamespace}
}

// anchor returns the project whose host and top-level group bound where the
// session's activity goes: its first project, or for a record from an earlier
// glab, the project it was recorded with.
func (m *sessionMeta) anchor() projectRef {
	if m.FirstProject.PathWithNamespace != "" {
		return m.FirstProject
	}
	return m.project()
}

func (m *sessionMeta) part(p projectRef) (sessionPart, bool) {
	i := slices.IndexFunc(m.Parts, func(part sessionPart) bool { return part.key() == p.key() })
	if i < 0 {
		return sessionPart{}, false
	}
	return m.Parts[i], true
}

// recordPart adds p, or replaces the part for its project.
func (m *sessionMeta) recordPart(p sessionPart) {
	i := slices.IndexFunc(m.Parts, func(part sessionPart) bool { return part.key() == p.key() })
	if i < 0 {
		m.Parts = append(m.Parts, p)
		return
	}
	m.Parts[i] = p
}

func (m *sessionMeta) recordFolder(dir string, p projectRef) {
	if m.Folders == nil {
		m.Folders = map[string]projectRef{}
	}
	m.Folders[cleanCWD(dir)] = p
}

// sessionPart is the GitLab session created for an agent session in one
// project.
type sessionPart struct {
	projectRef
	// Key is the idempotency key the GitLab session was created with.
	Key string `json:"key,omitempty"`
	// ProjectID and SessionID identify the GitLab session, so that later
	// syncs post to it without creating it again.
	ProjectID int64 `json:"project_id,omitempty"`
	SessionID int   `json:"session_id,omitempty"`
	// Unsupported reports that the project's GitLab instance does not accept
	// the agent type, so the session's activity there is never uploaded, not
	// even after an upgrade.
	Unsupported bool `json:"unsupported,omitempty"`
}

// extractPrompt returns the text the user typed in a user message, or an
// empty string for bash inputs, tool results, and other non-prompt content.
func extractPrompt(raw json.RawMessage) string {
	// Try array content format: {"role":"user","content":[{"type":"text","text":"..."}]}
	var msgArray struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &msgArray); err == nil {
		for _, block := range msgArray.Content {
			if block.Type == "text" && block.Text != "" {
				if skipPrompt(block.Text) {
					return ""
				}
				return block.Text
			}
		}
	}

	// Try string content format: {"role":"user","content":"<bash-input>..."}
	var msgString struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &msgString); err == nil && msgString.Content != "" {
		content := strings.TrimSpace(msgString.Content)
		if skipPrompt(content) {
			return ""
		}
		return content
	}

	return ""
}

// skipPrompt reports whether text is a bash input or XML-tagged content
// injected by the agent rather than typed by the user.
func skipPrompt(text string) bool {
	return strings.Contains(text, "<bash-input>") ||
		strings.Contains(text, "<bash-output>") ||
		strings.HasPrefix(strings.TrimSpace(text), "<")
}

// goalFromText returns text truncated for use as a session goal, or an empty
// string for bash inputs and XML-tagged content.
func goalFromText(text string) string {
	if skipPrompt(text) {
		return ""
	}
	text = redact.Secrets(text)
	if len(text) > 256 {
		return text[:256] + "..."
	}
	return text
}

// parseTranscript reads the complete entries of the transcript file after
// startOffset, extracts session data, and returns the offset after the last
// complete entry, so an entry that is still being written is read next time.
func parseTranscript(path string, startOffset int64) (*sessionData, int64, error) {
	data := &sessionData{}
	goalFound := startOffset > 0 // if we have a cursor, we already captured the goal
	calls := map[string]toolCall{}
	responses := map[string]int{}

	offset, err := scanJSONL(path, startOffset, func(line []byte, _ int64) {
		var entry transcriptEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return // skip malformed lines
		}

		if data.SessionID == "" && entry.SessionID != "" {
			data.SessionID = entry.SessionID
		}
		if data.AgentVersion == "" {
			data.AgentVersion = entry.Version
		}

		ts, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
		if err != nil {
			ts = time.Now() // fallback: use current time if timestamp is malformed
		}

		switch entry.Type {
		case "user":
			var msg assistantMessage
			// A prompt's content can be a plain string, which fails to decode
			// as blocks and holds no tool results.
			_ = json.Unmarshal(entry.Message, &msg)
			hasResults := false
			for _, block := range msg.Content {
				if block.Type != "tool_result" {
					continue
				}
				hasResults = true
				result := toolResult{CallID: block.ToolUseID, IsError: block.IsError, Timestamp: ts, CWD: cleanCWD(entry.CWD)}
				if call, ok := calls[block.ToolUseID]; ok {
					result.Name = call.Name
					result.Duration = ts.Sub(call.Timestamp)
					result.CWD = call.CWD
				}
				if block.IsError {
					result.Error = toolResultText(block.Content)
				}
				data.ToolResults = append(data.ToolResults, result)
			}
			if entry.IsMeta || entry.IsCompactSummary || entry.IsVisibleInTranscriptOnly || entry.PromptID == "" || hasResults {
				return
			}
			text := extractPrompt(entry.Message)
			if text == "" {
				return
			}
			data.Prompts = append(data.Prompts, prompt{ID: entry.UUID, Text: text, Timestamp: ts, CWD: cleanCWD(entry.CWD)})
			if !goalFound {
				data.Goal = goalFromText(text)
				data.StartedAt = ts
				goalFound = true
			}

		case "assistant":
			var msg assistantMessage
			if err := json.Unmarshal(entry.Message, &msg); err != nil {
				return
			}

			for _, block := range msg.Content {
				if block.Type == "tool_use" {
					call := toolCall{ID: block.ID, Name: block.Name, Input: block.Input, Timestamp: ts, CWD: cleanCWD(entry.CWD)}
					calls[block.ID] = call
					data.ToolCalls = append(data.ToolCalls, call)
				}
			}

			// A response is streamed as several entries with the same message
			// ID. The usage of the latest entry is the most complete.
			if msg.ID != "" {
				response := llmResponse{ID: msg.ID, Model: msg.Model, Usage: msg.Usage, Timestamp: ts, CWD: cleanCWD(entry.CWD)}
				if i, ok := responses[msg.ID]; ok {
					response.Timestamp = data.Responses[i].Timestamp
					response.CWD = data.Responses[i].CWD
					data.Responses[i] = response
				} else {
					responses[msg.ID] = len(data.Responses)
					data.Responses = append(data.Responses, response)
				}
			}

		case "system":
			// Session summary entry signals completion
			if entry.DurationMs > 0 {
				data.IsComplete = true
				if ts, err := time.Parse(time.RFC3339Nano, entry.Timestamp); err == nil {
					data.EndedAt = ts
				}
			}
		}
	})
	if err != nil {
		return nil, offset, err
	}

	return data, offset, nil
}

// toolResultText returns the text of a tool_result content, which is either a
// string or a list of content blocks.
func toolResultText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var blocks []contentBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// sha256File computes the SHA-256 hex string of a file's contents.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// currentCWD returns the current working directory.
func currentCWD() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("could not get current directory: %w", err)
	}
	// On macOS, resolve symlinks since Claude Code uses the resolved path
	if runtime.GOOS == "darwin" {
		if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
			cwd = resolved
		}
	}
	return cwd, nil
}

func findTranscriptForSession(sessionID string) (string, error) {
	base, err := claudeProjectsDir()
	if err != nil {
		return "", err
	}

	dirs, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}

	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		candidate := filepath.Join(base, dir.Name(), sessionID+".jsonl")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", nil
}

func metaPath(sessionID string) (string, error) {
	cp, err := syncstate.PositionPath(sessionID)
	if err != nil {
		return "", err
	}
	return cp + ".meta", nil
}

// writeSessionMeta replaces the record by a rename, so that a concurrent read
// never sees a partly written file.
func writeSessionMeta(sessionID string, meta sessionMeta) error {
	path, err := metaPath(sessionID)
	if err != nil {
		return err
	}
	if meta.Parts == nil {
		meta.Parts = []sessionPart{}
	}
	data, err := json.Marshal(meta) //nolint:forbidigo // writing to disk, not stdout
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("could not create meta directory: %w", err)
	}
	return gaig.WriteFileAtomic(path, data)
}

// withSessionMetaLock runs fn while no other glab changes a session record.
// The hooks and --all can change the same record at once, so a change reads
// and writes the record under this lock. One lock covers every session,
// because its file is never removed, and a change holds it only briefly.
func withSessionMetaLock(fn func() error) error {
	return fsx.WithLock(filepath.Join(gaig.StateDir(), "cursors", "records.lock"), fn)
}

// updateSessionMeta applies change to the recorded metadata of a session, if
// it has any.
func updateSessionMeta(sessionID string, change func(*sessionMeta)) error {
	return withSessionMetaLock(func() error {
		meta, err := readSessionMeta(sessionID)
		if errors.Is(err, errNoSessionMeta) {
			return nil
		}
		if err != nil {
			return err
		}
		change(meta)
		return writeSessionMeta(sessionID, *meta)
	})
}

// errNoSessionMeta is returned by readSessionMeta when the session has no metadata.
var errNoSessionMeta = errors.New("no session metadata")

func readSessionMeta(sessionID string) (*sessionMeta, error) {
	path, err := metaPath(sessionID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errNoSessionMeta
	}
	if err != nil {
		return nil, err
	}
	var recorded struct {
		sessionMeta
		Parts *[]sessionPart `json:"parts"`
	}
	if err := json.Unmarshal(data, &recorded); err != nil {
		return nil, err
	}
	meta := recorded.sessionMeta
	switch {
	case recorded.Parts != nil:
		if len(*recorded.Parts) > 0 {
			meta.Parts = *recorded.Parts
		}
	case meta.PathWithNamespace != "":
		// An earlier glab keyed the GitLab session with the agent session's
		// ID, so the session goes on in the GitLab session it already has.
		meta.Parts = []sessionPart{{projectRef: meta.project(), Key: sessionID}}
	}
	return &meta, nil
}

// listSessionMetas returns the IDs of sessions that have metadata.
func listSessionMetas() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(gaig.StateDir(), "cursors"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not read cursors directory: %w", err)
	}
	var ids []string
	for _, entry := range entries {
		if id, ok := strings.CutSuffix(entry.Name(), ".meta"); ok && !entry.IsDir() {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func forgetSession(sessionID string) error {
	cp, err := syncstate.PositionPath(sessionID)
	if err != nil {
		return err
	}
	start, err := syncstate.StartPath(sessionID)
	if err != nil {
		return err
	}
	var errs []error
	for _, path := range []string{cp, cp + ".meta", start} {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// sessionMetaModTime returns when a hook last recorded the session.
func sessionMetaModTime(sessionID string) (time.Time, error) {
	path, err := metaPath(sessionID)
	if err != nil {
		return time.Time{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

func markSessionCompleted(sessionID string, at time.Time) error {
	return updateSessionMeta(sessionID, func(meta *sessionMeta) {
		meta.CompletedAt = &at
	})
}

// errTranscriptNotFound is returned when a session's transcript does not exist.
var errTranscriptNotFound = errors.New("transcript not found")

// errAgentSessionNotFound is returned when the agent reports that it has no
// such session, which may also mean the job cannot see the agent's data.
var errAgentSessionNotFound = errors.New("the agent reports no such session")

// transcript is a source of agent session data.
type transcript interface {
	fmt.Stringer
	// read returns the session data recorded after cursor, and the cursor
	// to pass next time.
	read(ctx context.Context, cursor int64) (*sessionData, int64, error)
	lastActivity(ctx context.Context) (time.Time, error)
	checksum(ctx context.Context) (string, error)
}

// claudeTranscript is a Claude Code JSONL transcript. Its cursor is a byte offset.
type claudeTranscript struct {
	path string
}

func (t claudeTranscript) String() string {
	return t.path
}

func (t claudeTranscript) read(_ context.Context, cursor int64) (*sessionData, int64, error) {
	if _, err := os.Stat(t.path); errors.Is(err, fs.ErrNotExist) {
		return nil, cursor, errTranscriptNotFound
	}
	return parseTranscript(t.path, cursor)
}

func (t claudeTranscript) lastActivity(context.Context) (time.Time, error) {
	info, err := os.Stat(t.path)
	if errors.Is(err, fs.ErrNotExist) {
		return time.Time{}, errTranscriptNotFound
	}
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

func (t claudeTranscript) checksum(context.Context) (string, error) {
	return sha256File(t.path)
}

// claudeCode syncs Claude Code sessions. Their transcript locator is the
// JSONL file path.
type claudeCode struct{}

func (claudeCode) name() string { return "claude-code" }

func (claudeCode) sessionID() string { return os.Getenv("CLAUDE_CODE_SESSION_ID") }

func (claudeCode) current(sessionID string) (transcript, string, error) {
	cwd, err := currentCWD()
	if err != nil {
		return nil, "", fmt.Errorf("could not get current directory: %w", err)
	}
	path, err := transcriptPath(sessionID, cwd)
	if err != nil {
		return nil, "", fmt.Errorf("could not resolve transcript path: %w", err)
	}
	return claudeTranscript{path: path}, path, nil
}

func (claudeCode) recorded(sessionID, path string) (transcript, string, error) {
	if _, err := os.Stat(path); err == nil {
		return claudeTranscript{path: path}, path, nil
	}
	// The recorded path is derived from the hook's working directory, which
	// differs from the session's project directory if the agent changed
	// directory.
	found, err := findTranscriptForSession(sessionID)
	if err != nil {
		return nil, "", err
	}
	if found == "" {
		return claudeTranscript{path: path}, path, nil
	}
	return claudeTranscript{path: found}, found, nil
}

// canForget allows forgetting a completed Claude Code session once its
// transcript is deleted, which Claude Code does after cleanupPeriodDays.
func (claudeCode) canForget(src transcript, _ time.Time) (bool, error) {
	t, ok := src.(claudeTranscript)
	if !ok {
		return false, fmt.Errorf("unexpected Claude Code transcript type %T", src)
	}
	return fileDeleted(t.path)
}

func (claudeCode) discover(context.Context, func(string) bool) ([]discoveredSession, error) {
	return nil, nil
}

func fileDeleted(path string) (bool, error) {
	_, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	return false, err
}

func fileModTime(path string) (time.Time, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return time.Time{}, errTranscriptNotFound
	}
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

// scanJSONL calls fn for each complete line of the file at path after
// offset, with the offset where the line starts. It returns the offset after
// the last complete line, so a line still being written is read next time.
func scanJSONL(path string, offset int64, fn func(line []byte, lineOffset int64)) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return offset, fmt.Errorf("could not open transcript: %w", err)
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return offset, fmt.Errorf("could not seek transcript: %w", err)
	}

	r := bufio.NewReaderSize(f, 1024*1024)
	for {
		line, err := r.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			return offset, nil
		}
		if err != nil {
			return offset, fmt.Errorf("error reading transcript: %w", err)
		}
		fn(line[:len(line)-1], offset)
		offset += int64(len(line))
	}
}

func (claudeCode) pausedReason() (string, error) {
	settings, err := claudehooks.SettingsPath()
	if err != nil {
		return "", err
	}
	installed, err := claudehooks.SyncHookInstalled(settings)
	if err != nil {
		return "", err
	}
	if !installed {
		return "the glab Stop hook is not installed in ~/" + claudehooks.SettingsFile, nil
	}
	return "", nil
}
