package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gitlab.com/gitlab-org/cli/internal/dbg"
)

const writeProbeFilename = ".glab-write-probe"

// CredentialWriteProbe reports whether credentials for hostname could be
// persisted right now, without writing one.
//
// The keyring is checked only for a keyring-mode host: headless Linux and CI
// runners have none and refresh through the config file perfectly well.
func CredentialWriteProbe(cfg Config, hostname string) error {
	useKeyring, err := cfg.Get(hostname, "use_keyring")
	if err != nil {
		return fmt.Errorf("could not determine whether host %q stores credentials in the keyring: %w", hostname, err)
	}
	if useKeyring == "true" {
		// Report the backend's own error. Without it the message reads as a
		// permissions or ACL problem, which sends anyone debugging it in the wrong
		// direction.
		if err := keyringWriteError(); err != nil {
			return fmt.Errorf("host %q stores credentials in the operating system keyring, but the keyring is not accepting writes: %w", hostname, err)
		}
	}

	fc, ok := cfg.(*fileConfig)
	if !ok {
		dbg.Debugf("write probe: %T is not a *fileConfig, skipping the configuration directory check for %q", cfg, hostname)
		return nil
	}
	// Write() returns early for an empty dir, so there is no write to fail.
	if fc.dir == "" {
		return nil
	}

	probe := filepath.Join(fc.dir, writeProbeFilename)
	if err := writeConfigFile(probe, nil); err != nil {
		return fmt.Errorf("the configuration directory %q is not accepting writes: %w", fc.dir, err)
	}
	_ = os.Remove(probe)

	return nil
}
