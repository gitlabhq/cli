//go:build !integration

package main

import (
	"bytes"
	"errors"
	"log"
	"os"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
	"go.uber.org/goleak"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// Test started when the test binary is started
// and calls the main function
func TestGlab(t *testing.T) {
	t.Setenv("GLAB_CONFIG_DIR", t.TempDir())
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)
	main()
}

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		goleak.IgnoreTopFunction("internal/poll.runtime_pollWait"), // HTTP keep-alive connections
	)
}

func TestMain_isUpdateCheckEnabled(t *testing.T) {
	tests := []struct {
		name           string
		envValue       string
		config         config.Config
		expectedResult bool
		description    string
	}{
		{
			name:           "env var enabled true",
			envValue:       "true",
			config:         config.NewFromString(""),
			expectedResult: true,
			description:    "GLAB_CHECK_UPDATE=true should return true",
		},
		{
			name:           "env var enabled 1",
			envValue:       "1",
			config:         config.NewFromString(""),
			expectedResult: true,
			description:    "GLAB_CHECK_UPDATE=1 should return true",
		},
		{
			name:           "env var disabled false",
			envValue:       "false",
			config:         config.NewFromString(""),
			expectedResult: false,
			description:    "GLAB_CHECK_UPDATE=false should return false",
		},
		{
			name:           "env var disabled 0",
			envValue:       "0",
			config:         config.NewFromString(""),
			expectedResult: false,
			description:    "GLAB_CHECK_UPDATE=0 should return false",
		},
		{
			name:           "config enabled true",
			envValue:       "",
			config:         config.NewFromString("check_update: true"),
			expectedResult: true,
			description:    "check_update=true in config should return true",
		},
		{
			name:           "config enabled 1",
			envValue:       "",
			config:         config.NewFromString("check_update: 1"),
			expectedResult: true,
			description:    "check_update=1 in config should return true",
		},
		{
			name:           "config disabled false",
			envValue:       "",
			config:         config.NewFromString("check_update: false"),
			expectedResult: false,
			description:    "check_update=false in config should return false",
		},
		{
			name:           "config disabled 0",
			envValue:       "",
			config:         config.NewFromString("check_update: 0"),
			expectedResult: false,
			description:    "check_update=0 in config should return false",
		},
		{
			name:           "env var overrides config",
			envValue:       "false",
			config:         config.NewFromString("check_update: true"),
			expectedResult: false,
			description:    "Environment variable should take precedence over config",
		},
		{
			name:           "default when no config",
			envValue:       "",
			config:         config.NewFromString(""),
			expectedResult: true,
			description:    "Should default to true when no config is set",
		},
		{
			name:           "invalid config value returns false",
			envValue:       "",
			config:         config.NewFromString("check_update: invalid"),
			expectedResult: false,
			description:    "Invalid config value should return false (strconv.ParseBool zero value)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variable if specified
			if tt.envValue != "" {
				t.Setenv("GLAB_CHECK_UPDATE", tt.envValue)
			}

			ios, _, _, _ := cmdtest.TestIOStreams()
			factory := cmdtest.NewTestFactory(ios, cmdtest.WithConfig(tt.config))

			result := isUpdateCheckEnabled(factory)

			assert.Equal(t, tt.expectedResult, result, tt.description)
		})
	}
}

func TestMain_configureHyperlinks(t *testing.T) {
	tests := []struct {
		name        string
		envValue    string
		envName     string
		config      config.Config
		expected    string
		description string
	}{
		{
			name:        "config unset, no env",
			config:      config.NewFromString(""),
			expected:    "never",
			description: "configureHyperlinks should not override the IOStreams default when config and env are unset",
		},
		{
			name:        "config=true, no env",
			config:      config.NewFromString("display_hyperlinks: true"),
			expected:    "auto",
			description: "display_hyperlinks=true should map to auto (TTY-only)",
		},
		{
			name:        "config=false, no env",
			config:      config.NewFromString("display_hyperlinks: false"),
			expected:    "never",
			description: "display_hyperlinks=false should map to never",
		},
		{
			name:        "FORCE_HYPERLINKS=1, config unset",
			envName:     "FORCE_HYPERLINKS",
			envValue:    "1",
			config:      config.NewFromString(""),
			expected:    "always",
			description: "FORCE_HYPERLINKS=1 should force always when config is unset",
		},
		{
			name:        "FORCE_HYPERLINKS=1, config=false",
			envName:     "FORCE_HYPERLINKS",
			envValue:    "1",
			config:      config.NewFromString("display_hyperlinks: false"),
			expected:    "always",
			description: "FORCE_HYPERLINKS=1 should override display_hyperlinks=false",
		},
		{
			name:        "FORCE_HYPERLINKS=0, config unset",
			envName:     "FORCE_HYPERLINKS",
			envValue:    "0",
			config:      config.NewFromString(""),
			expected:    "never",
			description: "a falsy FORCE_HYPERLINKS should fall through to the config check, leaving the IOStreams default in place",
		},
		{
			name:        "FORCE_HYPERLINKS=0, config=false",
			envName:     "FORCE_HYPERLINKS",
			envValue:    "0",
			config:      config.NewFromString("display_hyperlinks: false"),
			expected:    "never",
			description: "a falsy FORCE_HYPERLINKS should fall through and let display_hyperlinks=false win",
		},
		{
			name:        "GLAB_FORCE_HYPERLINKS=1, config=false",
			envName:     "GLAB_FORCE_HYPERLINKS",
			envValue:    "1",
			config:      config.NewFromString("display_hyperlinks: false"),
			expected:    "always",
			description: "GLAB_FORCE_HYPERLINKS=1 should override display_hyperlinks=false",
		},
		{
			name:        "GLAB_FORCE_HYPERLINKS=0, config=false",
			envName:     "GLAB_FORCE_HYPERLINKS",
			envValue:    "0",
			config:      config.NewFromString("display_hyperlinks: false"),
			expected:    "never",
			description: "a falsy GLAB_FORCE_HYPERLINKS should fall through and let display_hyperlinks=false win",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear both env vars first so each subtest is hermetic regardless
			// of the surrounding shell environment.
			t.Setenv("FORCE_HYPERLINKS", "")
			t.Setenv("GLAB_FORCE_HYPERLINKS", "")
			os.Unsetenv("FORCE_HYPERLINKS")
			os.Unsetenv("GLAB_FORCE_HYPERLINKS")

			if tt.envName != "" {
				t.Setenv(tt.envName, tt.envValue)
			}

			ios, _, _, _ := cmdtest.TestIOStreams()
			configureHyperlinks(ios, tt.config)

			assert.Equal(t, tt.expected, ios.DisplayHyperlinks(), tt.description)
		})
	}
}

func TestMain_configureColorMode(t *testing.T) {
	tests := []struct {
		name        string
		envValue    string
		config      config.Config
		expected    iostreams.ColorMode
		description string
	}{
		{
			name:        "config unset, no env",
			config:      config.NewFromString(""),
			expected:    iostreams.ColorModeAuto,
			description: "the color key's schema default (auto) applies when nothing is set",
		},
		{
			name:        "config=never",
			config:      config.NewFromString("color: never"),
			expected:    iostreams.ColorModeNever,
			description: "color: never should force color off",
		},
		{
			name:        "config=always",
			config:      config.NewFromString("color: always"),
			expected:    iostreams.ColorModeAlways,
			description: "color: always should force color on",
		},
		{
			name:        "GLAB_COLOR=never, config=always",
			envValue:    "never",
			config:      config.NewFromString("color: always"),
			expected:    iostreams.ColorModeNever,
			description: "GLAB_COLOR should take precedence over the config file, per the color key's declared EnvVars",
		},
		{
			name:        "config=bogus falls back to auto",
			config:      config.NewFromString("color: bogus"),
			expected:    iostreams.ColorModeAuto,
			description: "an unrecognized color value should fall back to auto rather than failing the command",
		},
		{
			name:        "config=truecolor (a removed depth value) falls back to auto",
			config:      config.NewFromString("color: truecolor"),
			expected:    iostreams.ColorModeAuto,
			description: "color only takes whether values; color: truecolor falls back to auto",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GLAB_COLOR", "")
			os.Unsetenv("GLAB_COLOR")
			if tt.envValue != "" {
				t.Setenv("GLAB_COLOR", tt.envValue)
			}

			ios, _, _, _ := cmdtest.TestIOStreams()
			configureColorMode(ios, tt.config)

			assert.Equal(t, tt.expected, ios.ColorMode(), tt.description)
		})
	}
}

// An unrecognized color value falls back to auto and is logged under
// GLAB_DEBUG; an unset value logs nothing.
func TestMain_configureColorMode_LogsUnrecognizedValue(t *testing.T) {
	t.Setenv("GLAB_DEBUG", "true")
	t.Setenv("GLAB_COLOR", "")
	os.Unsetenv("GLAB_COLOR")

	var logOutput bytes.Buffer
	log.SetOutput(&logOutput)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	ios, _, _, _ := cmdtest.TestIOStreams()
	configureColorMode(ios, config.NewFromString("color: bogus"))

	assert.Contains(t, logOutput.String(), "bogus", "an unrecognized color value should be logged via internal/dbg, not eaten silently")

	logOutput.Reset()
	ios2, _, _, _ := cmdtest.TestIOStreams()
	configureColorMode(ios2, config.NewFromString(""))
	assert.Empty(t, logOutput.String(), "an unset color value is not an error and must not log anything")
}

func TestFangColorEnv_Apply(t *testing.T) {
	tests := []struct {
		name         string
		mode         iostreams.ColorMode
		noColor      string
		cliForce     string
		wantNoColor  string
		wantCLIForce string
	}{
		{name: "never disables color for fang", mode: iostreams.ColorModeNever, wantNoColor: "1"},
		{name: "never drops CLICOLOR_FORCE, which wins on a non-TTY", mode: iostreams.ColorModeNever, cliForce: "1", wantNoColor: "1"},
		{name: "always forces color for fang", mode: iostreams.ColorModeAlways, wantCLIForce: "1"},
		{name: "always overrides NO_COLOR", mode: iostreams.ColorModeAlways, noColor: "1", wantCLIForce: "1"},
		{name: "auto leaves the environment alone", mode: iostreams.ColorModeAuto, noColor: "1", cliForce: "0", wantNoColor: "1", wantCLIForce: "0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setColorEnv(t, tt.noColor, tt.cliForce)

			newFangColorEnv(tt.mode).apply()

			gotNoColor, hasNoColor := os.LookupEnv("NO_COLOR")
			gotForce, hasForce := os.LookupEnv("CLICOLOR_FORCE")
			assert.Equal(t, tt.wantNoColor != "", hasNoColor)
			assert.Equal(t, tt.wantNoColor, gotNoColor)
			assert.Equal(t, tt.wantCLIForce != "", hasForce)
			assert.Equal(t, tt.wantCLIForce, gotForce)
		})
	}
}

func TestFangColorEnv_RestoreBringsBackTheOriginalEnvironment(t *testing.T) {
	setColorEnv(t, "yes", "")

	e := newFangColorEnv(iostreams.ColorModeAlways)
	e.apply()
	e.restore()

	got, ok := os.LookupEnv("NO_COLOR")
	assert.True(t, ok)
	assert.Equal(t, "yes", got)
	_, ok = os.LookupEnv("CLICOLOR_FORCE")
	assert.False(t, ok, "a variable that was unset stays unset")
}

// Children that glab starts (git, pagers, editors, alias shells) must not
// inherit the variables meant for fang's rendering.
func TestScopeColorEnvToFang_CommandsRunWithTheOriginalEnvironment(t *testing.T) {
	setColorEnv(t, "", "")

	var duringRun, duringRunE []string
	root := &cobra.Command{Use: "glab"}
	root.AddCommand(&cobra.Command{Use: "run", Run: func(*cobra.Command, []string) { duringRun = colorEnvSnapshot() }})
	nested := &cobra.Command{Use: "group"}
	nested.AddCommand(&cobra.Command{Use: "rune", RunE: func(*cobra.Command, []string) error {
		duringRunE = colorEnvSnapshot()
		return errors.New("boom")
	}})
	root.AddCommand(nested)

	e := newFangColorEnv(iostreams.ColorModeAlways)
	e.apply()
	scopeColorEnvToFang(root, e)

	root.SetArgs([]string{"run"})
	require.NoError(t, root.Execute())
	assert.Empty(t, duringRun, "Run sees the original environment")
	assert.Equal(t, []string{"CLICOLOR_FORCE=1"}, colorEnvSnapshot(), "fang's error rendering sees the exported value again afterwards")

	root.SetArgs([]string{"group", "rune"})
	root.SilenceErrors, root.SilenceUsage = true, true
	require.Error(t, root.Execute())
	assert.Empty(t, duringRunE, "nested RunE sees the original environment")
	assert.Equal(t, []string{"CLICOLOR_FORCE=1"}, colorEnvSnapshot(), "re-applied even when the command fails")
}

func setColorEnv(t *testing.T, noColor, cliForce string) {
	t.Helper()
	t.Setenv("NO_COLOR", noColor)
	t.Setenv("CLICOLOR_FORCE", cliForce)
	if noColor == "" {
		os.Unsetenv("NO_COLOR")
	}
	if cliForce == "" {
		os.Unsetenv("CLICOLOR_FORCE")
	}
}

func colorEnvSnapshot() []string {
	var out []string
	for _, k := range []string{"NO_COLOR", "CLICOLOR_FORCE"} {
		if v, ok := os.LookupEnv(k); ok {
			out = append(out, k+"="+v)
		}
	}
	return out
}
