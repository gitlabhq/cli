// Package fsx contains small filesystem helpers shared across the
// dependency-firewall packages. Its current use is writing the end-of-run
// CI log (.gitlab/df/ci-log.json) via WriteOwnerOnly, which cilog.Save uses
// so the gate-controlling log is created 0o600 on every rewrite.
//
// # Why a parallel helper vs. internal/config.WriteFile
//
// The CLI already ships internal/config.WriteFile, which wraps renameio for
// atomic writes. We keep this thin parallel helper for two reasons:
//
//  1. Deterministic owner-only permissions. renameio.WriteFile applies
//     WithExistingPermissions() by default, which preserves the *existing*
//     mode when the target file is already on disk and ignores the perm
//     argument. WriteOwnerOnly instead passes WithStaticPermissions(0o600)
//     so the temp file is created 0o600 from the outset (ignoring the
//     existing mode and umask, with no post-write chmod window) and a
//     rewritten token-bearing file cannot silently retain a looser,
//     pre-existing mode on a shared host or CI runner.
//
//  2. Transitive dependency footprint. internal/config pulls in go-keyring,
//     viper, and yaml — hundreds of transitive packages. Keeping the
//     dependency-firewall core lean keeps the blast radius small when the
//     config subsystem changes.
//
// The write is atomic on POSIX (tempfile + rename); on Windows that guarantee
// is not available and the write degrades to os.WriteFile, matching how
// internal/config.WriteFile behaves on that platform.
package fsx
