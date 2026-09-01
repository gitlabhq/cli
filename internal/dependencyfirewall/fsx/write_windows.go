//go:build windows

package fsx

import (
	"os"
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
