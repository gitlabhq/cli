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
	"strings"
	"time"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
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

// pausedReason never pauses OpenCode, because glab installs nothing in
// OpenCode that the user could remove to pause syncing.
func (openCode) pausedReason() (string, error) {
	return "", nil
}

// openCodeTranscript reads an OpenCode session through `opencode export`
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

type openCodeExport struct {
	Info struct {
		Time openCodeTime `json:"time"`
	} `json:"info"`
	Messages []struct {
		Info struct {
			Role string       `json:"role"`
			Time openCodeTime `json:"time"`
		} `json:"info"`
		Parts []openCodePart `json:"parts"`
	} `json:"messages"`
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
		Time struct {
			Start int64 `json:"start"`
		} `json:"time"`
	} `json:"state"`
}

func (t *openCodeTranscript) String() string {
	return "OpenCode session " + t.sessionID
}

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

	stdout, stderr, err := t.runExport(ctx, binary)
	if err != nil {
		// opencode reports a deleted session only through this message. It is
		// not proof that the session was deleted: the job may see a different
		// OpenCode data directory from the agent.
		if strings.Contains(string(stderr), "Session not found") {
			return fmt.Errorf("opencode export %s: %w", t.sessionID, errAgentSessionNotFound)
		}
		return fmt.Errorf("opencode export %s: %w: %s", t.sessionID, err, strings.TrimSpace(string(stderr)))
	}

	var export openCodeExport
	if err := json.Unmarshal(stdout, &export); err != nil {
		return fmt.Errorf("could not parse opencode export: %w", err)
	}
	t.raw = stdout
	t.export = &export
	return nil
}

// openCodeExportTimeout bounds one opencode export. launchd and systemd do not
// start a run while the previous one is still going, so a hung export would
// otherwise stop all later syncing.
const openCodeExportTimeout = 2 * time.Minute

// runExport runs opencode export with its output in temporary files rather
// than buffers. With a buffer, exec copies the output through a pipe and
// waits for the pipe to close, so a child process of opencode that inherits
// it keeps the wait blocked even after opencode is killed.
func (t *openCodeTranscript) runExport(ctx context.Context, binary string) ([]byte, []byte, error) {
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

	runErr := t.exportToFiles(ctx, binary, outPath, errPath)
	if runErr != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		runErr = fmt.Errorf("timed out after %s", timeout)
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

func (t *openCodeTranscript) exportToFiles(ctx context.Context, binary, outPath, errPath string) (err error) {
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

	return t.executor.ExecWithIO(ctx, binary, []string{"export", t.sessionID}, nil, nil, outFile, errFile)
}

func (t *openCodeTranscript) read(ctx context.Context, cursor int64) (*sessionData, int64, error) {
	if err := t.load(ctx); err != nil {
		return nil, cursor, err
	}

	data := &sessionData{
		SessionID: t.sessionID,
		StartedAt: time.UnixMilli(t.export.Info.Time.Created),
	}
	var seen int64
	for _, msg := range t.export.Messages {
		for _, part := range msg.Parts {
			switch part.Type {
			case "text":
				if cursor == 0 && data.Goal == "" && msg.Info.Role == "user" && !part.Synthetic {
					data.Goal = goalFromText(part.Text)
				}
			case "tool":
				if seen >= cursor {
					// Pending tool calls have no start time yet.
					started := part.State.Time.Start
					if started == 0 {
						started = msg.Info.Time.Created
					}
					id := part.CallID
					if id == "" {
						id = part.ID
					}
					data.ToolCalls = append(data.ToolCalls, toolCall{
						ID:        id,
						Name:      part.Tool,
						Timestamp: time.UnixMilli(started),
					})
				}
				seen++
			}
		}
	}
	return data, max(seen, cursor), nil
}

func (t *openCodeTranscript) lastActivity(ctx context.Context) (time.Time, error) {
	if err := t.load(ctx); err != nil {
		return time.Time{}, err
	}
	last := t.export.Info.Time.Updated
	for _, msg := range t.export.Messages {
		last = max(last, msg.Info.Time.Created)
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
