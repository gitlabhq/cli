// Package claudehooks provides shared constants and utilities for managing
// Claude Code lifecycle hooks.
package claudehooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	// SettingsFile is the path to the Claude Code settings file relative to the home directory.
	SettingsFile = ".claude/settings.json"

	// StopHookCommand fires after every Claude Code turn.
	StopHookCommand = "glab govern audit sync --silent >/dev/null 2>&1 &"

	// SessionEndHookCommand fires when a Claude Code session ends.
	SessionEndHookCommand = "glab govern audit sync --silent --complete >/dev/null 2>&1 &"
)

// HookEntry mirrors the Claude Code hook entry format.
type HookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// HookGroup mirrors the Claude Code hook group format.
type HookGroup struct {
	// Matcher is optional and scopes the hook to specific tools.
	// It must be preserved to avoid widening hook scope.
	Matcher string      `json:"matcher,omitempty"`
	Hooks   []HookEntry `json:"hooks"`
}

// SettingsPath returns the absolute path to the Claude Code settings file.
func SettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not find home directory: %w", err)
	}
	return filepath.Join(home, SettingsFile), nil
}

// LoadRawSettings reads the Claude Code settings file as a raw JSON map,
// preserving all unknown keys. Returns an empty map if the file does not exist.
func LoadRawSettings(path string) (map[string]json.RawMessage, error) {
	raw := make(map[string]json.RawMessage)
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("could not parse %s: %w", path, err)
		}
	case errors.Is(err, fs.ErrNotExist):
		// File does not exist -- start with an empty map
	default:
		return nil, fmt.Errorf("could not read %s: %w", path, err)
	}
	return raw, nil
}

// HooksFromRaw extracts the hooks map from a raw settings map.
// Returns an empty map if the hooks key is absent or malformed.
func HooksFromRaw(raw map[string]json.RawMessage) (map[string][]HookGroup, error) {
	hooks := make(map[string][]HookGroup)
	if raw["hooks"] == nil {
		return hooks, nil
	}
	if err := json.Unmarshal(raw["hooks"], &hooks); err != nil {
		return nil, fmt.Errorf("could not parse hooks: %w", err)
	}
	return hooks, nil
}

// WriteSettings writes the settings map back to disk atomically using a
// temp file + rename, preserving all keys including unknown ones.
// SetEscapeHTML(false) keeps shell characters like > and & readable.
func WriteSettings(path string, raw map[string]json.RawMessage) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("could not create directory: %w", err)
	}

	buf := &bytes.Buffer{}
	encoder := json.NewEncoder(buf) //nolint:forbidigo // serializing to disk, not stdout
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(raw); err != nil {
		return fmt.Errorf("could not serialise settings: %w", err)
	}

	// Preserve existing file permissions, default to 0644 for new files
	perm := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode()
	}

	// Write atomically via temp file + rename to avoid corruption on crash
	tmp, err := os.CreateTemp(filepath.Dir(path), ".claude-settings-*.json")
	if err != nil {
		return fmt.Errorf("could not create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(buf.Bytes()); err != nil {
		return errors.Join(fmt.Errorf("could not write temp file: %w", err), tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("could not close temp file: %w", err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return fmt.Errorf("could not set file permissions: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("could not rename temp file: %w", err)
	}

	return nil
}

// HookPresent reports whether a hook with the given command exists for the given event
// in the provided hooks map.
func HookPresent(hooks map[string][]HookGroup, event, command string) bool {
	groups, ok := hooks[event]
	if !ok {
		return false
	}
	for _, group := range groups {
		for _, hook := range group.Hooks {
			if hook.Command == command {
				return true
			}
		}
	}
	return false
}

// AddHook adds a hook for the given event if not already present.
// It mutates the hooks map in place and returns whether anything changed.
func AddHook(hooks map[string][]HookGroup, event, command string) bool {
	if HookPresent(hooks, event, command) {
		return false
	}
	hooks[event] = append(hooks[event], HookGroup{
		Hooks: []HookEntry{{Type: "command", Command: command}},
	})
	return true
}
