//go:build !integration

package sync

import (
	"os"
	"strings"
	"testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestMain(m *testing.M) {
	cleanup := cmdtest.IsolateConfig()
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// fake builds a token at run time, so the source holds nothing that looks like
// a real secret to secret scanners.
func fake(prefix string, n int) string {
	return prefix + strings.Repeat("a1B2", n/4+1)[:n]
}
