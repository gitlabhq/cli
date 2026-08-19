//go:build !integration

package policy

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
)

func TestResultAllowIsZeroVerdict(t *testing.T) {
	var r Result
	assert.Equal(t, verdict.Verdict(""), r.Verdict)
	assert.False(t, r.Blocked())
}

func TestResultBlockedHelper(t *testing.T) {
	r := Result{Verdict: verdict.Blocked}
	assert.True(t, r.Blocked())
}

func TestCoordinateKey(t *testing.T) {
	c := Coordinate{Ecosystem: "npm", Name: "left-pad", Version: "1.3.0"}
	assert.Equal(t, "npm:left-pad@1.3.0", c.Key())
}

func TestNewSelectsFakeWhenConfigured(t *testing.T) {
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "block")

	c := New(nil, "some/project")
	_, isFake := c.(fakeChecker)
	assert.True(t, isFake, "expected the fake checker when GLAB_DF_FAKE_* is set")
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

	c := New(nil, "some/project")
	_, isFake := c.(fakeChecker)
	assert.False(t, isFake, "expected the non-fake checker when no GLAB_DF_FAKE_* is set")
	assert.NotNil(t, c)
}
