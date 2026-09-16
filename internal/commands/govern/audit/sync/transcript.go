package sync

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

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
	Content []contentBlock `json:"content"`
}

// contentBlock represents a single content block in an assistant message.
type contentBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
	Text  string          `json:"text"`
}

// sessionData holds extracted data from the transcript for a single session.
type sessionData struct {
	SessionID  string
	Goal       string
	StartedAt  time.Time
	ToolCalls  []toolCall
	EndedAt    time.Time
	IsComplete bool
}

// toolCall represents a single tool invocation extracted from the transcript.
type toolCall struct {
	ID        string
	Name      string
	Timestamp time.Time
}

// extractGoal attempts to extract a human-readable goal from a user message.
// Returns empty string if the message is a bash input, tool result, or other
// non-goal content.
func extractGoal(raw json.RawMessage) string {
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
				// Skip bash inputs and XML-tagged content
				if strings.Contains(block.Text, "<bash-input>") ||
					strings.Contains(block.Text, "<bash-output>") ||
					strings.HasPrefix(strings.TrimSpace(block.Text), "<") {
					return ""
				}
				goal := block.Text
				if len(goal) > 256 {
					goal = goal[:256] + "..."
				}
				return goal
			}
		}
	}

	// Try string content format: {"role":"user","content":"<bash-input>..."}
	var msgString struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &msgString); err == nil && msgString.Content != "" {
		content := strings.TrimSpace(msgString.Content)
		// Skip bash inputs and XML-tagged content
		if strings.Contains(content, "<bash-input>") ||
			strings.Contains(content, "<bash-output>") ||
			strings.HasPrefix(content, "<") {
			return ""
		}
		if len(content) > 256 {
			content = content[:256] + "..."
		}
		return content
	}

	return ""
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

		switch entry.Type {
		case "user":
			if !entry.IsMeta && !goalFound && entry.PromptID != "" {
				goal := extractGoal(entry.Message)
				if goal != "" {
					data.Goal = goal
					if ts, err := time.Parse(time.RFC3339Nano, entry.Timestamp); err == nil {
						data.StartedAt = ts
					}
					goalFound = true
				}
				// If goal is empty (bash input etc), keep looking
			}

		case "assistant":
			// Extract tool calls from assistant messages
			var msg assistantMessage
			if err := json.Unmarshal(entry.Message, &msg); err != nil {
				continue
			}
			ts, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
			if err != nil {
				ts = time.Now() // fallback: use current time if timestamp is malformed
			}

			for _, block := range msg.Content {
				if block.Type == "tool_use" {
					data.ToolCalls = append(data.ToolCalls, toolCall{
						ID:        block.ID,
						Name:      block.Name,
						Timestamp: ts,
					})
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
