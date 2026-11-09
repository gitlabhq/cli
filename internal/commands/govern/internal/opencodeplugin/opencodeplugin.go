// Package opencodeplugin manages the OpenCode plugin that glab installs to
// sync OpenCode sessions.
package opencodeplugin

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// FileName is the plugin's file name in the OpenCode plugin directory.
const FileName = "glab-govern.js"

// Source is the plugin glab installs. OpenCode sets no environment variable
// with the session ID, so the plugin passes it to glab as
// OPENCODE_SESSION_ID.
//
//go:embed glab-govern.js
var Source []byte

// markers start every version of the plugin, so that glab only removes a file
// it installed. Versions before the rename to glab minder start with the
// second.
var markers = [][]byte{
	[]byte("// Installed by glab minder setup."),
	[]byte("// Installed by glab govern setup."),
}

func hasMarker(data []byte) bool {
	return slices.ContainsFunc(markers, func(m []byte) bool { return bytes.HasPrefix(data, m) })
}

// ConfigDir returns OpenCode's global configuration directory, which OpenCode
// creates when it first runs.
func ConfigDir() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "opencode"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not find home directory: %w", err)
	}
	return filepath.Join(home, ".config", "opencode"), nil
}

// Path returns where the plugin is installed. OpenCode loads every plugin in
// this directory, for all projects.
func Path() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "plugins", FileName), nil
}

// Display shortens path for messages, writing the home directory as ~.
func Display(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if rel, err := filepath.Rel(home, path); err == nil && filepath.IsLocal(rel) {
		return filepath.Join("~", rel)
	}
	return path
}

// Detected reports whether OpenCode has run for this user.
func Detected() (bool, error) {
	dir, err := ConfigDir()
	if err != nil {
		return false, err
	}
	switch _, err := os.Stat(dir); {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, err
	}
}

// State is what is installed at the plugin path.
type State int

const (
	// NotInstalled means there is no file at the plugin path.
	NotInstalled State = iota
	// Current means the plugin is this glab's version.
	Current
	// Outdated means glab installed a different version of the plugin.
	Outdated
	// Foreign means the file at the plugin path was not installed by glab.
	Foreign
)

// Check reports what is installed at path.
func Check(path string) (State, error) {
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return NotInstalled, nil
	case err != nil:
		return NotInstalled, err
	case bytes.Equal(data, Source):
		return Current, nil
	case hasMarker(data):
		return Outdated, nil
	default:
		return Foreign, nil
	}
}

// ForeignError reports that the file at path, where the plugin goes, was not
// installed by glab.
func ForeignError(path string) error {
	return fmt.Errorf("%s was not installed by glab; remove or rename it, then run 'glab minder setup --agents opencode'", Display(path))
}

// Install writes the plugin to path, replacing an earlier version. It
// returns whether anything changed, and refuses to replace a file that glab
// did not install.
func Install(path string) (bool, error) {
	state, err := Check(path)
	switch {
	case err != nil:
		return false, err
	case state == Current:
		return false, nil
	case state == Foreign:
		return false, ForeignError(path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("could not create directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".glab-govern-*.js.tmp")
	if err != nil {
		return false, fmt.Errorf("could not create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(Source); err != nil {
		return false, errors.Join(fmt.Errorf("could not write temp file: %w", err), tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return false, fmt.Errorf("could not close temp file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return false, fmt.Errorf("could not set file permissions: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return false, fmt.Errorf("could not rename temp file: %w", err)
	}
	return true, nil
}

// Remove deletes the plugin at path if glab installed it, and returns whether
// it did.
func Remove(path string) (bool, error) {
	state, err := Check(path)
	if err != nil || state == NotInstalled || state == Foreign {
		return false, err
	}
	if err := os.Remove(path); err != nil {
		return false, err
	}
	return true, nil
}
