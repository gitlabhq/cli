//go:build !integration

package summary

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestRenderEmptyPrintsNoActivity(t *testing.T) {
	ios, _, out, _ := cmdtest.TestIOStreams()
	Render(ios, nil)
	assert.Contains(t, out.String(), "no Dependency Firewall activity recorded")
}

func TestRenderIncludesTitleAndTableHeaders(t *testing.T) {
	ios, _, _, errOut := cmdtest.TestIOStreams()
	entries := []verdict.Entry{
		{Package: "lodash", Version: "4.17.21", Verdict: verdict.Blocked, Reason: "policy violation"},
	}
	Render(ios, entries)

	stderr := errOut.String()
	assert.Contains(t, stderr, "GitLab Dependency Firewall")
	assert.Contains(t, stderr, "PACKAGE")
	assert.Contains(t, stderr, "VERSION")
	assert.Contains(t, stderr, "STATUS")
	assert.Contains(t, stderr, "REASON")
}

func TestRenderSingleBlockedGoesToStderr(t *testing.T) {
	ios, _, _, errOut := cmdtest.TestIOStreams()
	entries := []verdict.Entry{
		{Package: "lodash", Version: "4.17.21", Verdict: verdict.Blocked, Reason: "policy violation"},
	}
	Render(ios, entries)

	stderr := errOut.String()
	assert.Contains(t, stderr, "1 package blocked")
	assert.Contains(t, stderr, "lodash")
	assert.Contains(t, stderr, "4.17.21")
	assert.Contains(t, stderr, "Blocked")
}

func TestRenderSingleWarningGoesToStdout(t *testing.T) {
	ios, _, out, _ := cmdtest.TestIOStreams()
	entries := []verdict.Entry{
		{Package: "left-pad", Version: "1.3.0", Verdict: verdict.Warning, Reason: "Deprecated package"},
	}
	Render(ios, entries)

	stdout := out.String()
	assert.Contains(t, stdout, "1 package warning")
	assert.Contains(t, stdout, "left-pad")
	assert.Contains(t, stdout, "Warning")
}

func TestRenderMixedSummaryLine(t *testing.T) {
	ios, _, _, errOut := cmdtest.TestIOStreams()
	entries := []verdict.Entry{
		{Package: "lodash", Version: "4.17.21", Verdict: verdict.Blocked, Reason: "policy violation"},
		{Package: "left-pad", Version: "1.3.0", Verdict: verdict.Warning, Reason: "Deprecated package"},
	}
	Render(ios, entries)

	stderr := errOut.String()
	assert.Contains(t, stderr, "2 issues found: 1 blocked, 1 warning")
	assert.Contains(t, stderr, "lodash")
	assert.Contains(t, stderr, "left-pad")
}

func TestRenderStartsWithBlankLine(t *testing.T) {
	ios, _, _, errOut := cmdtest.TestIOStreams()
	entries := []verdict.Entry{
		{Package: "lodash", Version: "4.17.21", Verdict: verdict.Blocked, Reason: "policy violation"},
	}
	Render(ios, entries)

	assert.True(t, strings.HasPrefix(errOut.String(), "\n"), "expected a leading blank line before the summary")
}

func TestRenderTruncatesLongPackageName(t *testing.T) {
	ios, _, _, errOut := cmdtest.TestIOStreams()
	longName := "@some-really-long-scope/a-package-with-a-long-name"
	entries := []verdict.Entry{
		{Package: longName, Version: "10.4.2", Verdict: verdict.Blocked, Reason: "policy violation with a fairly long reason that must not be truncated"},
	}
	Render(ios, entries)

	stderr := errOut.String()
	assert.NotContains(t, stderr, longName, "package name longer than the cap should be truncated")
	assert.Contains(t, stderr, "@some-really-long...", "truncated package name should end with an ellipsis")
	assert.Contains(t, stderr, "policy violation with a fairly long reason that must not be truncated", "reason must never be truncated")
}

func TestRenderKeepsShortPackageNameIntact(t *testing.T) {
	ios, _, _, errOut := cmdtest.TestIOStreams()
	entries := []verdict.Entry{
		{Package: "lodash", Version: "4.17.21", Verdict: verdict.Blocked, Reason: "policy violation"},
	}
	Render(ios, entries)

	assert.Contains(t, errOut.String(), "lodash")
}

func TestRenderDoesNotDrawBespokeBox(t *testing.T) {
	ios, _, _, errOut := cmdtest.TestIOStreams()
	entries := []verdict.Entry{
		{Package: "lodash", Version: "4.17.21", Verdict: verdict.Blocked, Reason: "policy violation"},
	}
	Render(ios, entries)

	stderr := errOut.String()
	for _, c := range []string{"┌", "┐", "└", "┘", "│"} {
		assert.NotContains(t, stderr, c, "output should not contain bespoke box char %q; use tableprinter instead", c)
	}
}
