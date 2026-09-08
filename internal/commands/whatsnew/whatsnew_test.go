//go:build !integration

package whatsnew

import (
	"net/http"
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

// Tests that exercise the API client install per-test mocks via this
// helper; they can't run with t.Parallel() because the package-level
// clientCreator var is mutated for the duration of each test (same
// pattern as internal/commands/update/check_update_test.go).
func stubClientCreator(t *testing.T, testClient *gitlabtesting.TestClient) {
	t.Helper()
	old := clientCreator
	clientCreator = func(userAgent string, options ...api.ClientOption) (*api.Client, error) {
		return api.NewClient(
			func(*http.Client) (gitlab.AuthSource, error) {
				return gitlab.AccessTokenAuthSource{Token: ""}, nil
			},
			api.WithGitLabClient(testClient.Client),
		)
	}
	t.Cleanup(func() { clientCreator = old })
}

func release(tag, description string) *gitlab.Release {
	return &gitlab.Release{TagName: tag, Name: tag, Description: description}
}

func TestWhatsnew_specificVersion(t *testing.T) {
	tc := gitlabtesting.NewTestClient(t)
	tc.MockReleases.EXPECT().
		GetRelease("gitlab-org/cli", "v1.85.0", gomock.Any()).
		Return(release("v1.85.0", "## Highlights\n\n- thing one"), nil, nil)
	stubClientCreator(t, tc)

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithBuildInfo(api.BuildInfo{Version: "1.85.0"}),
	)
	out, err := exec("v1.85.0")
	require.NoError(t, err)
	stripped := stripansi.Strip(out.String())
	assert.Contains(t, stripped, "## v1.85.0")
	assert.Contains(t, stripped, "thing one")
}

func TestWhatsnew_versionArgWithoutVPrefix(t *testing.T) {
	tc := gitlabtesting.NewTestClient(t)
	tc.MockReleases.EXPECT().
		GetRelease("gitlab-org/cli", "v1.85.0", gomock.Any()).
		Return(release("v1.85.0", "notes"), nil, nil)
	stubClientCreator(t, tc)

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithBuildInfo(api.BuildInfo{Version: "1.85.0"}),
	)
	_, err := exec("1.85.0")
	require.NoError(t, err)
}

func TestWhatsnew_latestFlag(t *testing.T) {
	tc := gitlabtesting.NewTestClient(t)
	tc.MockReleases.EXPECT().
		ListReleases("gitlab-org/cli", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ any, opts *gitlab.ListReleasesOptions, _ ...gitlab.RequestOptionFunc) ([]*gitlab.Release, *gitlab.Response, error) {
			assert.Equal(t, int64(1), opts.PerPage)
			return []*gitlab.Release{release("v1.85.0", "latest notes")}, nil, nil
		})
	stubClientCreator(t, tc)

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithBuildInfo(api.BuildInfo{Version: "1.80.0"}),
	)
	out, err := exec("--latest")
	require.NoError(t, err)
	stripped := stripansi.Strip(out.String())
	assert.Contains(t, stripped, "v1.85.0")
	assert.Contains(t, stripped, "latest notes")
}

func TestWhatsnew_sinceFlagFiltersReleases(t *testing.T) {
	tc := gitlabtesting.NewTestClient(t)
	tc.MockReleases.EXPECT().
		ListReleases("gitlab-org/cli", gomock.Any(), gomock.Any()).
		Return([]*gitlab.Release{
			release("v1.85.0", "newest"),
			release("v1.84.0", "middle"),
			release("v1.83.0", "skipped"),
			release("v1.82.0", "also skipped"),
		}, nil, nil)
	stubClientCreator(t, tc)

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithBuildInfo(api.BuildInfo{Version: "1.85.0"}),
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
	tc := gitlabtesting.NewTestClient(t)
	tc.MockReleases.EXPECT().
		ListReleases("gitlab-org/cli", gomock.Any(), gomock.Any()).
		Return([]*gitlab.Release{
			release("v1.85.0", "newest"),
			release("v1.84.0", "skipped"),
		}, nil, nil)
	stubClientCreator(t, tc)

	cfg := newTestConfig(t, map[string]string{LastWhatsnewVersionKey: "v1.84.0"})

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithBuildInfo(api.BuildInfo{Version: "1.85.0"}),
		cmdtest.WithConfig(cfg),
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
	tc := gitlabtesting.NewTestClient(t)
	tc.MockReleases.EXPECT().
		ListReleases("gitlab-org/cli", gomock.Any(), gomock.Any()).
		Return([]*gitlab.Release{release("v1.85.0", "latest")}, nil, nil)
	stubClientCreator(t, tc)

	cfg := newTestConfig(t, map[string]string{LastWhatsnewVersionKey: "v1.80.0"})

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithBuildInfo(api.BuildInfo{Version: "1.85.0"}),
		cmdtest.WithConfig(cfg),
	)
	_, err := exec("--latest")
	require.NoError(t, err)

	got, _ := cfg.Get("", LastWhatsnewVersionKey)
	assert.Equal(t, "v1.80.0", got)
}

func TestWhatsnew_noNewReleases(t *testing.T) {
	tc := gitlabtesting.NewTestClient(t)
	tc.MockReleases.EXPECT().
		ListReleases("gitlab-org/cli", gomock.Any(), gomock.Any()).
		Return([]*gitlab.Release{release("v1.85.0", "")}, nil, nil)
	stubClientCreator(t, tc)

	cfg := newTestConfig(t, map[string]string{LastWhatsnewVersionKey: "v1.85.0"})

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithBuildInfo(api.BuildInfo{Version: "1.85.0"}),
		cmdtest.WithConfig(cfg),
	)
	out, err := exec("")
	require.NoError(t, err)
	assert.Contains(t, out.Stderr(), "No new releases")
}

func TestWhatsnew_emptyReleaseDescription(t *testing.T) {
	tc := gitlabtesting.NewTestClient(t)
	tc.MockReleases.EXPECT().
		GetRelease("gitlab-org/cli", "v1.85.0", gomock.Any()).
		Return(release("v1.85.0", ""), nil, nil)
	stubClientCreator(t, tc)

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithBuildInfo(api.BuildInfo{Version: "1.85.0"}),
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

	cfg := newTestConfig(t, nil)

	bannerIO, _, _, bannerErr := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
	bannerBuild := api.BuildInfo{Version: currentVersion}
	update.MaybeShowPostUpgradeBanner(bannerIO, cfg, bannerBuild)
	require.Contains(t, bannerErr.String(), "glab whatsnew",
		"precondition: the banner must nudge the user to run whatsnew")

	tc := gitlabtesting.NewTestClient(t)
	tc.MockReleases.EXPECT().
		ListReleases("gitlab-org/cli", gomock.Any(), gomock.Any()).
		Return([]*gitlab.Release{release("v1.101.0", "## Highlights\n\n- the feature")}, nil, nil)
	stubClientCreator(t, tc)

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false,
		cmdtest.WithBuildInfo(api.BuildInfo{Version: currentVersion}),
		cmdtest.WithConfig(cfg),
	)
	out, err := exec("")
	require.NoError(t, err)

	stripped := stripansi.Strip(out.String())
	assert.Contains(t, stripped, "v1.101.0",
		"following the banner's nudge should show the release it advertised")
	assert.NotContains(t, out.Stderr(), "No new releases",
		"the banner nudged the user here; reporting nothing new defeats the feature")
}
