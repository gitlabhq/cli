package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSnapConfined(t *testing.T) {
	t.Run("true when SNAP_NAME identifies glab", func(t *testing.T) {
		t.Setenv("SNAP_NAME", "glab")
		assert.True(t, SnapConfined())
	})

	t.Run("false when SNAP_NAME is empty", func(t *testing.T) {
		t.Setenv("SNAP_NAME", "")
		assert.False(t, SnapConfined())
	})

	t.Run("false when running inside a different snap (env inherited)", func(t *testing.T) {
		t.Setenv("SNAP_NAME", "code")
		assert.False(t, SnapConfined())
	})
}
