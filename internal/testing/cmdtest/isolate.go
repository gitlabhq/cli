package cmdtest

import (
	"log"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
)

// IsolateConfig is for a TestMain. It points HOME, USERPROFILE (the home
// directory on Windows), XDG_CONFIG_HOME, XDG_RUNTIME_DIR and
// GLAB_CONFIG_DIR at a new temporary directory, and
// unsets CLAUDE_CONFIG_DIR and CODEX_HOME, so that no test reads or changes
// the developer's glab, Claude Code, Codex or systemd user state. It returns
// a function that removes the directory.
//
// Setting HOME in a test is not enough: xdg computes the default glab config
// directory from the real HOME when it loads.
func IsolateConfig() func() {
	dir, err := os.MkdirTemp("", "glab-test-config-")
	if err != nil {
		log.Fatal(err)
	}
	for name, sub := range map[string]string{"HOME": "home", "USERPROFILE": "home", "XDG_CONFIG_HOME": "xdg-config", "XDG_RUNTIME_DIR": "xdg-runtime", "GLAB_CONFIG_DIR": "glab"} {
		path := filepath.Join(dir, sub)
		if err := os.MkdirAll(path, 0o700); err != nil {
			log.Fatal(err)
		}
		if err := os.Setenv(name, path); err != nil {
			log.Fatal(err)
		}
	}
	for _, name := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME"} {
		if err := os.Unsetenv(name); err != nil {
			log.Fatal(err)
		}
	}
	xdg.Reload()
	return func() { _ = os.RemoveAll(dir) }
}
