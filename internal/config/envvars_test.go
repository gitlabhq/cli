//go:build !integration

package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Unprefixed names such as PROXY or CA_CERT are easy to collide with on a
// managed machine, so every documented key offers a GLAB_ name. The GITLAB_
// names are already namespaced and are left alone.
func TestEnvVarsForKey_EveryDocumentedKeyOffersAPrefixedName(t *testing.T) {
	t.Parallel()

	for _, kd := range KeySchema {
		if !kd.UserSettable || kd.HelpHidden {
			continue
		}
		names := EnvVarsForKey(kd)
		if len(names) == 0 {
			continue
		}

		var prefixed bool
		for _, name := range names {
			if strings.HasPrefix(name, "GLAB_") || strings.HasPrefix(name, "GITLAB_") {
				prefixed = true
				break
			}
		}
		assert.True(t, prefixed, "key %q offers only unprefixed names: %v", kd.Name, names)
	}
}

// The preferred name is the one glab resolves first, so a GLAB_ name added for
// an existing key has to lead.
func TestEnvVarsForKey_PrefixedNameIsPreferred(t *testing.T) {
	t.Parallel()

	for _, kd := range KeySchema {
		names := EnvVarsForKey(kd)
		if len(names) < 2 {
			continue
		}
		if !strings.HasPrefix(names[0], "GLAB_") {
			continue
		}
		for _, name := range names[1:] {
			assert.False(t, strings.HasPrefix(name, "GLAB_"),
				"key %q lists %q after the preferred %q", kd.Name, name, names[0])
		}
	}
}
