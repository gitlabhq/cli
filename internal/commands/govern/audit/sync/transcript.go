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
	"strconv"
	"strings"
	"time"

	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/claudehooks"
	"gitlab.com/gitlab-org/cli/internal/config"
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

// cursorPath returns the path to the cursor file for a given session.
func cursorPath(sessionID string) (string, error) {
	if strings.ContainsAny(sessionID, "/\\..") {
		return "", fmt.Errorf("invalid session ID: %q", sessionID)
	}
	return filepath.Join(config.ConfigDir(), "gaig", "cursors", sessionID), nil
}

// readCursor returns the byte offset of the last synced position for a session.
// Returns 0 if no cursor exists.
func readCursor(sessionID string) (int64, error) {
	path, err := cursorPath(sessionID)
	if err != nil {
		return 0, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("could not read cursor: %w", err)
	}

	offset, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("could not parse cursor: %w", err)
	}

	return offset, nil
}

// writeCursor saves the current byte offset as the cursor for a session.
func writeCursor(sessionID string, offset int64) error {
	path, err := cursorPath(sessionID)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("could not create cursor directory: %w", err)
	}

	return os.WriteFile(path, []byte(strconv.FormatInt(offset, 10)), 0o644)
}

// transcriptEntry represents a single line in a Claude Code JSONL transcript.
type transcriptEntry struct {
	Type      string          `json:"type"`
	SessionID string          `json:"sessionId"`
	Timestamp string          `json:"timestamp"`
	UUID      string          `json:"uuid"`
	Message   json.RawMessage `json:"message"`
	IsMeta    bool            `json:"isMeta"`
	Subtype   string          `json:"subtype"`
	CWD       string          `json:"cwd"`

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
}

// empty reports whether there is nothing to post.
func (d *sessionData) empty() bool {
	return len(d.ToolCalls) == 0 && len(d.ToolResults) == 0 && len(d.Prompts) == 0 && len(d.Responses) == 0
}

// toolCall represents a single tool invocation extracted from the transcript.
type toolCall struct {
	ID        string
	Name      string
	Input     json.RawMessage
	Timestamp time.Time
}

// toolResult is the outcome of a tool call. Name and Duration are empty when
// the call was synced in an earlier run.
type toolResult struct {
	CallID string
	Name   string
	// Outcome overrides the outcome derived from IsError, for agents that do
	// not record whether a call succeeded.
	Outcome   string
	IsError   bool
	Error     string
	Duration  time.Duration
	Timestamp time.Time
}

// prompt is a prompt the user typed.
type prompt struct {
	ID        string
	Text      string
	Timestamp time.Time
}

// llmResponse is the metadata of one model response, without its content.
type llmResponse struct {
	ID        string
	Model     string
	Usage     tokenUsage
	Timestamp time.Time
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
	if len(text) > 256 {
		return text[:256] + "..."
	}
	return text
}

// parseTranscript reads new entries from the transcript file starting at offset,
// extracts session data, and returns the new offset.
func parseTranscript(path string, startOffset int64) (*sessionData, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("could not open transcript: %w", err)
	}
	defer f.Close()

	if _, err := f.Seek(startOffset, io.SeekStart); err != nil {
		return nil, 0, fmt.Errorf("could not seek transcript: %w", err)
	}

	data := &sessionData{}
	scanner := bufio.NewScanner(f)
	// Increase buffer for large lines
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, len(buf))

	var currentOffset int64 = startOffset
	goalFound := startOffset > 0 // if we have a cursor, we already captured the goal
	calls := map[string]toolCall{}
	responses := map[string]int{}

	for scanner.Scan() {
		line := scanner.Bytes()
		currentOffset += int64(len(line)) + 1 // +1 for newline

		var entry transcriptEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue // skip malformed lines
		}

		if data.SessionID == "" && entry.SessionID != "" {
			data.SessionID = entry.SessionID
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
				result := toolResult{CallID: block.ToolUseID, IsError: block.IsError, Timestamp: ts}
				if call, ok := calls[block.ToolUseID]; ok {
					result.Name = call.Name
					result.Duration = ts.Sub(call.Timestamp)
				}
				if block.IsError {
					result.Error = toolResultText(block.Content)
				}
				data.ToolResults = append(data.ToolResults, result)
			}
			if entry.IsMeta || entry.PromptID == "" || hasResults {
				continue
			}
			text := extractPrompt(entry.Message)
			if text == "" {
				continue
			}
			data.Prompts = append(data.Prompts, prompt{ID: entry.UUID, Text: text, Timestamp: ts})
			if !goalFound {
				data.Goal = goalFromText(text)
				data.StartedAt = ts
				goalFound = true
			}

		case "assistant":
			var msg assistantMessage
			if err := json.Unmarshal(entry.Message, &msg); err != nil {
				continue
			}

			for _, block := range msg.Content {
				if block.Type == "tool_use" {
					call := toolCall{ID: block.ID, Name: block.Name, Input: block.Input, Timestamp: ts}
					calls[block.ID] = call
					data.ToolCalls = append(data.ToolCalls, call)
				}
			}

			// A response is streamed as several entries with the same message
			// ID. The usage of the latest entry is the most complete.
			if msg.ID != "" {
				response := llmResponse{ID: msg.ID, Model: msg.Model, Usage: msg.Usage, Timestamp: ts}
				if i, ok := responses[msg.ID]; ok {
					response.Timestamp = data.Responses[i].Timestamp
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
	}

	if err := scanner.Err(); err != nil {
		return nil, currentOffset, fmt.Errorf("error reading transcript: %w", err)
	}

	return data, currentOffset, nil
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
	cp, err := cursorPath(sessionID)
	if err != nil {
		return "", err
	}
	return cp + ".meta", nil
}

func writeSessionMeta(sessionID string, meta sessionMeta) error {
	path, err := metaPath(sessionID)
	if err != nil {
		return err
	}
	data, err := json.Marshal(meta) //nolint:forbidigo // writing to disk, not stdout
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("could not create meta directory: %w", err)
	}
	return os.WriteFile(path, data, 0o600)
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
	var meta sessionMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// listSessionMetas returns the IDs of sessions that have metadata.
func listSessionMetas() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(config.ConfigDir(), "gaig", "cursors"))
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

// forgetSession removes the cursor and metadata of a session.
func forgetSession(sessionID string) error {
	cp, err := cursorPath(sessionID)
	if err != nil {
		return err
	}
	var errs []error
	for _, path := range []string{cp, cp + ".meta"} {
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
	meta, err := readSessionMeta(sessionID)
	if errors.Is(err, errNoSessionMeta) {
		return nil
	}
	if err != nil {
		return err
	}
	meta.CompletedAt = &at
	return writeSessionMeta(sessionID, *meta)
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
