//go:build !windows

package fsx

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/google/renameio/v2"
)

// WriteOwnerOnly writes data to path atomically with mode 0o600.
//
// On POSIX the write is a temp-file-plus-rename via renameio, so a reader
// concurrent with the write either sees the full old file or the full new
// file — never a half-written mix. If the process dies mid-write, the target
// is untouched and only an orphaned temp file may be left behind.
//
// renameio.WriteFile defaults to WithExistingPermissions(), which would give
// the new bytes the mode of a file someone loosened, until a Chmod after the
// rename caught up. The mode is set on the temp file instead, so a token is
// never readable by anyone else, not even briefly.
//
// Callers use this for any file that may embed an inline authentication
// token — for example, .npmrc, .yarnrc.yml, or a patched Pipfile — and for
// the token-bearing backups those files produce.
func WriteOwnerOnly(path string, data []byte) error {
	return writeWithMode(path, data, 0o600)
}

// WriteExecutable writes data to path atomically with mode 0o700. A reader
// racing the write — for example, a shim invocation racing a concurrent
// install — never observes the file at its final path before it is
// executable.
//
// Callers use this for shim scripts that Docker or another tool invokes
// directly. Never use it for a file that may hold a secret; see
// WriteOwnerOnly for those.
func WriteExecutable(path string, data []byte) error {
	return writeWithMode(path, data, 0o700)
}

// WriteWorldReadable writes data to path atomically with mode 0o644, for a
// public file that another, unprivileged user has to read, such as a dnf
// .repo file or a public key. Never use it for a file that may hold a secret.
func WriteWorldReadable(path string, data []byte) error {
	return writeWithMode(path, data, 0o644)
}

// writeWithMode sets mode on the temp file before the atomic rename, so the
// file never appears at path with any other mode, whatever mode an existing
// file had.
func writeWithMode(path string, data []byte, mode os.FileMode) error {
	t, err := renameio.NewPendingFile(path, renameio.WithStaticPermissions(mode))
	if err != nil {
		return err
	}
	defer func() { _ = t.Cleanup() }()

	if _, err := t.Write(data); err != nil {
		return err
	}

	return t.CloseAtomicallyReplace()
}

// WriteJSONFile creates path's parent directory (0o755), marshals v with
// two-space indent for user-editability, appends a trailing newline
// (POSIX text-file convention; keeps `git diff` clean when the file is
// hand-edited later), and writes the result to path with mode 0o644.
// The write is atomic via renameio. Use it for non-secret configuration
// and log files; token-bearing files must use WriteOwnerOnly instead.
func WriteJSONFile(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(v, "", "  ") //nolint:forbidigo // writing config/log to disk, not stdout
	if err != nil {
		return err
	}
	return renameio.WriteFile(path, append(raw, '\n'), 0o644)
}
