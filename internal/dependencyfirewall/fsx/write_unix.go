//go:build !windows

package fsx

import (
	"github.com/google/renameio/v2"
)

// WriteOwnerOnly writes data to path atomically and forces mode 0o600.
//
// On POSIX the write is a temp-file-plus-rename via renameio, so a reader
// concurrent with the write either sees the full old file or the full new
// file — never a half-written mix. If the process dies mid-write, the target
// is untouched and only an orphaned temp file may be left behind.
//
// renameio.WriteFile cannot be used here: it always appends
// WithExistingPermissions(), which — when the target already exists —
// overrides any WithStaticPermissions/WithPermissions and creates the temp
// file with the existing target's (possibly loose) mode, only tightening it
// after the secret bytes are on disk. That leaves a window where token data
// is world/group-readable. Instead we drive renameio.NewPendingFile directly
// with only WithStaticPermissions(0o600), so the temp file is created 0o600
// from the outset (ignoring the existing mode and umask) with no post-write
// chmod window, then atomically replace the target.
//
// Use this for any file whose contents must stay owner-only even when a
// pre-existing target was created with a looser mode — currently the CI log
// (cilog.Save), whose integrity gates the firewall job's exit code on shared
// runners, and any future token-bearing file the firewall writes to disk.
func WriteOwnerOnly(path string, data []byte) error {
	f, err := renameio.NewPendingFile(path, renameio.WithStaticPermissions(0o600))
	if err != nil {
		return err
	}
	defer func() { _ = f.Cleanup() }()

	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.CloseAtomicallyReplace()
}
