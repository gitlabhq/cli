//go:build !integration

package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/zalando/go-keyring"
	"go.uber.org/goleak"

	"gitlab.com/gitlab-org/cli/internal/config"
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
