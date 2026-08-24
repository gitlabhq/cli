//go:build !integration

package orbit

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEnvironWith_OverrideReplacesUserValue(t *testing.T) {
	base := []string{"FOO=1", "ORBIT_API_BASE_URL=https://user.example.com", "BAR=2"}
	overrides := []string{"ORBIT_API_BASE_URL=https://gitlab.com", "GITLAB_ORBIT_DISTRIBUTION=glab"}

	got := environWith(base, overrides)

	assert.Contains(t, got, "FOO=1")
	assert.Contains(t, got, "BAR=2")
	assert.Contains(t, got, "ORBIT_API_BASE_URL=https://gitlab.com")
	assert.Contains(t, got, "GITLAB_ORBIT_DISTRIBUTION=glab")
	assert.NotContains(t, got, "ORBIT_API_BASE_URL=https://user.example.com")

	occurrences := 0
	for _, kv := range got {
		if strings.HasPrefix(kv, "ORBIT_API_BASE_URL=") {
			occurrences++
		}
	}
	assert.Equal(t, 1, occurrences)
}
