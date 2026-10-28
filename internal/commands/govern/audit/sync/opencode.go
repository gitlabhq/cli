package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/opencodeplugin"
	"gitlab.com/gitlab-org/cli/internal/dbg"
)

// openCode syncs OpenCode sessions. Their transcript locator is the path of
// the opencode binary, because the scheduled job runs with a minimal PATH
// that usually does not include it.
type openCode struct {
	executor cmdutils.Executor
}

func (openCode) name() string { return "opencode" }

func (openCode) sessionID() string { return os.Getenv("OPENCODE_SESSION_ID") }

func (o openCode) current(sessionID string) (transcript, string, error) {
	binary, err := o.executor.LookPath("opencode")
	if err != nil {
		return nil, "", fmt.Errorf("could not find opencode: %w", err)
	}
	return &openCodeTranscript{sessionID: sessionID, binary: binary, executor: o.executor}, binary, nil
}

func (o openCode) recorded(sessionID, binary string) (transcript, string, error) {
	use := binary
	if _, err := os.Stat(binary); err != nil {
		// Fall back to PATH, for example after opencode moved on upgrade.
		use = ""
	}
	// The recorded locator is kept, so that rewriting the metadata does not
	// reset how long ago a hook last recorded the session.
	return &openCodeTranscript{sessionID: sessionID, binary: use, executor: o.executor}, binary, nil
}

// canForget allows forgetting a completed OpenCode session only a fixed time
// after completion. Checking whether the session still exists would need a
// full opencode export, and its "Session not found" may mean the job sees a
// different OpenCode data directory.
func (openCode) canForget(_ transcript, completedAt time.Time) (bool, error) {
	return time.Since(completedAt) > forgetUnfoundAfter, nil
}

func (openCode) discover(context.Context, func(string) bool) ([]discoveredSession, error) {
	return nil, nil
}

func (openCode) pausedReason() (string, error) {
	return openCodePausedReason(runtime.GOOS)
}

func openCodePausedReason(goos string) (string, error) {
	if goos == "windows" {
		return "the OpenCode plugin is not supported on Windows", nil
	}
	path, err := opencodeplugin.Path()
	if err != nil {
		return "", err
	}
	state, err := opencodeplugin.Check(path)
	if err != nil {
		return "", err
	}
	if state == opencodeplugin.NotInstalled || state == opencodeplugin.Foreign {
		return "the glab OpenCode plugin is not installed in " + opencodeplugin.Display(path), nil
	}
	return "", nil
}

// openCodeTranscript reads an OpenCode session through its export command
// rather than OpenCode's storage directly. The storage moved from JSON files
// to SQLite in OpenCode 1.2 and its schema keeps changing, while the export
// format is the documented interface.
//
// Its cursor counts the tool calls already synced, because the export has no
// byte offsets.
type openCodeTranscript struct {
	sessionID string
	binary    string
	executor  cmdutils.Executor
	// timeout overrides openCodeExportTimeout in tests.
	timeout time.Duration

	raw    []byte
	export *openCodeExport
}

// openCodeExport holds what glab reads from either export format. OpenCode 1
// keeps each message's role and parts under info and parts. OpenCode 2 gives
// each message a type, with the user's text or the assistant's content.
type openCodeExport struct {
	Info struct {
		Time    openCodeTime `json:"time"`
		Version string       `json:"version"`
	} `json:"info"`
	Messages []openCodeMessage `json:"messages"`
}

type openCodeMessage struct {
	Info struct {
		Role string       `json:"role"`
		Time openCodeTime `json:"time"`
	} `json:"info"`
	Parts []openCodePart `json:"parts"`

	Type string       `json:"type"`
	Time openCodeTime `json:"time"`
	Text string       `json:"text"`
	// Content is decoded one block at a time in tools, so that a block in a
	// shape glab does not know does not fail the whole export.
	Content []json.RawMessage `json:"content"`
}

// openCodeTime holds Unix timestamps in milliseconds.
type openCodeTime struct {
	Created int64 `json:"created"`
	Updated int64 `json:"updated"`
}

type openCodePart struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Text      string `json:"text"`
	Synthetic bool   `json:"synthetic"`
	CallID    string `json:"callID"`
	Tool      string `json:"tool"`
	State     struct {
		Status string          `json:"status"`
		Input  json.RawMessage `json:"input"`
		Error  string          `json:"error"`
		Time   struct {
			Start int64 `json:"start"`
			End   int64 `json:"end"`
		} `json:"time"`
	} `json:"state"`
}

// openCodeContent is an OpenCode 2 assistant content block.
type openCodeContent struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Name  string `json:"name"`
	State struct {
		Status string          `json:"status"`
		Input  json.RawMessage `json:"input"`
		Error  json.RawMessage `json:"error"`
	} `json:"state"`
	Time struct {
		Created   int64 `json:"created"`
		Ran       int64 `json:"ran"`
		Completed int64 `json:"completed"`
	} `json:"time"`
}

// errorText returns a tool error's message, or its type when it has none.
func (c openCodeContent) errorText() string {
	var structured struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	if json.Unmarshal(c.State.Error, &structured) == nil {
		if structured.Message != "" {
			return structured.Message
		}
		return structured.Type
	}
	var text string
	_ = json.Unmarshal(c.State.Error, &text)
	return text
}

// openCodeTool is a tool call from either export format, with times in Unix
// milliseconds. start and end are zero until the call has started or ended.
type openCodeTool struct {
	id, name string
	input    json.RawMessage
	status   string
	err      string
	created  int64
	start    int64
	end      int64
}

func (e *openCodeExport) goal() string {
	for _, msg := range e.Messages {
		if msg.Type == "user" {
			if goal := goalFromText(msg.Text); goal != "" {
				return goal
			}
		}
		if msg.Info.Role != "user" {
			continue
		}
		for _, part := range msg.Parts {
			if part.Type != "text" || part.Synthetic {
				continue
			}
			if goal := goalFromText(part.Text); goal != "" {
				return goal
			}
		}
	}
	return ""
}

func (e *openCodeExport) tools() []openCodeTool {
	var tools []openCodeTool
	for _, msg := range e.Messages {
		for _, part := range msg.Parts {
			if part.Type != "tool" {
				continue
			}
			id := part.CallID
			if id == "" {
				id = part.ID
			}
			tools = append(tools, openCodeTool{
				id:      id,
				name:    part.Tool,
				input:   part.State.Input,
				status:  part.State.Status,
				err:     part.State.Error,
				created: msg.Info.Time.Created,
				start:   part.State.Time.Start,
				end:     part.State.Time.End,
			})
		}
		for _, raw := range msg.Content {
			var block openCodeContent
			if err := json.Unmarshal(raw, &block); err != nil || block.Type != "tool" {
				continue
			}
			input := block.State.Input
			// A streaming call's input is a string of partial JSON, and the
			// call is not synced again once it has been counted.
			if block.State.Status == "streaming" {
				input = nil
			}
			tools = append(tools, openCodeTool{
				id:      block.ID,
				name:    block.Name,
				input:   input,
				status:  block.State.Status,
				err:     block.errorText(),
				created: block.Time.Created,
				start:   block.Time.Ran,
				end:     block.Time.Completed,
			})
		}
	}
	return tools
}

func (t *openCodeTranscript) String() string {
	return "OpenCode session " + t.sessionID
}

// openCodeExportCommands are the commands that print a session, newest
// OpenCode first. OpenCode 2 moved export under session, and --standalone
// reads it without OpenCode's background service, which the scheduled job
// would otherwise start.
var openCodeExportCommands = [][]string{{"session", "export", "--standalone"}, {"export"}}

var errOpenCodeExportTimedOut = errors.New("timed out")

func (t *openCodeTranscript) load(ctx context.Context) error {
	if t.export != nil {
		return nil
	}

	binary := t.binary
	if binary == "" {
		path, err := t.executor.LookPath("opencode")
		if err != nil {
			return fmt.Errorf("could not find opencode: %w", err)
		}
		binary = path
	}

	var failures []error
	for _, command := range openCodeExportCommands {
		if err := ctx.Err(); err != nil {
			return err
		}
		args := append(slices.Clone(command), t.sessionID)
		stdout, stderr, err := t.runExport(ctx, binary, args)
		switch {
		// opencode reports a deleted session only through this message. It is
		// not proof that the session was deleted: the job may see a different
		// OpenCode data directory from the agent.
		case err != nil && strings.Contains(string(stderr), "Session not found"):
			return fmt.Errorf("opencode %s: %w", strings.Join(args, " "), errAgentSessionNotFound)
		case errors.Is(err, errOpenCodeExportTimedOut):
			return fmt.Errorf("opencode %s: %w", strings.Join(args, " "), err)
		case err != nil:
			// OpenCode 1 answers session export with its whole help text.
			reason, _, _ := strings.Cut(strings.TrimSpace(string(stderr)), "\n")
			failures = append(failures, fmt.Errorf("opencode %s: %w: %s", strings.Join(args, " "), err, reason))
			continue
		}

		var export openCodeExport
		if err := json.Unmarshal(stdout, &export); err != nil {
			return fmt.Errorf("could not parse opencode export: %w", err)
		}
		t.raw = stdout
		t.export = &export
		return nil
	}
	return errors.Join(failures...)
}

// openCodeExportTimeout bounds one opencode export. launchd and systemd do not
// start a run while the previous one is still going, so a hung export would
// otherwise stop all later syncing.
const openCodeExportTimeout = 2 * time.Minute

// runExport runs an opencode export command with its output in temporary
// files rather than buffers. With a buffer, exec copies the output through a
// pipe and waits for the pipe to close, so a child process of opencode that
// inherits it keeps the wait blocked even after opencode is killed.
func (t *openCodeTranscript) runExport(ctx context.Context, binary string, args []string) ([]byte, []byte, error) {
	timeout := t.timeout
	if timeout == 0 {
		timeout = openCodeExportTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dir, err := os.MkdirTemp("", "glab-opencode-export-*")
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			dbg.Debugf("could not remove %s: %v", dir, err)
		}
	}()
	outPath, errPath := filepath.Join(dir, "stdout"), filepath.Join(dir, "stderr")

	runErr := t.exportToFiles(ctx, binary, args, outPath, errPath)
	if runErr != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		runErr = fmt.Errorf("%w after %s", errOpenCodeExportTimedOut, timeout)
	}

	stdout, err := os.ReadFile(outPath)
	if err != nil {
		return nil, nil, err
	}
	stderr, err := os.ReadFile(errPath)
	if err != nil {
		return nil, nil, err
	}
	return stdout, stderr, runErr
}

func (t *openCodeTranscript) exportToFiles(ctx context.Context, binary string, args []string, outPath, errPath string) (err error) {
	outFile, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, outFile.Close()) }()
	errFile, err := os.Create(errPath)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, errFile.Close()) }()

	return t.executor.ExecWithIO(ctx, binary, args, nil, nil, outFile, errFile)
}

func (t *openCodeTranscript) read(ctx context.Context, cursor int64) (*sessionData, int64, error) {
	if err := t.load(ctx); err != nil {
		return nil, cursor, err
	}

	data := &sessionData{
		SessionID:    t.sessionID,
		StartedAt:    time.UnixMilli(t.export.Info.Time.Created),
		AgentVersion: t.export.Info.Version,
	}
	if cursor == 0 {
		data.Goal = t.export.goal()
	}
	tools := t.export.tools()
	for _, tool := range tools[min(cursor, int64(len(tools))):] {
		// Pending tool calls have no start time yet.
		started := tool.start
		if started == 0 {
			started = tool.created
		}
		data.ToolCalls = append(data.ToolCalls, toolCall{
			ID:        tool.id,
			Name:      tool.name,
			Input:     tool.input,
			Timestamp: time.UnixMilli(started),
		})
		// The cursor counts tool calls, so a result is only recorded when the
		// call has finished by the time it is first synced, which is usual as
		// syncs follow each turn.
		if tool.status != "completed" && tool.status != "error" {
			continue
		}
		result := toolResult{
			CallID:    tool.id,
			Name:      tool.name,
			IsError:   tool.status == "error",
			Error:     tool.err,
			Timestamp: time.UnixMilli(tool.end),
		}
		if tool.end > 0 && tool.start > 0 {
			result.Duration = time.Duration(tool.end-tool.start) * time.Millisecond
		} else {
			result.Timestamp = time.UnixMilli(started)
		}
		data.ToolResults = append(data.ToolResults, result)
	}
	return data, max(int64(len(tools)), cursor), nil
}

func (t *openCodeTranscript) lastActivity(ctx context.Context) (time.Time, error) {
	if err := t.load(ctx); err != nil {
		return time.Time{}, err
	}
	last := t.export.Info.Time.Updated
	for _, msg := range t.export.Messages {
		last = max(last, msg.Info.Time.Created, msg.Time.Created)
	}
	if last == 0 {
		// Without any timestamp the session must not look idle, or --all
		// would mark a live session completed.
		return time.Now(), nil
	}
	return time.UnixMilli(last), nil
}

func (t *openCodeTranscript) checksum(ctx context.Context) (string, error) {
	if err := t.load(ctx); err != nil {
		return "", err
	}
	sum := sha256.Sum256(t.raw)
	return hex.EncodeToString(sum[:]), nil
}
