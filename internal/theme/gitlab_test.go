//go:build !integration

package theme

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewGitLabColors(t *testing.T) {
	t.Run("dark", func(t *testing.T) {
		colors := NewGitLabColors(lipgloss.LightDark(true))

		assert.Equal(t, lipgloss.Color("#A989F5"), colors.Purple)
		assert.Equal(t, lipgloss.Color("#4285F4"), colors.Blue)
		assert.Equal(t, lipgloss.Color("#34D058"), colors.Green)
		assert.Equal(t, lipgloss.Color("#F97583"), colors.Red)
		assert.Equal(t, lipgloss.Color("#E5A000"), colors.Warning)
		assert.Equal(t, lipgloss.Color("#FAFAFA"), colors.Text)
		assert.Equal(t, lipgloss.Color("#B0B0B0"), colors.Subtle)
	})

	t.Run("light", func(t *testing.T) {
		colors := NewGitLabColors(lipgloss.LightDark(false))

		assert.Equal(t, lipgloss.Color("#7759C2"), colors.Purple)
		assert.Equal(t, lipgloss.Color("#1068BF"), colors.Blue)
		assert.Equal(t, lipgloss.Color("#217645"), colors.Green)
		assert.Equal(t, lipgloss.Color("#C91C00"), colors.Red)
		assert.Equal(t, lipgloss.Color("#C97A00"), colors.Warning)
		assert.Equal(t, lipgloss.Color("#171321"), colors.Text)
		assert.Equal(t, lipgloss.Color("#6B6B73"), colors.Subtle)
	})
}

// HuhTheme used to re-derive its own light/dark hex constants inline instead
// of reading from NewGitLabColors, so the two could drift. These tests pin
// HuhTheme's styles to the shared palette for both backgrounds.
func TestHuhTheme_UsesSharedPalette(t *testing.T) {
	for _, isDark := range []bool{true, false} {
		t.Run(map[bool]string{true: "dark", false: "light"}[isDark], func(t *testing.T) {
			colors := NewGitLabColors(lipgloss.LightDark(isDark))
			styles := HuhTheme()(isDark)
			require.NotNil(t, styles)

			assert.Equal(t, colors.Purple, styles.Focused.Title.GetForeground())
			assert.Equal(t, colors.Blue, styles.Focused.SelectedOption.GetForeground())
			assert.Equal(t, colors.Orange, styles.Focused.SelectedPrefix.GetForeground())
			assert.Equal(t, colors.Subtle, styles.Focused.UnselectedOption.GetForeground())
			assert.Equal(t, colors.Red, styles.Focused.ErrorMessage.GetForeground())
			assert.Equal(t, colors.Red, styles.Blurred.ErrorMessage.GetForeground())
		})
	}
}
