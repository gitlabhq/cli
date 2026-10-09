package sync

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/fallbacksync"
	"gitlab.com/gitlab-org/cli/internal/dbg"
)

// codex syncs Codex sessions, which the scheduled job finds by scanning
// Codex's rollout files. Their transcript locator is the rollout path.
type codex struct {
	executor cmdutils.Executor
}

func (codex) name() string { return "codex" }

// sessionID reports no session, because Codex sessions are only discovered.
func (codex) sessionID() string { return "" }

func (codex) current(string) (transcript, string, error) {
	return nil, "", errors.New("codex sessions are discovered, not recorded by a hook")
}

func (codex) recorded(sessionID, path string) (transcript, string, error) {
	return codexTranscript{sessionID: sessionID, path: path}, path, nil
}

func (codex) canForget(src transcript, _ time.Time) (bool, error) {
	t, ok := src.(codexTranscript)
	if !ok {
		return false, fmt.Errorf("unexpected Codex transcript type %T", src)
	}
	return fileDeleted(t.path)
}

func (codex) pausedReason() (string, error) {
	return discoveryPausedReason("codex")
}

// codexSessionsDir is where Codex writes rollouts. CODEX_HOME is followed only
// when the scheduled job itself has it, which launchd and systemd jobs
// usually do not.
func codexSessionsDir() (string, error) {
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return filepath.Join(home, "sessions"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex", "sessions"), nil
}

// codexSessionMeta is the payload of the first line of a rollout.
type codexSessionMeta struct {
	ID             string `json:"id"`
	HistoryMode    string `json:"history_mode"`
	Timestamp      string `json:"timestamp"`
	CWD            string `json:"cwd"`
	ParentThreadID string `json:"parent_thread_id"`
	Git            *struct {
		RepositoryURL string `json:"repository_url"`
	} `json:"git"`
}

func (c codex) discover(ctx context.Context, known func(string) bool) ([]discoveredSession, error) {
	root, err := codexSessionsDir()
	if err != nil {
		return nil, err
	}
	var found []discoveredSession
	// Sessions in the same directory share a remote, so git runs once per
	// directory in each run.
	remotes := map[string]string{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == root {
			return filepath.SkipAll
		}
		if err != nil && path == root {
			return err
		}
		if err != nil {
			// One unreadable directory must not stop discovery of the others.
			dbg.Debugf("skipping %s: %v", path, err)
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !strings.HasPrefix(d.Name(), "rollout-") || filepath.Ext(d.Name()) != ".jsonl" {
			return nil
		}
		meta, err := readCodexSessionMeta(path)
		if err != nil {
			// One unreadable rollout must not stop discovery of the others.
			dbg.Debugf("skipping %s: %v", path, err)
			return nil
		}
		if meta.ID == "" || known(meta.ID) {
			return nil
		}
		// Subagent threads are not synced in this version.
		if meta.ParentThreadID != "" {
			return nil
		}
		s := discoveredSession{id: meta.ID, locator: path}
		if meta.Git != nil && meta.Git.RepositoryURL != "" {
			s.remote = meta.Git.RepositoryURL
		} else if meta.CWD != "" {
			remote, ok := remotes[meta.CWD]
			if !ok {
				remote, err = gitRemote(ctx, c.executor, meta.CWD)
				if err != nil {
					dbg.Debugf("codex session %s: %v", meta.ID, err)
				}
				remotes[meta.CWD] = remote
			}
			s.remote = remote
		}
		found = append(found, s)
		return nil
	})
	return found, err
}

func readCodexSessionMeta(path string) (*codexSessionMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	line, err := bufio.NewReaderSize(f, 1024*1024).ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	var first struct {
		Type    string           `json:"type"`
		Payload codexSessionMeta `json:"payload"`
	}
	if err := json.Unmarshal(line, &first); err != nil {
		return nil, err
	}
	if first.Type != "session_meta" {
		return nil, fmt.Errorf("%s does not start with session metadata", path)
	}
	return &first.Payload, nil
}

// codexTranscript is a Codex rollout. Its cursor is a byte offset.
type codexTranscript struct {
	sessionID string
	path      string
}

func (t codexTranscript) String() string { return t.path }

func (t codexTranscript) read(_ context.Context, cursor int64) (*sessionData, int64, error) {
	if _, err := os.Stat(t.path); errors.Is(err, fs.ErrNotExist) {
		return nil, cursor, errTranscriptNotFound
	}
	// The history mode is only on the first line, and decides where prompts
	// are recorded.
	meta, err := readCodexSessionMeta(t.path)
	if err != nil {
		return nil, cursor, err
	}
	r := codexReader{
		sessionID: t.sessionID,
		paginated: meta.HistoryMode == "paginated",
		data:      &sessionData{},
		goalFound: cursor > 0,
		calls:     map[string]toolCall{},
	}
	offset, err := scanJSONL(t.path, cursor, r.parseLine)
	return r.data, offset, err
}

func (t codexTranscript) lastActivity(context.Context) (time.Time, error) {
	return fileModTime(t.path)
}

func (t codexTranscript) checksum(context.Context) (string, error) {
	return sha256File(t.path)
}

// codexToolNames names the response items that are tool calls but carry no
// name of their own.
var codexToolNames = map[string]string{
	"local_shell_call": "shell",
	"web_search_call":  "web_search",
	"tool_search_call": "tool_search",
}

// codexReader collects session data from rollout lines.
type codexReader struct {
	sessionID string
	// paginated rollouts do not record user_message events, so prompts come
	// from user message items instead.
	paginated bool
	data      *sessionData
	goalFound bool
	calls     map[string]toolCall
	model     string
}

type codexLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		Type      string          `json:"type"`
		ID        string          `json:"id"`
		CallID    string          `json:"call_id"`
		Name      string          `json:"name"`
		Role      string          `json:"role"`
		Message   string          `json:"message"`
		Model     string          `json:"model"`
		Arguments json.RawMessage `json:"arguments"`
		Input     string          `json:"input"`
		Action    json.RawMessage `json:"action"`
		Content   []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Info *struct {
			LastTokenUsage struct {
				InputTokens       int `json:"input_tokens"`
				CachedInputTokens int `json:"cached_input_tokens"`
				OutputTokens      int `json:"output_tokens"`
			} `json:"last_token_usage"`
		} `json:"info"`
	} `json:"payload"`
}

func (r *codexReader) parseLine(line []byte, lineOffset int64) {
	var entry codexLine
	if json.Unmarshal(line, &entry) != nil {
		return
	}
	ts, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
	if err != nil {
		ts = time.Now()
	}
	p := entry.Payload
	lineID := fmt.Sprintf("codex:%s:%d", r.sessionID, lineOffset)

	switch entry.Type {
	case "session_meta":
		r.data.StartedAt = ts
	case "turn_context":
		r.model = p.Model
	case "event_msg":
		switch p.Type {
		case "user_message":
			// user_message events hold the prompt as typed, unlike user
			// message items, which also carry injected instructions.
			r.addPrompt(lineID, strings.TrimSpace(p.Message), ts)
		case "token_count":
			if p.Info == nil {
				return
			}
			u := p.Info.LastTokenUsage
			r.data.Responses = append(r.data.Responses, llmResponse{
				ID:        lineID,
				Model:     r.model,
				Usage:     tokenUsage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CacheReadInputTokens: u.CachedInputTokens},
				Timestamp: ts,
			})
		}
	case "response_item":
		r.parseResponseItem(p.Type, entry, lineID, ts)
	}
}

func (r *codexReader) parseResponseItem(itemType string, entry codexLine, lineID string, ts time.Time) {
	p := entry.Payload
	switch itemType {
	case "message":
		if !r.paginated || p.Role != "user" {
			return
		}
		for _, c := range p.Content {
			// Injected context is XML-tagged or the AGENTS.md instructions.
			if c.Type == "input_text" && !strings.HasPrefix(c.Text, "# AGENTS.md") {
				r.addPrompt(lineID, strings.TrimSpace(c.Text), ts)
				return
			}
		}
	case "function_call", "custom_tool_call", "local_shell_call", "web_search_call", "tool_search_call":
		name := p.Name
		if fixed, ok := codexToolNames[itemType]; ok {
			name = fixed
		}
		id := p.CallID
		if id == "" {
			id = p.ID
		}
		if id == "" {
			return
		}
		call := toolCall{ID: id, Name: name, Input: codexArguments(p.Arguments, p.Input, p.Action), Timestamp: ts}
		r.calls[id] = call
		r.data.ToolCalls = append(r.data.ToolCalls, call)
	case "function_call_output", "custom_tool_call_output":
		// Codex does not record whether a call succeeded.
		result := toolResult{CallID: p.CallID, Outcome: "completed", Timestamp: ts}
		if call, ok := r.calls[p.CallID]; ok {
			result.Name = call.Name
			result.Duration = ts.Sub(call.Timestamp)
		}
		r.data.ToolResults = append(r.data.ToolResults, result)
	}
}

func (r *codexReader) addPrompt(id, text string, ts time.Time) {
	if text == "" || skipPrompt(text) {
		return
	}
	r.data.Prompts = append(r.data.Prompts, prompt{ID: id, Text: text, Timestamp: ts})
	if !r.goalFound {
		r.data.Goal = goalFromText(text)
		r.goalFound = true
	}
}

// codexArguments returns a tool call's arguments as JSON: function call
// arguments are a string that holds JSON, tool search arguments are a JSON
// value, custom tool input is free text, and shell and web search calls carry
// an action object.
func codexArguments(arguments json.RawMessage, input string, action json.RawMessage) json.RawMessage {
	var text string
	isString := json.Unmarshal(arguments, &text) == nil
	switch {
	case isString && text != "" && json.Valid([]byte(text)):
		return json.RawMessage(text)
	case isString && text != "":
		return marshalString(text)
	case !isString && len(arguments) > 0 && string(arguments) != "null":
		return arguments
	case input != "":
		encoded, err := json.Marshal(map[string]string{"input": input}) //nolint:forbidigo // not output
		if err != nil {
			return nil
		}
		return encoded
	default:
		return action
	}
}

func marshalString(s string) json.RawMessage {
	encoded, err := json.Marshal(s) //nolint:forbidigo // not output
	if err != nil {
		return nil
	}
	return encoded
}

// discoveryPausedReason pauses an agent that setup no longer enables.
func discoveryPausedReason(agent string) (string, error) {
	enabled, err := fallbacksync.DiscoveredAgents()
	if err != nil {
		return "", err
	}
	if slices.Contains(enabled, agent) {
		return "", nil
	}
	return fmt.Sprintf("%s is not enabled; run 'glab govern setup --agents %s' to enable it", agent, agent), nil
}
