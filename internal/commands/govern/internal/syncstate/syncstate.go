// Package syncstate records how far glab has synced each agent session: its
// sync position, which is a byte offset or a count of entries, depending on
// the agent.
package syncstate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/gaig"
)

// PositionPath returns the path of the file that holds a session's sync
// position. The files are kept in the cursors directory, as they always were.
func PositionPath(sessionID string) (string, error) {
	if strings.ContainsAny(sessionID, "/\\..") {
		return "", fmt.Errorf("invalid session ID: %q", sessionID)
	}
	return filepath.Join(gaig.StateDir(), "cursors", sessionID), nil
}

// ReadPosition returns the position a session was last synced to, or 0 if
// it has none.
func ReadPosition(sessionID string) (int64, error) {
	path, err := PositionPath(sessionID)
	if err != nil {
		return 0, err
	}
	position, err := readOffset(path)
	if err != nil {
		return 0, fmt.Errorf("could not read the sync position: %w", err)
	}
	return position, nil
}

// WritePosition saves the position a session was synced to. It does not move
// the position back: a hook and the fallback sync can work on the same
// session at once, and the one that finishes last may have read less.
func WritePosition(sessionID string, position int64) error {
	path, err := PositionPath(sessionID)
	if err != nil {
		return err
	}

	if current, err := ReadPosition(sessionID); err == nil && current > position {
		return nil
	}
	if err := writeOffset(path, position); err != nil {
		return fmt.Errorf("could not write the sync position: %w", err)
	}
	return nil
}

// StartPath returns the path of the file that records the position a
// session's syncing started from, when it did not start at the beginning of
// the transcript.
func StartPath(sessionID string) (string, error) {
	path, err := PositionPath(sessionID)
	if err != nil {
		return "", err
	}
	return path + ".start", nil
}

// HasUploads reports whether glab has uploaded any of the session, which it
// has once the sync position has moved past where syncing started.
func HasUploads(sessionID string, position int64) (bool, error) {
	path, err := StartPath(sessionID)
	if err != nil {
		return false, err
	}
	start, err := readOffset(path)
	if err != nil {
		return false, fmt.Errorf("could not read where syncing started: %w", err)
	}
	return position > start, nil
}

// StartAt moves the session's sync position to offset, and makes its syncing
// start there if glab has uploaded none of it yet.
func StartAt(sessionID string, offset int64) error {
	position, err := ReadPosition(sessionID)
	if err != nil {
		return err
	}
	uploaded, err := HasUploads(sessionID, position)
	if err != nil {
		return err
	}
	offset = max(offset, position)
	if !uploaded {
		start, err := StartPath(sessionID)
		if err != nil {
			return err
		}
		// Written before the position, so that the position never passes
		// the start without anything uploaded.
		if err := writeOffset(start, offset); err != nil {
			return fmt.Errorf("could not record where syncing starts: %w", err)
		}
	}
	return WritePosition(sessionID, offset)
}

func readOffset(path string) (int64, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	offset, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("could not parse %s: %w", filepath.Base(path), err)
	}
	return offset, nil
}

func writeOffset(path string, offset int64) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return gaig.WriteFileAtomic(path, []byte(strconv.FormatInt(offset, 10)))
}
