package gaig

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// CursorTranscript is the main transcript of a Cursor agent session.
type CursorTranscript struct {
	ID   string
	Path string
	// Workspace is the name Cursor gave the workspace's folder under
	// ~/.cursor/projects.
	Workspace string
	Size      int64
	ModTime   time.Time
}

// CursorTranscripts returns the transcripts at
// ~/.cursor/projects/<workspace>/agent-transcripts/<id>/<id>.jsonl, grouped by
// workspace, following symbolic links. Subagent transcripts, under
// <id>/subagents, are left out. It also returns an error for each folder or
// transcript it could not read, with the transcripts it could.
func CursorTranscripts() ([]CursorTranscript, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	root := filepath.Join(home, ".cursor", "projects")
	projects, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var found []CursorTranscript
	var errs []error
	for _, project := range projects {
		if ok, err := IsDir(filepath.Join(root, project.Name())); err != nil || !ok {
			errs = append(errs, err)
			continue
		}
		dir := filepath.Join(root, project.Name(), "agent-transcripts")
		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, entry := range entries {
			id := entry.Name()
			if ok, err := IsDir(filepath.Join(dir, id)); err != nil || !ok {
				errs = append(errs, err)
				continue
			}
			path := filepath.Join(dir, id, id+".jsonl")
			info, err := os.Stat(path)
			switch {
			case errors.Is(err, fs.ErrNotExist):
				continue
			case err != nil:
				errs = append(errs, err)
				continue
			}
			found = append(found, CursorTranscript{ID: id, Path: path, Workspace: project.Name(), Size: info.Size(), ModTime: info.ModTime()})
		}
	}
	if err := errors.Join(errs...); err != nil {
		return found, fmt.Errorf("could not read all Cursor transcripts: %w", err)
	}
	return found, nil
}

// IsDir reports whether path is a directory or a symbolic link to one, such as
// /var and /tmp on macOS. A broken link is not an error.
func IsDir(path string) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}
