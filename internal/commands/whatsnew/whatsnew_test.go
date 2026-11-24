//go:build !integration

package whatsnew

import (
	"testing"

	"github.com/acarl005/stripansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/commands/update"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func newTestConfig(t *testing.T, kvs map[string]string) config.Config {
	t.Helper()
	cfg := config.NewBlankConfig()
	for k, v := range kvs {
		require.NoError(t, cfg.Set("", k, v))
	}
	return cfg
}

func mockGitLabComClient(t *testing.T, tc *gitlabtesting.TestClient) cmdtest.FactoryOption {
	t.Helper()
	return cmdtest.WithGitLabComClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client)))
}

func release(tag, description string) *gitlab.Release {
	return &gitlab.Release{TagName: tag, Name: tag, Description: description}
}

func TestWhatsnew_specificVersion(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockReleases.EXPECT().
		GetRelease("gitlab-org/cli", "v1.85.0", gomock.Any()).
		Return(release("v1.85.0", "## Highlights\n\n- thing one"), nil, nil)

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithBuildInfo(api.BuildInfo{Version: "1.85.0"}),
		mockGitLabComClient(t, tc),
	)
	out, err := exec("v1.85.0")
	require.NoError(t, err)
	stripped := stripansi.Strip(out.String())
	assert.Contains(t, stripped, "## v1.85.0")
	assert.Contains(t, stripped, "thing one")
}

func TestWhatsnew_versionArgWithoutVPrefix(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockReleases.EXPECT().
		GetRelease("gitlab-org/cli", "v1.85.0", gomock.Any()).
		Return(release("v1.85.0", "notes"), nil, nil)

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithBuildInfo(api.BuildInfo{Version: "1.85.0"}),
		mockGitLabComClient(t, tc),
	)
	_, err := exec("1.85.0")
	require.NoError(t, err)
}

func TestWhatsnew_latestFlag(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockReleases.EXPECT().
		ListReleases("gitlab-org/cli", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ any, opts *gitlab.ListReleasesOptions, _ ...gitlab.RequestOptionFunc) ([]*gitlab.Release, *gitlab.Response, error) {
			assert.Equal(t, int64(1), opts.PerPage)
			return []*gitlab.Release{release("v1.85.0", "latest notes")}, nil, nil
		})

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithBuildInfo(api.BuildInfo{Version: "1.80.0"}),
		mockGitLabComClient(t, tc),
	)
	out, err := exec("--latest")
	require.NoError(t, err)
	stripped := stripansi.Strip(out.String())
	assert.Contains(t, stripped, "v1.85.0")
	assert.Contains(t, stripped, "latest notes")
}

func TestWhatsnew_sinceFlagFiltersReleases(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockReleases.EXPECT().
		ListReleases("gitlab-org/cli", gomock.Any(), gomock.Any()).
		Return([]*gitlab.Release{
			release("v1.85.0", "newest"),
			release("v1.84.0", "middle"),
			release("v1.83.0", "skipped"),
			release("v1.82.0", "also skipped"),
		}, nil, nil)

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithBuildInfo(api.BuildInfo{Version: "1.85.0"}),
		mockGitLabComClient(t, tc),
	)
	out, err := exec("--since v1.83.0")
	require.NoError(t, err)
	stripped := stripansi.Strip(out.String())
	assert.Contains(t, stripped, "v1.85.0")
	assert.Contains(t, stripped, "v1.84.0")
	assert.NotContains(t, stripped, "v1.83.0")
	assert.NotContains(t, stripped, "v1.82.0")
}

func TestWhatsnew_defaultInvocationUsesLastWhatsnewAndAdvancesMarker(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockReleases.EXPECT().
		ListReleases("gitlab-org/cli", gomock.Any(), gomock.Any()).
		Return([]*gitlab.Release{
			release("v1.85.0", "newest"),
			release("v1.84.0", "skipped"),
		}, nil, nil)

	cfg := newTestConfig(t, map[string]string{LastWhatsnewVersionKey: "v1.84.0"})

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithBuildInfo(api.BuildInfo{Version: "1.85.0"}),
		cmdtest.WithConfig(cfg),
		mockGitLabComClient(t, tc),
	)
	out, err := exec("")
	require.NoError(t, err)
	stripped := stripansi.Strip(out.String())
	assert.Contains(t, stripped, "v1.85.0")
	assert.NotContains(t, stripped, "v1.84.0")

	got, _ := cfg.Get("", LastWhatsnewVersionKey)
	assert.Equal(t, "1.85.0", got)
}

func TestWhatsnew_explicitInvocationDoesNotAdvanceMarker(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockReleases.EXPECT().
		ListReleases("gitlab-org/cli", gomock.Any(), gomock.Any()).
		Return([]*gitlab.Release{release("v1.85.0", "latest")}, nil, nil)

	cfg := newTestConfig(t, map[string]string{LastWhatsnewVersionKey: "v1.80.0"})

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithBuildInfo(api.BuildInfo{Version: "1.85.0"}),
		cmdtest.WithConfig(cfg),
		mockGitLabComClient(t, tc),
	)
	_, err := exec("--latest")
	require.NoError(t, err)

	got, _ := cfg.Get("", LastWhatsnewVersionKey)
	assert.Equal(t, "v1.80.0", got)
}

func TestWhatsnew_noNewReleases(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockReleases.EXPECT().
		ListReleases("gitlab-org/cli", gomock.Any(), gomock.Any()).
		Return([]*gitlab.Release{release("v1.85.0", "")}, nil, nil)

	cfg := newTestConfig(t, map[string]string{LastWhatsnewVersionKey: "v1.85.0"})

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithBuildInfo(api.BuildInfo{Version: "1.85.0"}),
		cmdtest.WithConfig(cfg),
		mockGitLabComClient(t, tc),
	)
	out, err := exec("")
	require.NoError(t, err)
	assert.Contains(t, out.Stderr(), "No new releases")
}

func TestWhatsnew_emptyReleaseDescription(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockReleases.EXPECT().
		GetRelease("gitlab-org/cli", "v1.85.0", gomock.Any()).
		Return(release("v1.85.0", ""), nil, nil)

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithBuildInfo(api.BuildInfo{Version: "1.85.0"}),
		mockGitLabComClient(t, tc),
	)
	out, err := exec("v1.85.0")
	require.NoError(t, err)
	assert.Contains(t, out.String(), "(no release notes)")
}

func TestWhatsnew_flagConflicts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args string
	}{
		{name: "positional with --latest", args: "v1.85.0 --latest"},
		{name: "positional with --since", args: "v1.85.0 --since v1.80.0"},
		{name: "--since with --latest", args: "--since v1.80.0 --latest"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
				cmdtest.WithBuildInfo(api.BuildInfo{Version: "1.85.0"}),
			)
			_, err := exec(tt.args)
			require.Error(t, err)
		})
	}
}

// Regression: the post-upgrade banner and the default `whatsnew` view used
// to share last_seen_version. The banner advanced the marker to the current
// version, then `whatsnew` filtered releases strictly greater than that
// marker — so following the banner's nudge produced "No new releases."
// The fix is the separate LastWhatsnewVersionKey marker.
func TestWhatsnew_bannerNudgeThenWhatsnew(t *testing.T) {
	const currentVersion = "1.101.0"

	t.Setenv("CI", "")
	t.Setenv("GITLAB_CI", "")
	cfg := newTestConfig(t, nil)

	bannerIO, _, _, bannerErr := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))
	bannerBuild := api.BuildInfo{Version: currentVersion}
	update.MaybeShowPostUpgradeBanner(bannerIO, cfg, bannerBuild)
	require.Contains(t, bannerErr.String(), "glab whatsnew",
		"precondition: the banner must nudge the user to run whatsnew")

	tc := gitlabtesting.NewTestClient(t)
	tc.MockReleases.EXPECT().
		ListReleases("gitlab-org/cli", gomock.Any(), gomock.Any()).
		Return([]*gitlab.Release{release("v1.101.0", "## Highlights\n\n- the feature")}, nil, nil)

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithBuildInfo(api.BuildInfo{Version: currentVersion}),
		cmdtest.WithConfig(cfg),
		mockGitLabComClient(t, tc),
	)
	out, err := exec("")
	require.NoError(t, err)

	stripped := stripansi.Strip(out.String())
	assert.Contains(t, stripped, "v1.101.0",
		"following the banner's nudge should show the release it advertised")
	assert.NotContains(t, out.Stderr(), "No new releases",
		"the banner nudged the user here; reporting nothing new defeats the feature")
}
