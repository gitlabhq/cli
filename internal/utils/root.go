package utils

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnsureDestinationRoot creates the directory that path lives in and returns a
// root anchored at it, together with the file name to use inside that root.
//
// Absolute paths are honored as-is: the caller asked for that location
// explicitly. Relative paths must stay under the working directory —
// filepath.IsLocal is the rule — so a relative "../elsewhere" is rejected
// before any directory is created.
//
// The returned *os.Root confines the caller's subsequent writes to dir, so
// server-supplied or otherwise untrusted names cannot escape via traversal or
// symbolic-link tricks. Callers should use root.Create(name), root.Stat(name),
// and so on rather than plain os.Create on a joined path.
//
// The caller owns the returned root and is responsible for closing it.
func EnsureDestinationRoot(path string) (*os.Root, string, error) {
	if !filepath.IsAbs(path) && !filepath.IsLocal(path) {
		return nil, "", fmt.Errorf("relative path %q escapes the working directory", path)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", fmt.Errorf("error creating directory: %w", err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, "", fmt.Errorf("unable to open root directory: %w", err)
	}

	return root, filepath.Base(path), nil
}
