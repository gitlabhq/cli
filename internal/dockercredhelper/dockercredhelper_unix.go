//go:build linux || darwin

package dockercredhelper

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"gitlab.com/gitlab-org/cli/internal/fsx"
)

// Supported reports whether this operating system can run the shim. It always
// succeeds here.
func Supported() error { return nil }

// candidate is a directory Install may write the shim into.
type candidate struct {
	dir string
	// onPath records whether Docker can resolve the shim from dir as things
	// stand. Only ~/.local/bin can be false: every other candidate is a PATH
	// entry.
	onPath bool
	// create is set only for ~/.local/bin. A PATH entry that does not exist
	// is not somewhere the user asked glab to put files, whereas ~/.local/bin
	// is the conventional per-user bin directory and creating it is expected.
	create bool
}

// candidates returns the directories Install tries, in order. glabDir is the
// directory holding the glab binary.
//
// The shim re-resolves glab from PATH at run time rather than calling it by
// absolute path, so it does not have to sit next to the binary. The one hard
// requirement is a directory on PATH, because that is how Docker finds it.
//
// A failure to resolve the home directory is returned rather than fatal: it
// costs only the ~/.local/bin candidate, and a writable PATH entry still
// gives a working install. Install folds it into its own error if nothing
// else works out, where an unset $HOME is the actionable part.
func candidates(glabDir string) ([]candidate, error) {
	// Cleaned once, so membership and dedup compare the same spelling: a PATH
	// holding both /usr/bin and /usr/bin/ is one directory, and treating it as
	// two attempts the same write twice and names it twice in the failure.
	var onPath []string
	seen := make(map[string]struct{})
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		// Relative entries are skipped rather than resolved: "" means the
		// working directory, so honoring one would put the shim wherever glab
		// happened to be run from and leave Docker resolving it only from
		// there.
		if filepath.IsAbs(entry) {
			onPath = append(onPath, filepath.Clean(entry))
		}
	}

	var localBin string
	home, homeErr := os.UserHomeDir()
	if homeErr != nil {
		homeErr = fmt.Errorf("resolving the home directory for the %s fallback: %w", FullName, homeErr)
	} else {
		localBin = filepath.Join(home, ".local", "bin")
	}

	var found []candidate
	add := func(dir string, create bool) {
		dir = filepath.Clean(dir)
		if _, dup := seen[dir]; dup {
			return
		}
		seen[dir] = struct{}{}
		found = append(found, candidate{dir: dir, onPath: slices.Contains(onPath, dir), create: create})
	}

	// glabDir first. Locate resolved glab through PATH, so this directory is on
	// PATH by construction, and trying it first keeps a writable install
	// idempotent: the shim already next to glab is updated in place rather than
	// orphaned behind a second copy elsewhere on PATH.
	add(glabDir, false)

	// ~/.local/bin second, whether or not it is on PATH, and ahead of every
	// other PATH entry. It belongs to this user, where /usr/local/bin and
	// /opt/homebrew/bin are machine-wide and commonly group-writable. The shim
	// is written 0700, so a machine-wide copy is executable only by whoever
	// installed it: a second user's Docker finds it on PATH and fails with
	// permission denied instead of falling through, and their own
	// configure-docker then replaces it with a copy the first user cannot run.
	// A warning the user has to act on beats two users breaking each other.
	if localBin != "" {
		add(localBin, true)
	}

	// Reached only when neither of the above would accept the write.
	for _, dir := range onPath {
		add(dir, false)
	}

	return found, homeErr
}

// unusable reports whether err means the directory itself will not do, as
// opposed to something having gone wrong with this particular write. Only the
// former is worth trying the next candidate for; a full disk or an I/O error
// would fail identically everywhere, and retrying it down the whole of PATH
// would bury the real cause under a list of directories.
func unusable(err error) bool {
	return errors.Is(err, fs.ErrPermission) ||
		errors.Is(err, fs.ErrNotExist) ||
		errors.Is(err, syscall.EROFS) ||
		errors.Is(err, syscall.ENOTDIR)
}

// Install writes the shim into the first directory that will accept it,
// preferring the one holding the glab binary, and returns where it landed. It
// is idempotent: re-running it overwrites the script and forces the mode, so a
// shim left by an older glab is brought up to date.
//
// The preference order exists because glab's own directory is frequently not
// writable by the user running it: /usr/bin under the .deb and .rpm packages,
// or a read-only Nix store. That used to fail the whole command.
func Install() (Installation, error) {
	glabPath, err := Locate()
	if err != nil {
		return Installation{}, err
	}

	dirs, homeErr := candidates(filepath.Dir(glabPath))

	var refused []string
	for _, c := range dirs {
		path := filepath.Join(c.dir, FullName)

		err := writeShim(path, c.create)
		if err == nil {
			return Installation{Path: path, OnPath: c.onPath}, nil
		}
		if !unusable(err) {
			return Installation{}, fmt.Errorf("writing %s shim to %s: %w", FullName, c.dir, err)
		}
		refused = append(refused, c.dir)
	}

	// The directories are named because that is the whole remedy: the user
	// makes one of them writable, or puts a writable one on PATH. homeErr
	// joins it here and nowhere else: it only matters once every PATH entry
	// has refused, and then it is the reason ~/.local/bin is missing from the
	// list the user is being asked to choose from.
	return Installation{}, errors.Join(fmt.Errorf(
		"writing %s shim: no directory could be written to, having tried %s",
		FullName, strings.Join(refused, ", ")), homeErr)
}

func writeShim(path string, createDir bool) error {
	if createDir {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
	}

	// Docker invokes this one path for every registry it delegates to glab, so
	// the shim is written through fsx.WriteExecutable rather than in place: a
	// process killed mid-write, or a docker-credential-glab invocation racing
	// a concurrent install, must never observe a torn or non-executable
	// script. WriteExecutable lands the file at its final mode as part of the
	// same atomic rename, so unlike a write followed by a separate Chmod,
	// there is no window where the shim exists at its final path but isn't
	// yet executable.
	return fsx.WriteExecutable(path, script)
}
