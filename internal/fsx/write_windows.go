//go:build windows

package fsx

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// WriteOwnerOnly writes data to path and forces mode 0o600.
//
// Atomic replace is not available on Windows (renameio only supports POSIX;
// there is no cross-filesystem atomic rename primitive on Windows that
// matches the semantics of rename(2)), so the write degrades to os.WriteFile
// + os.Chmod. This mirrors internal/config.WriteFile, which also drops to
// os.WriteFile on Windows for the same reason.
//
// POSIX file modes are not enforced on Windows in the way this package
// relies on, so the Chmod call is best-effort. Callers should not use
// this package on Windows to protect a secret — the dependency-firewall
// engine core targets Linux CI runners where the atomicity and mode
// guarantees actually hold.
func WriteOwnerOnly(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// WriteExecutable writes data to path and forces mode 0o700.
//
// Atomic replace is not available on Windows (see WriteOwnerOnly), so this
// degrades to os.WriteFile + os.Chmod. Windows does not enforce POSIX
// execute bits, so the Chmod call is best-effort and the file is briefly
// visible at its final path in a non-executable state; callers targeting
// Windows must not rely on this function's atomicity guarantee.
func WriteExecutable(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o700); err != nil { // #nosec G302 -- 0o700 is intentional: owner rwx, no group/other access, for an executable shim
		return err
	}
	return os.Chmod(path, 0o700)
}

// WriteJSONFile creates path's parent directory (0o755), marshals v with
// two-space indent for user-editability, appends a trailing newline
// (POSIX text-file convention; keeps `git diff` clean when the file is
// hand-edited later), and writes the result to path with mode 0o644.
// The write is non-atomic on Windows; see WriteOwnerOnly for the rationale.
func WriteJSONFile(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(v, "", "  ") //nolint:forbidigo // writing config/log to disk, not stdout
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}
