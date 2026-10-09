package sync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
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

// deleted never reports an OpenCode session as deleted, because the only
// existence check is a full opencode export, and its "Session not found" may
// mean the job sees a different OpenCode data directory.
func (openCode) deleted(transcript) (bool, error) {
	return false, nil
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

	var stdout, stderr bytes.Buffer
	if err := t.executor.ExecWithIO(ctx, binary, []string{"export", t.sessionID}, nil, nil, &stdout, &stderr); err != nil {
		// opencode reports a deleted session only through this message. It is
		// not proof that the session was deleted: the job may see a different
		// OpenCode data directory from the agent.
		if strings.Contains(stderr.String(), "Session not found") {
			return fmt.Errorf("opencode export %s: %w", t.sessionID, errAgentSessionNotFound)
		}
		return fmt.Errorf("opencode export %s: %w: %s", t.sessionID, err, strings.TrimSpace(stderr.String()))
	}

	var export openCodeExport
	if err := json.Unmarshal(stdout.Bytes(), &export); err != nil {
		return fmt.Errorf("could not parse opencode export: %w", err)
	}
	t.raw = stdout.Bytes()
	t.export = &export
	return nil
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
