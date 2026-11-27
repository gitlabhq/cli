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
	"reflect"
	"runtime"
	"slices"
	"strings"
)

const (
	// SettingsFile is the path to the Claude Code settings file relative to
	// the home directory, used when CLAUDE_CONFIG_DIR is not set.
	SettingsFile = ".claude/settings.json"

	// StopHookArgs are the glab arguments of the hook that fires after
	// every Claude Code turn.
	StopHookArgs = "minder audit sync --silent >/dev/null 2>&1 &"

	// SessionEndHookArgs are the glab arguments of the hook that fires when
	// a Claude Code session ends.
	SessionEndHookArgs = "minder audit sync --silent --complete >/dev/null 2>&1 &"

	// StopHookCommand and SessionEndHookCommand are the hooks with a bare
	// glab, as setup writes them. Setup, uninstall and doctor match a hook by
	// its arguments, so that a glab at another path is recognized too.
	StopHookCommand       = "glab " + StopHookArgs
	SessionEndHookCommand = "glab " + SessionEndHookArgs

	// legacyStopHookArgs and legacySessionEndHookArgs are the arguments of
	// the hooks that glab versions before the rename to glab minder
	// installed. They still run, through the hidden glab govern command,
	// until setup replaces them.
	legacyStopHookArgs       = "govern audit sync --silent >/dev/null 2>&1 &"
	legacySessionEndHookArgs = "govern audit sync --silent --complete >/dev/null 2>&1 &"
)

// glabHook is a hook that setup installs: glab with args, for event. The
// hooks that glab versions before the rename installed run glab with
// legacyArgs instead.
type glabHook struct{ event, args, legacyArgs string }

// glabHooks are the hooks that setup installs, in the order it adds them.
var glabHooks = []glabHook{
	{"Stop", StopHookArgs, legacyStopHookArgs},
	{"SessionEnd", SessionEndHookArgs, legacySessionEndHookArgs},
}

// matches reports whether command runs glab, by any path, with the hook's
// arguments or its legacy ones.
func (h glabHook) matches(command string) bool {
	return MatchesGlabCommand(command, h.args) || h.legacy(command)
}

// legacy reports whether command runs glab, by any path, with the hook's
// legacy arguments.
func (h glabHook) legacy(command string) bool {
	return MatchesGlabCommand(command, h.legacyArgs)
}

// MatchesGlabCommand reports whether command runs glab, by any path, with
// args, so that a glab moved by an upgrade is still recognized. Another
// program run with the same arguments does not match.
func MatchesGlabCommand(command, args string) bool {
	program, ok := strings.CutSuffix(command, " "+args)
	if !ok {
		return false
	}
	if len(program) >= 2 && strings.HasPrefix(program, "'") && strings.HasSuffix(program, "'") {
		program = strings.ReplaceAll(program[1:len(program)-1], `'\''`, "'")
	}
	name := program[strings.LastIndexAny(program, `/\`)+1:]
	return name == "glab" || strings.EqualFold(name, "glab.exe")
}

// HookEntry mirrors the Claude Code hook entry format.
type HookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
	// unknown keeps fields glab does not model, such as "if" and
	// "statusMessage". Dropping "if" would widen the hook's scope.
	unknown map[string]json.RawMessage
}

func (e *HookEntry) UnmarshalJSON(data []byte) error {
	type known HookEntry
	var k known
	unknown, err := splitUnknown(data, &k)
	if err != nil {
		return err
	}
	*e = HookEntry(k)
	e.unknown = unknown
	return nil
}

func (e HookEntry) MarshalJSON() ([]byte, error) {
	type known HookEntry
	return joinUnknown(known(e), e.unknown)
}

// HookGroup mirrors the Claude Code hook group format.
type HookGroup struct {
	// Matcher is optional and scopes the hook to specific tools.
	// It must be preserved to avoid widening hook scope.
	Matcher string      `json:"matcher,omitempty"`
	Hooks   []HookEntry `json:"hooks"`
	unknown map[string]json.RawMessage
	// raw is the group as read. An unchanged group is written back as raw,
	// so that another tool's entries, such as EAP's, keep their key order.
	raw json.RawMessage
}

func (g *HookGroup) UnmarshalJSON(data []byte) error {
	type known HookGroup
	var k known
	unknown, err := splitUnknown(data, &k)
	if err != nil {
		return err
	}
	*g = HookGroup(k)
	g.unknown = unknown
	g.raw = slices.Clone(data)
	return nil
}

func (g HookGroup) MarshalJSON() ([]byte, error) {
	type known HookGroup
	encoded, err := joinUnknown(known(g), g.unknown)
	if err != nil || g.raw == nil || !jsonEqual(encoded, g.raw) {
		return encoded, err
	}
	buf := &bytes.Buffer{}
	if json.Compact(buf, g.raw) == nil {
		return buf.Bytes(), nil
	}
	return encoded, nil
}

// jsonEqual reports whether a and b encode the same value.
func jsonEqual(a, b []byte) bool {
	var va, vb any
	if json.Unmarshal(a, &va) != nil || json.Unmarshal(b, &vb) != nil {
		return false
	}
	return reflect.DeepEqual(va, vb)
}

// splitUnknown decodes data into known and returns the fields that known
// does not write back. A known field left at its zero value is omitted when
// encoded, so it is kept here verbatim and written back unchanged.
func splitUnknown(data []byte, known any) (map[string]json.RawMessage, error) {
	if err := json.Unmarshal(data, known); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	encoded, err := marshal(known)
	if err != nil {
		return nil, err
	}
	var written map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &written); err != nil {
		return nil, err
	}
	for key := range written {
		delete(fields, key)
	}
	if len(fields) == 0 {
		return nil, nil
	}
	return fields, nil
}

func joinUnknown(known any, unknown map[string]json.RawMessage) ([]byte, error) {
	encoded, err := marshal(known)
	if err != nil || len(unknown) == 0 {
		return encoded, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return nil, err
	}
	for key, value := range unknown {
		if _, ok := fields[key]; !ok {
			fields[key] = value
		}
	}
	return marshal(fields)
}

// marshal encodes v without HTML escaping, so shell characters such as > and
// & in hook commands stay readable in the settings file.
func marshal(v any) ([]byte, error) {
	buf := &bytes.Buffer{}
	encoder := json.NewEncoder(buf) //nolint:forbidigo // serializing to disk, not stdout
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// MarshalHooks encodes hooks for the "hooks" key of the settings file.
func MarshalHooks(hooks map[string][]HookGroup) (json.RawMessage, error) {
	return marshal(hooks)
}

// ConfigDir returns Claude Code's configuration directory, as Claude Code
// finds it: CLAUDE_CONFIG_DIR when that is set, and otherwise ~/.claude.
func ConfigDir() (string, error) {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not find home directory: %w", err)
	}
	return filepath.Join(home, ".claude"), nil
}

// SettingsPath returns the path to the Claude Code user settings file:
// settings.json in ConfigDir.
func SettingsPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "settings.json"), nil
}

// CheckConfigDir refuses a relative CLAUDE_CONFIG_DIR. Claude Code resolves
// it from each session's directory, so no one settings file matches it.
func CheckConfigDir() error {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" && !filepath.IsAbs(dir) {
		return fmt.Errorf("set CLAUDE_CONFIG_DIR to an absolute path: Claude Code resolves a relative value (%q) from each session's directory", dir)
	}
	return nil
}

// DisplayPath shows path for messages, with the home directory as ~.
func DisplayPath(path string) string {
	return displayPath(runtime.GOOS, path)
}

// displayPath shows a Windows path unchanged, rather than as ~/ followed by
// backslashes.
func displayPath(goos, path string) string {
	if goos == "windows" {
		return path
	}
	if home, err := os.UserHomeDir(); err == nil {
		if rel, ok := strings.CutPrefix(path, filepath.Clean(home)+string(filepath.Separator)); ok {
			return "~/" + rel
		}
	}
	return path
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
	return hookPresentFunc(hooks, event, func(c string) bool { return c == command })
}

// hookPresentFunc reports whether a hook whose command matches exists for
// event.
func hookPresentFunc(hooks map[string][]HookGroup, event string, match func(command string) bool) bool {
	for _, group := range hooks[event] {
		for _, hook := range group.Hooks {
			if match(hook.Command) {
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

// SyncHookInstalled reports whether the glab Stop hook, current or legacy, is
// present in the Claude Code settings file at path.
func SyncHookInstalled(path string) (bool, error) {
	raw, err := LoadRawSettings(path)
	if err != nil {
		return false, err
	}
	hooks, err := HooksFromRaw(raw)
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	return GlabHookPresent(hooks, "Stop"), nil
}

// GlabHookPresent reports whether glab's hook for event, current or legacy,
// is present in hooks, run by any glab path.
func GlabHookPresent(hooks map[string][]HookGroup, event string) bool {
	for _, h := range glabHooks {
		if h.event == event && hookPresentFunc(hooks, event, h.matches) {
			return true
		}
	}
	return false
}

// LegacyHookPresent reports whether hooks still has a hook that an earlier glab
// version installed, which runs glab govern instead of glab minder.
func LegacyHookPresent(hooks map[string][]HookGroup) bool {
	for _, h := range glabHooks {
		if hookPresentFunc(hooks, h.event, h.legacy) {
			return true
		}
	}
	return false
}

// InstallGlabHooks leaves one of each of glab's hooks in hooks, so that a
// session is not synced twice. A single hook that already runs glab, by any
// path, with the hook's arguments is kept; legacy and duplicate ones are
// replaced. It mutates hooks in place and returns whether anything changed.
func InstallGlabHooks(hooks map[string][]HookGroup) bool {
	changed := false
	for _, h := range glabHooks {
		if countHooks(hooks, h.event, h.matches) == 1 && !hookPresentFunc(hooks, h.event, h.legacy) {
			continue
		}
		removeHookFunc(hooks, h.event, h.matches)
		AddHook(hooks, h.event, "glab "+h.args)
		changed = true
	}
	return changed
}

// countHooks returns how many hooks for event match.
func countHooks(hooks map[string][]HookGroup, event string, match func(command string) bool) int {
	n := 0
	for _, group := range hooks[event] {
		for _, hook := range group.Hooks {
			if match(hook.Command) {
				n++
			}
		}
	}
	return n
}

// RemoveGlabHooks removes glab's hooks, current and legacy, run by any glab
// path, from hooks. It mutates hooks in place and returns whether anything
// changed.
func RemoveGlabHooks(hooks map[string][]HookGroup) bool {
	changed := false
	for _, h := range glabHooks {
		changed = removeHookFunc(hooks, h.event, h.matches) || changed
	}
	return changed
}

// RemoveHook removes every hook for event that runs command. Groups left
// without hooks are dropped, and so is the event when no groups remain.
// It mutates hooks in place and returns whether anything changed.
func RemoveHook(hooks map[string][]HookGroup, event, command string) bool {
	return removeHookFunc(hooks, event, func(c string) bool { return c == command })
}

// removeHookFunc is RemoveHook for every hook whose command matches.
func removeHookFunc(hooks map[string][]HookGroup, event string, match func(command string) bool) bool {
	changed := false
	var kept []HookGroup
	for _, group := range hooks[event] {
		entries := slices.DeleteFunc(slices.Clone(group.Hooks), func(e HookEntry) bool {
			return match(e.Command)
		})
		if len(entries) != len(group.Hooks) {
			changed = true
		}
		if len(entries) > 0 {
			group.Hooks = entries
			kept = append(kept, group)
		}
	}
	switch {
	case !changed:
	case len(kept) == 0:
		delete(hooks, event)
	default:
		hooks[event] = kept
	}
	return changed
}
