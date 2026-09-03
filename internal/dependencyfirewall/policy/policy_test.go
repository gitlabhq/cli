//go:build !integration

package policy

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
)

func TestResultAllowIsZeroVerdict(t *testing.T) {
	t.Parallel()
	var r Result
	assert.Equal(t, verdict.Allowed, r.Verdict)
	assert.True(t, r.Allowed())
	assert.False(t, r.Blocked())
	assert.False(t, r.Warned())
}

func TestResultBlockedHelper(t *testing.T) {
	t.Parallel()
	r := Result{Verdict: verdict.Blocked}
	assert.True(t, r.Blocked())
	assert.False(t, r.Allowed())
	assert.False(t, r.Warned())
}

func TestResultWarnedHelper(t *testing.T) {
	t.Parallel()
	r := Result{Verdict: verdict.Warning}
	assert.True(t, r.Warned())
	assert.False(t, r.Allowed())
	assert.False(t, r.Blocked())
}

func TestCoordinateKey(t *testing.T) {
	t.Parallel()
	c := Coordinate{Ecosystem: "npm", Name: "left-pad", Version: "1.3.0"}
	assert.Equal(t, "npm:left-pad@1.3.0", c.Key())
}

func TestOperationString(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "download", Download.String())
	assert.Equal(t, "upload", Upload.String())
}

func TestOperationStringUnknown(t *testing.T) {
	t.Parallel()
	// An out-of-range operation renders through the default branch rather than
	// panicking or printing a bare integer, so a stray value stays legible in
	// logs and cache keys.
	assert.Equal(t, "operation(99)", Operation(99).String())
}

func TestRequestKey(t *testing.T) {
	t.Parallel()
	// The key must include project and operation so verdicts don't leak
	// across projects or between download and upload.
	base := Request{
		Coordinate: Coordinate{Ecosystem: "npm", Name: "left-pad", Version: "1.3.0"},
		ProjectID:  "group/app",
		Operation:  Download,
	}
	assert.Equal(t, "npm:left-pad@1.3.0|group/app|download", base.Key())

	// Same coordinate, different operation -> different key.
	upload := base
	upload.Operation = Upload
	assert.NotEqual(t, base.Key(), upload.Key())

	// Same coordinate, different project -> different key.
	otherProject := base
	otherProject.ProjectID = "group/other"
	assert.NotEqual(t, base.Key(), otherProject.Key())
}

func TestNewSelectsFakeWhenConfigured(t *testing.T) {
	t.Setenv("GITLAB_CI", "")
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "block")

	c, err := New(nil)
	require.NoError(t, err)
	_, isFake := c.(fakeChecker)
	assert.True(t, isFake, "expected the fake checker when GLAB_DF_FAKE_* is set")
}

func TestNewRefusesFakeInCI(t *testing.T) {
	t.Setenv("GITLAB_CI", "true")
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")

	c, err := New(nil)
	require.Error(t, err, "fake mode must be refused under GITLAB_CI=true")
	assert.Nil(t, c)
	assert.Contains(t, err.Error(), fakeEnvPrefix)
}

func TestNewSelectsNonFakeWhenUnconfigured(t *testing.T) {
	// t.Setenv can only set, not unset; clear any ambient GLAB_DF_FAKE_*
	// so the non-fake branch is exercised.
	for _, e := range os.Environ() {
		if k, _, ok := strings.Cut(e, "="); ok && strings.HasPrefix(k, fakeEnvPrefix) {
			t.Setenv(k, "")
			os.Unsetenv(k)
		}
	}

	c, err := New(nil)
	require.NoError(t, err)
	_, isFake := c.(fakeChecker)
	assert.False(t, isFake, "expected the non-fake checker when no GLAB_DF_FAKE_* is set")
	assert.NotNil(t, c)
}
