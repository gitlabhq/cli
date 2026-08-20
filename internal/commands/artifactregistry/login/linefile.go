package login

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/fsx"
)

// homeDir resolves the home directory every writer roots its target path in,
// wrapping the failure once so all four writers report it in the same words.
func homeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return home, nil
}

// upsertLines reads path as newline-separated lines (treating a missing
// file as empty), passes them to mutate for an in-place update-or-append,
// then writes the result back with mode 0o600 via fsx.WriteOwnerOnly:
// shared by loginGradle, loginNpm, and loginSbt, whose only difference is the
// upsert rule mutate implements. loginMaven doesn't use this: its target is
// an XML block, not a line-oriented file.
//
// The write-back is atomic (temp file plus rename) on POSIX only. On Windows
// fsx.WriteOwnerOnly degrades to a plain os.WriteFile, so a reader concurrent
// with the write can observe a partial file there.
//
// The read-modify-write is not compare-and-swap: two concurrent logins
// against the same file can race, and the last writer wins. Acceptable for
// an interactively-invoked CLI.
//
// mutate returns an error for a file this command cannot edit safely, which
// leaves path untouched. Writing something the tool will not read back is the
// failure worth refusing: it reports a login the next build does not have.
func upsertLines(path string, mutate func(lines []string) ([]string, error)) error {
	content, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	var lines []string
	if len(content) > 0 {
		lines = strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	}

	lines, err = mutate(lines)
	if err != nil {
		return fmt.Errorf("updating %s: %w", path, err)
	}

	// Derived rather than taken as an argument: every target is a file inside
	// the directory the writer has to create, so the two cannot disagree.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	if err := fsx.WriteOwnerOnly(path, []byte(strings.Join(lines, "\n")+"\n")); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}
