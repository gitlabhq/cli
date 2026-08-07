// Package fsx contains small filesystem helpers for callers that need a
// write to land at a forced permission mode, atomically where the platform
// allows it: a token-bearing file that must not stay world-readable, or an
// executable shim that must never be observed in a non-executable state.
//
// # Why a parallel helper vs. internal/config.WriteFile
//
// The CLI already ships internal/config.WriteFile, which wraps renameio for
// atomic writes. We considered reusing it and chose not to for two reasons:
//
//  1. Permissions mismatch on token-bearing files. renameio.WriteFile applies
//     WithExistingPermissions() by default, which preserves the *existing*
//     mode when the target file is already on disk and ignores the perm
//     argument. That is the exact opposite of what a token-bearing file
//     needs: the whole reason WriteOwnerOnly exists is to tighten a
//     pre-existing, possibly world-readable file (for example, the
//     dependency firewall's .npmrc) down to 0o600 so a live _authToken is
//     not readable by other users on a shared host or CI runner. A straight
//     call to config.WriteFile(path, data, 0o600) would silently keep a
//     stale 0o644 and reintroduce the leak dbickford flagged on !3438.
//
//     npm itself does not help us here. npm's own save() writes the user
//     config (~/.npmrc) at 0o600 because that is where credentials belong,
//     but writes the project-level config (the sibling of package.json) at
//     0o666 because a project .npmrc is not expected to hold credentials.
//     The dependency firewall deliberately embeds an inline _authToken in
//     the project .npmrc to point npm at the CI-scoped proxy, which
//     violates npm's own "auth goes in the user config" convention. So we
//     cannot inherit npm's project-level 0o666 default; every write from
//     this package must actively enforce its caller's requested mode.
//
//  2. Transitive dependency footprint. internal/config pulls in go-keyring,
//     viper, and yaml — 291 transitive packages against fsx's 69. Keeping
//     callers like the dependency-firewall engine core lean lets the proxy
//     binary link cleanly and keeps blast radius small when the config
//     subsystem changes.
//
// So this package wires renameio directly (already vendored) and layers an
// explicit os.Chmod on top to defeat WithExistingPermissions() on overwrites.
// The write itself is atomic on POSIX (tempfile + rename); on Windows the
// same guarantee is not available and the write degrades to os.WriteFile,
// which matches how internal/config.WriteFile behaves on that platform.
package fsx
