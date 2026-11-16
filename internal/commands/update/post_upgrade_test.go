//go:build !integration

package update

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func newBannerTest(t *testing.T, currentVersion, lastSeen, showWhatsNew, codingAgent string) (*iostreams.IOStreams, config.Config, api.BuildInfo, *bytes.Buffer) {
	t.Helper()
	cfg := config.NewBlankConfig()
	if lastSeen != "" {
		require.NoError(t, cfg.Set("", LastSeenVersionKey, lastSeen))
	}
	if showWhatsNew != "" {
		require.NoError(t, cfg.Set("", "show_whats_new", showWhatsNew))
	}
	ios, _, _, stderr := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))
	return ios, cfg, api.BuildInfo{Version: currentVersion, CodingAgent: codingAgent}, stderr
}

func TestMaybeShowPostUpgradeBanner(t *testing.T) {
	t.Parallel()
	stubNoInstalledSkills(t)

	tests := []struct {
		name           string
		currentVersion string
		lastSeen       string
		showWhatsNew   string
		codingAgent    string
		stderrNotTTY   bool
		wantBanner     bool
		wantLastSeen   string
	}{
		{
			name:           "no stored value, current newer than seeded default fires banner",
			currentVersion: "1.101.0",
			lastSeen:       "",
			wantBanner:     true,
			wantLastSeen:   "1.101.0",
		},
		{
			name:           "no stored value, current at seeded default is silent",
			currentVersion: "1.100.0",
			lastSeen:       "",
			wantBanner:     false,
			wantLastSeen:   "v1.100.0", // defaultFor() value
		},
		{
			name:           "stored value older than current fires banner and updates marker",
			currentVersion: "1.85.0",
			lastSeen:       "1.84.0",
			wantBanner:     true,
			wantLastSeen:   "1.85.0",
		},
		{
			name:           "same version is silent",
			currentVersion: "1.85.0",
			lastSeen:       "1.85.0",
			wantBanner:     false,
			wantLastSeen:   "1.85.0",
		},
		{
			name:           "downgrade is silent",
			currentVersion: "1.84.0",
			lastSeen:       "1.85.0",
			wantBanner:     false,
			wantLastSeen:   "1.85.0",
		},
		{
			name:           "config opt-out suppresses banner",
			currentVersion: "1.85.0",
			lastSeen:       "1.84.0",
			showWhatsNew:   "false",
			wantBanner:     false,
			wantLastSeen:   "1.84.0",
		},
		{
			name:           "coding agent is silent",
			currentVersion: "1.85.0",
			lastSeen:       "1.84.0",
			codingAgent:    "claude-code",
			wantBanner:     false,
			wantLastSeen:   "1.84.0",
		},
		{
			name:           "stderr not a terminal is silent",
			currentVersion: "1.85.0",
			lastSeen:       "1.84.0",
			stderrNotTTY:   true,
			wantBanner:     false,
			wantLastSeen:   "1.84.0",
		},
		{
			name:           "unparseable current version is silent",
			currentVersion: "DEV",
			lastSeen:       "1.84.0",
			wantBanner:     false,
			wantLastSeen:   "1.84.0",
		},
		{
			name:           "unparseable last_seen is silent",
			currentVersion: "1.85.0",
			lastSeen:       "garbage",
			wantBanner:     false,
			wantLastSeen:   "garbage",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ios, cfg, buildInfo, stderr := newBannerTest(t, tc.currentVersion, tc.lastSeen, tc.showWhatsNew, tc.codingAgent)
			ios.IsErrTTY = !tc.stderrNotTTY
			maybeShowPostUpgradeBanner(ios, cfg, buildInfo, false)

			if tc.wantBanner {
				assert.Contains(t, stderr.String(), "What's new in glab")
				assert.Contains(t, stderr.String(), tc.currentVersion)
				assert.Contains(t, stderr.String(), "glab whatsnew")
			} else {
				assert.Empty(t, stderr.String())
			}

			got, _ := cfg.Get("", LastSeenVersionKey)
			assert.Equal(t, tc.wantLastSeen, got)
		})
	}
}

// envOverride covers GLAB_SHOW_WHATS_NEW separately because t.Setenv is
// incompatible with t.Parallel.
func TestMaybeShowPostUpgradeBanner_envOverride(t *testing.T) {
	stubNoInstalledSkills(t)
	t.Setenv("GLAB_SHOW_WHATS_NEW", "false")

	ios, cfg, buildInfo, stderr := newBannerTest(t, "1.85.0", "1.84.0", "", "")
	maybeShowPostUpgradeBanner(ios, cfg, buildInfo, false)

	assert.Empty(t, stderr.String())
	got, _ := cfg.Get("", LastSeenVersionKey)
	assert.Equal(t, "1.84.0", got)
}

func TestMaybeShowPostUpgradeBanner_ci(t *testing.T) {
	stubNoInstalledSkills(t)

	tests := []struct {
		name         string
		ci           string
		gitlabCI     string
		wantBanner   bool
		wantLastSeen string
	}{
		{name: "CI set is silent", ci: "true", wantLastSeen: "1.84.0"},
		{name: "GITLAB_CI set is silent", gitlabCI: "true", wantLastSeen: "1.84.0"},
		{name: "outside CI fires banner", wantBanner: true, wantLastSeen: "1.85.0"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CI", tc.ci)
			t.Setenv("GITLAB_CI", tc.gitlabCI)

			ios, cfg, buildInfo, stderr := newBannerTest(t, "1.85.0", "1.84.0", "", "")
			MaybeShowPostUpgradeBanner(ios, cfg, buildInfo)

			if tc.wantBanner {
				assert.Contains(t, stderr.String(), "What's new in glab 1.85.0")
			} else {
				assert.Empty(t, stderr.String())
			}
			got, _ := cfg.Get("", LastSeenVersionKey)
			assert.Equal(t, tc.wantLastSeen, got)
		})
	}
}
