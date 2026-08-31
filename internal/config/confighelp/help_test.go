//go:build !integration

package confighelp

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/gitlab-org/cli/internal/config"
)

func TestSettings_CoversEveryDocumentedKeyExactlyOnce(t *testing.T) {
	t.Parallel()

	got := Settings()
	lines := strings.Split(got, "\n")

	var want int
	for _, kd := range config.KeySchema {
		if !kd.UserSettable || kd.HelpHidden {
			continue
		}
		want++
		assert.Equal(t, 1, strings.Count(got, "- `"+kd.Name+"`: "),
			"key %q should appear exactly once", kd.Name)
	}
	assert.Len(t, lines, want)
}

func TestSettings_OmitsHiddenAndInternalKeys(t *testing.T) {
	t.Parallel()

	got := Settings()
	for _, kd := range config.KeySchema {
		if kd.UserSettable && !kd.HelpHidden {
			continue
		}
		assert.NotContains(t, got, "- `"+kd.Name+"`: ", "key %q should not be documented", kd.Name)
	}
}

func TestSettings_DocumentsAliases(t *testing.T) {
	t.Parallel()

	got := Settings()
	for _, kd := range config.KeySchema {
		if !kd.UserSettable || kd.HelpHidden {
			continue
		}
		for _, alias := range kd.Aliases {
			assert.Contains(t, got, "`"+alias+"`", "alias %q of %q should be documented", alias, kd.Name)
		}
	}
}

func TestSettings_RendersOneBulletPerLine(t *testing.T) {
	t.Parallel()

	for line := range strings.SplitSeq(Settings(), "\n") {
		assert.True(t, strings.HasPrefix(line, "- `"), "unexpected line: %q", line)
		assert.True(t, strings.HasSuffix(line, "."), "line should end in a period: %q", line)
	}
}
