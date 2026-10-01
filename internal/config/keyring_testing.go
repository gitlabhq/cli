package config

import (
	"testing"

	"github.com/zalando/go-keyring"
)

// Test binaries must never read, overwrite, or delete the developer's stored
// credentials. go-keyring's backend is process-global, so this covers every
// test that links this package without any per-package setup.
func init() {
	if testing.Testing() {
		keyring.MockInit()
	}
}
