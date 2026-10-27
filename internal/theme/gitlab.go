package theme

import (
	"image/color"

	"charm.land/fang/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

// GitLabColors contains the GitLab product color palette.
// These colors are designed for accessibility across different terminal backgrounds.
type GitLabColors struct {
	// Brand colors
	Orange color.Color // GitLab brand orange (#FC6D26)
	Purple color.Color // GitLab brand purple (#7759C2 light, #A989F5 dark)

	// Semantic colors
	Blue    color.Color // Blue for current/active states (#1068BF light, #4285F4 dark)
	Green   color.Color // Green for success (#217645 light, #34D058 dark)
	Red     color.Color // Red for errors (#C91C00 light, #F97583 dark)
	Warning color.Color // Amber for warnings/caution (#C97A00 light, #E5A000 dark)

	// Neutral colors
	Text   color.Color // High-contrast text (#171321 light, #FAFAFA dark)
	Subtle color.Color // Subtle elements (#6B6B73 light, #B0B0B0 dark)
}

// NewGitLabColors creates GitLab brand colors using lightDarkFunc for accessibility.
// The lightDarkFunc automatically selects colors based on terminal background.
func NewGitLabColors(lightDarkFunc lipgloss.LightDarkFunc) GitLabColors {
	return GitLabColors{
		Orange:  lightDarkFunc(lipgloss.Color("#FC6D26"), lipgloss.Color("#FC6D26")),
		Purple:  lightDarkFunc(lipgloss.Color("#7759C2"), lipgloss.Color("#A989F5")),
		Blue:    lightDarkFunc(lipgloss.Color("#1068BF"), lipgloss.Color("#4285F4")),
		Green:   lightDarkFunc(lipgloss.Color("#217645"), lipgloss.Color("#34D058")),
		Red:     lightDarkFunc(lipgloss.Color("#C91C00"), lipgloss.Color("#F97583")),
		Warning: lightDarkFunc(lipgloss.Color("#C97A00"), lipgloss.Color("#E5A000")),
		Text:    lightDarkFunc(lipgloss.Color("#171321"), lipgloss.Color("#FAFAFA")),
		Subtle:  lightDarkFunc(lipgloss.Color("#6B6B73"), lipgloss.Color("#B0B0B0")),
	}
}

// FangColorScheme returns a fang color scheme with GitLab product colors.
// This maintains the existing fang styling established in the CLI.
func FangColorScheme(lightDarkFunc lipgloss.LightDarkFunc) fang.ColorScheme {
	colors := NewGitLabColors(lightDarkFunc)
	scheme := fang.DefaultColorScheme(lightDarkFunc)

	// Apply GitLab product color semantics
	scheme.Title = colors.Text            // Main command titles
	scheme.Command = colors.Purple        // Subcommands - purple for brand consistency
	scheme.Flag = colors.Purple           // Flags - purple for brand consistency
	scheme.FlagDefault = colors.Subtle    // Default flag values - subtle
	scheme.Description = colors.Text      // Command descriptions - high contrast
	scheme.Program = colors.Orange        // Program name (glab) - brand consistency
	scheme.Argument = colors.Blue         // Command arguments - blue for interactive elements
	scheme.DimmedArgument = colors.Subtle // Optional/dimmed arguments - reduced prominence
	scheme.QuotedString = colors.Text     // Quoted strings - high contrast
	scheme.Comment = colors.Subtle        // Comments - reduced visual weight
	scheme.Help = colors.Text             // Help text - maximum readability
	scheme.Dash = colors.Subtle           // Dashes and separators - subtle structure
	scheme.ErrorHeader = [2]color.Color{lipgloss.Color("#FFFFFF"), lipgloss.Color("#C91C00")}
	scheme.ErrorDetails = colors.Red // Error details in GitLab red

	return scheme
}

// HuhTheme returns a huh theme with GitLab product colors.
// This ensures consistent branding across interactive prompts.
func HuhTheme() huh.ThemeFunc {
	return func(isDark bool) *huh.Styles {
		theme := huh.ThemeBase(isDark)

		colors := NewGitLabColors(lipgloss.LightDark(isDark))
		gitlabPurple, gitlabOrange, gitlabBlue, gitlabRed, gitlabSubtle :=
			colors.Purple, colors.Orange, colors.Blue, colors.Red, colors.Subtle

		// Focused field styles (when user is interacting)
		theme.Focused.Base = theme.Focused.Base.BorderForeground(gitlabPurple)
		theme.Focused.Title = theme.Focused.Title.Foreground(gitlabPurple).Bold(true)
		theme.Focused.Description = theme.Focused.Description.Foreground(gitlabSubtle)
		theme.Focused.SelectedOption = theme.Focused.SelectedOption.Foreground(gitlabBlue)
		theme.Focused.UnselectedOption = theme.Focused.UnselectedOption.Foreground(gitlabSubtle)
		theme.Focused.SelectedPrefix = lipgloss.NewStyle().Foreground(gitlabOrange).SetString("✓ ")
		theme.Focused.UnselectedPrefix = lipgloss.NewStyle().Foreground(gitlabSubtle).SetString("• ")
		theme.Focused.MultiSelectSelector = theme.Focused.MultiSelectSelector.Foreground(gitlabOrange) // MultiSelect cursor arrow
		theme.Focused.SelectSelector = theme.Focused.SelectSelector.Foreground(gitlabOrange)           // Select cursor arrow

		// Text input styling
		theme.Focused.TextInput.Cursor = theme.Focused.TextInput.Cursor.Foreground(gitlabOrange)
		theme.Focused.TextInput.Placeholder = theme.Focused.TextInput.Placeholder.Foreground(gitlabSubtle)
		theme.Focused.TextInput.Prompt = theme.Focused.TextInput.Prompt.Foreground(gitlabPurple)

		// Blurred field styles (when not active)
		theme.Blurred.Base = theme.Blurred.Base.BorderForeground(gitlabSubtle)
		theme.Blurred.Title = theme.Blurred.Title.Foreground(gitlabSubtle)
		theme.Blurred.Description = theme.Blurred.Description.Foreground(gitlabSubtle)
		theme.Blurred.TextInput.Prompt = theme.Blurred.TextInput.Prompt.Foreground(gitlabSubtle)

		// Confirm button styles: filled = selected, plain text = not selected.
		// Both are single-row height; the filled orange block makes the active button obvious.
		theme.Focused.FocusedButton = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#171321")).
			Background(gitlabOrange).
			Bold(true).
			Padding(0, 2).
			MarginRight(1)
		theme.Focused.BlurredButton = lipgloss.NewStyle().
			Foreground(gitlabSubtle).
			Padding(0, 2).
			MarginRight(1)

		// Error styling
		theme.Focused.ErrorIndicator = theme.Focused.ErrorIndicator.Foreground(gitlabRed)
		theme.Focused.ErrorMessage = theme.Focused.ErrorMessage.Foreground(gitlabRed)
		theme.Blurred.ErrorIndicator = theme.Blurred.ErrorIndicator.Foreground(gitlabRed)
		theme.Blurred.ErrorMessage = theme.Blurred.ErrorMessage.Foreground(gitlabRed)

		return theme
	}
}
