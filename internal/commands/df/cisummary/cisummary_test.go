//go:build !integration

package cisummary

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestCISummaryNoLogPrintsNoActivity(t *testing.T) {
	t.Chdir(t.TempDir())
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
	out, err := exec("")
	require.NoError(t, err)
	assert.Contains(t, out.OutBuf.String(), "no Dependency Firewall activity recorded")
}

func TestCISummaryRendersBlockedAndExitsNonZero(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".gitlab", "df"), 0o755))
	log := `{"schemaVersion":1,"session":{"command":"npm install foo"},"entries":[` +
		`{"package":"foo","version":"1.2.3","verdict":"blocked","reason":"known malware","status":403}]}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitlab", "df", "ci-log.json"), []byte(log), 0o644))

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
	out, err := exec("")

	// The command must render the entry AND fail with blockExitCode so
	// CI jobs surface policy violations as job failures.
	require.Error(t, err)
	var withCode *cmdutils.ExitError
	require.ErrorAs(t, err, &withCode, "expected *cmdutils.ExitError, got %T", err)
	assert.Equal(t, blockExitCode, withCode.Code)
	assert.Contains(t, out.ErrBuf.String(), "foo")
	assert.Contains(t, out.ErrBuf.String(), "known malware")
}

func TestCISummaryRendersWarningWithoutError(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".gitlab", "df"), 0o755))
	log := `{"schemaVersion":1,"session":{"command":"pip install foo"},"entries":[` +
		`{"package":"foo","version":"1.2.3","verdict":"warning","reason":"license"}]}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitlab", "df", "ci-log.json"), []byte(log), 0o644))

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
	out, err := exec("")

	// Warnings alone must not fail the command.
	require.NoError(t, err)
	assert.Contains(t, out.OutBuf.String(), "foo")
}
