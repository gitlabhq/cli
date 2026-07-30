package utils

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// EnsureDestinationRoot creates the directory that path lives in and returns a
// root anchored at it, together with the file name to use inside that root.
//
// A relative path is created through a root anchored at the working directory,
// so it cannot escape the directory the command was run from. An absolute path
// names a location the caller asked for explicitly, so it is created directly:
// os.Root rejects absolute names by design, so routing one through the
// working-directory root would fail with "path escapes from parent".
//
// The caller owns the returned root and is responsible for closing it.
func EnsureDestinationRoot(path string) (*os.Root, string, error) {
	dir := filepath.Dir(path)

	if filepath.IsAbs(path) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, "", fmt.Errorf("error creating directory: %w", err)
		}
	} else {
		wd, err := os.OpenRoot(".")
		if err != nil {
			return nil, "", fmt.Errorf("unable to open root directory: %w", err)
		}

		// wd is finished with as soon as the directory exists, so it is closed
		// here rather than deferred, and its error is surfaced alongside the
		// primary one instead of being discarded.
		if err := errors.Join(ensureDirectoryExists(wd, path), wd.Close()); err != nil {
			return nil, "", err
		}
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, "", fmt.Errorf("unable to open root directory: %w", err)
	}

	return root, filepath.Base(path), nil
}

// ensureDirectoryExists creates the directory component of path inside root.
func ensureDirectoryExists(root *os.Root, path string) error {
	dir := filepath.Dir(path)
	if dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("error creating directory: %w", err)
		}
	}

	return nil
}
