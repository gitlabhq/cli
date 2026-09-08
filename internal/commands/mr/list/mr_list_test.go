//go:build !integration

package list

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestNewCmdList(t *testing.T) {
	ios, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))

	// No API calls are made in this test since we provide a custom runFunc
	factory := cmdtest.NewTestFactory(ios,
		cmdtest.WithConfig(config.NewBlankConfig()),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)
	t.Run("MergeRequest_NewCmdList", func(t *testing.T) {
		gotOpts := &options{}
		err := NewCmdList(factory, func(opts *options) error {
			gotOpts = opts
			return nil
		}).Execute()

		require.NoError(t, err)
		assert.Equal(t, factory.IO(), gotOpts.io)

		gotBaseRepo, _ := gotOpts.baseRepo()
		expectedBaseRepo, _ := factory.BaseRepo()
		assert.Equal(t, expectedBaseRepo, gotBaseRepo)
	})
}

func TestMergeRequestList_tty(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockMergeRequests.EXPECT().
		ListProjectMergeRequests("OWNER/REPO", gomock.Any()).
		Return([]*gitlab.BasicMergeRequest{
			{
				ID:           76,
				IID:          6,
				ProjectID:    1,
				State:        "opened",
				Title:        "MergeRequest one",
				Description:  "a description here",
				TargetBranch: "master",
				SourceBranch: "test1",
				Labels:       gitlab.Labels{"foo", "bar"},
				WebURL:       "http://gitlab.com/OWNER/REPO/merge_requests/6",
				References: &gitlab.IssueReferences{
					Full:     "OWNER/REPO/merge_requests/6",
					Relative: "#6",
					Short:    "#6",
				},
			},
			{
				ID:           77,
				IID:          7,
				ProjectID:    1,
				State:        "opened",
				Title:        "MergeRequest two",
				Description:  "description two here",
				TargetBranch: "master",
				SourceBranch: "test2",
				Labels:       gitlab.Labels{"fooz", "baz"},
				WebURL:       "http://gitlab.com/OWNER/REPO/merge_requests/7",
				References: &gitlab.IssueReferences{
					Full:     "OWNER/REPO/merge_requests/7",
					Relative: "#7",
					Short:    "#7",
				},
			},
		}, nil, nil)

	// Create an api.Client with the mock GitLab client
	apiClient, err := api.NewClient(
		func(*http.Client) (gitlab.AuthSource, error) {
			return gitlab.AccessTokenAuthSource{Token: "test-token"}, nil
		},
		api.WithGitLabClient(testClient.Client),
	)
	require.NoError(t, err)

	exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
		return NewCmdList(f, nil)
	}, true,
		cmdtest.WithApiClient(apiClient),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	output, err := exec("")
	if err != nil {
		t.Errorf("error running command `mr list`: %v", err)
	}

	assert.Equal(t, heredoc.Doc(`
		Showing 2 open merge requests on OWNER/REPO. (Page 1)

		!6	OWNER/REPO/merge_requests/6	MergeRequest one	(master) ← (test1)
		!7	OWNER/REPO/merge_requests/7	MergeRequest two	(master) ← (test2)

	`), output.String())
	assert.Empty(t, output.Stderr())
}

func TestMergeRequestList_tty_withFlags(t *testing.T) {
	// NOTE: This test cannot use t.Parallel() because it uses t.Setenv().
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	t.Run("repo", func(t *testing.T) {
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockUsers.EXPECT().
			ListUsers(gomock.Any()).
			Return([]*gitlab.User{
				{ID: 1, Username: "someuser"},
			}, nil, nil)

		testClient.MockMergeRequests.EXPECT().
			ListProjectMergeRequests("OWNER/REPO", gomock.Any()).
			DoAndReturn(func(pid any, opts *gitlab.ListProjectMergeRequestsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.BasicMergeRequest, *gitlab.Response, error) {
				// Verify flags are passed correctly
				// Note: -p is Page, -P is PerPage, so "-P1 -p100" means PerPage=1, Page=100
				assert.Equal(t, "opened", *opts.State)
				assert.Equal(t, int64(100), opts.Page)
				assert.Equal(t, int64(1), opts.PerPage)
				assert.NotNil(t, opts.AssigneeID) // User ID 1 from someuser lookup
				assert.Equal(t, gitlab.LabelOptions{"bug"}, *opts.Labels)
				assert.Equal(t, "1", *opts.Milestone)
				return []*gitlab.BasicMergeRequest{}, nil, nil
			})

		apiClient, err := api.NewClient(
			func(*http.Client) (gitlab.AuthSource, error) {
				return gitlab.AccessTokenAuthSource{Token: "test-token"}, nil
			},
			api.WithGitLabClient(testClient.Client),
		)
		require.NoError(t, err)

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdList(f, nil)
		}, true,
			cmdtest.WithApiClient(apiClient),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		output, err := exec("--opened -P1 -p100 -a someuser -l bug -m1")
		require.NoError(t, err)

		assert.Empty(t, output.Stderr())
		assert.Equal(t, `No open merge requests match your search in OWNER/REPO.


`, output.String())
	})
	t.Run("group", func(t *testing.T) {
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			ListGroupMergeRequests("GROUP", gomock.Any()).
			Return([]*gitlab.BasicMergeRequest{}, nil, nil)

		apiClient, err := api.NewClient(
			func(*http.Client) (gitlab.AuthSource, error) {
				return gitlab.AccessTokenAuthSource{Token: "test-token"}, nil
			},
			api.WithGitLabClient(testClient.Client),
		)
		require.NoError(t, err)

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdList(f, nil)
		}, true,
			cmdtest.WithApiClient(apiClient),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		output, err := exec("--group GROUP")
		require.NoError(t, err)

		assert.Empty(t, output.Stderr())
		assert.Equal(t, `No open merge requests available on GROUP.

`, output.String())
	})

	t.Run("draft", func(t *testing.T) {
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			ListProjectMergeRequests("OWNER/REPO", gomock.Any()).
			DoAndReturn(func(pid any, opts *gitlab.ListProjectMergeRequestsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.BasicMergeRequest, *gitlab.Response, error) {
				// Verify draft filter is passed
				assert.Equal(t, "yes", *opts.WIP)
				return []*gitlab.BasicMergeRequest{
					{
						ID:           76,
						IID:          6,
						ProjectID:    1,
						State:        "opened",
						Title:        "MergeRequest one",
						Draft:        true,
						TargetBranch: "master",
						SourceBranch: "test1",
						Labels:       gitlab.Labels{"foo", "bar"},
						WebURL:       "http://gitlab.com/OWNER/REPO/merge_requests/6",
						References: &gitlab.IssueReferences{
							Full:     "OWNER/REPO/merge_requests/6",
							Relative: "#6",
							Short:    "#6",
						},
					},
					{
						ID:           77,
						IID:          7,
						ProjectID:    1,
						State:        "opened",
						Title:        "MergeRequest two",
						Draft:        true,
						TargetBranch: "master",
						SourceBranch: "test2",
						Labels:       gitlab.Labels{"fooz", "baz"},
						WebURL:       "http://gitlab.com/OWNER/REPO/merge_requests/7",
						References: &gitlab.IssueReferences{
							Full:     "OWNER/REPO/merge_requests/7",
							Relative: "#7",
							Short:    "#7",
						},
					},
				}, nil, nil
			})

		apiClient, err := api.NewClient(
			func(*http.Client) (gitlab.AuthSource, error) {
				return gitlab.AccessTokenAuthSource{Token: "test-token"}, nil
			},
			api.WithGitLabClient(testClient.Client),
		)
		require.NoError(t, err)

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdList(f, nil)
		}, true,
			cmdtest.WithApiClient(apiClient),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		output, err := exec("--draft")
		require.NoError(t, err)

		assert.Empty(t, output.Stderr())
		assert.Equal(t, heredoc.Doc(`
		Showing 2 open merge requests in OWNER/REPO that match your search. (Page 1)

		!6	OWNER/REPO/merge_requests/6	MergeRequest one	(master) ← (test1)
		!7	OWNER/REPO/merge_requests/7	MergeRequest two	(master) ← (test2)

	`), output.String())
	})

	t.Run("not draft", func(t *testing.T) {
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			ListProjectMergeRequests("OWNER/REPO", gomock.Any()).
			DoAndReturn(func(pid any, opts *gitlab.ListProjectMergeRequestsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.BasicMergeRequest, *gitlab.Response, error) {
				// Verify not-draft filter is passed
				assert.Equal(t, "no", *opts.WIP)
				return []*gitlab.BasicMergeRequest{}, nil, nil
			})

		apiClient, err := api.NewClient(
			func(*http.Client) (gitlab.AuthSource, error) {
				return gitlab.AccessTokenAuthSource{Token: "test-token"}, nil
			},
			api.WithGitLabClient(testClient.Client),
		)
		require.NoError(t, err)

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdList(f, nil)
		}, true,
			cmdtest.WithApiClient(apiClient),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		output, err := exec("--not-draft")
		require.NoError(t, err)

		assert.Empty(t, output.Stderr())
		assert.Equal(t, "No open merge requests match your search in OWNER/REPO.\n\n\n", output.String())
	})
}

func makeHyperlink(linkText, targetURL string) string {
	return fmt.Sprintf("\x1b]8;;%s\x1b\\%s\x1b]8;;\x1b\\", targetURL, linkText)
}

func TestMergeRequestList_hyperlinks(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	noHyperlinkCells := [][]string{
		{"!6", "OWNER/REPO/merge_requests/6", "MergeRequest one", "(master) ← (test1)"},
		{"!7", "OWNER/REPO/merge_requests/7", "MergeRequest two", "(master) ← (test2)"},
	}

	hyperlinkCells := [][]string{
		{makeHyperlink("!6", "http://gitlab.com/OWNER/REPO/merge_requests/6"), "OWNER/REPO/merge_requests/6", "MergeRequest one", "(master) ← (test1)"},
		{makeHyperlink("!7", "http://gitlab.com/OWNER/REPO/merge_requests/7"), "OWNER/REPO/merge_requests/7", "MergeRequest two", "(master) ← (test2)"},
	}

	type hyperlinkTest struct {
		forceHyperlinksEnv      string
		displayHyperlinksConfig string
		isTTY                   bool

		expectedCells [][]string
	}

	tests := []hyperlinkTest{
		// FORCE_HYPERLINKS causes hyperlinks to be output, whether or not we're talking to a TTY
		{forceHyperlinksEnv: "1", isTTY: true, expectedCells: hyperlinkCells},
		{forceHyperlinksEnv: "1", isTTY: false, expectedCells: hyperlinkCells},

		// empty/missing display_hyperlinks in config defaults to *not* outputting hyperlinks
		{displayHyperlinksConfig: "", isTTY: true, expectedCells: noHyperlinkCells},
		{displayHyperlinksConfig: "", isTTY: false, expectedCells: noHyperlinkCells},

		// display_hyperlinks: false in config prevents outputting hyperlinks
		{displayHyperlinksConfig: "false", isTTY: true, expectedCells: noHyperlinkCells},
		{displayHyperlinksConfig: "false", isTTY: false, expectedCells: noHyperlinkCells},

		// display_hyperlinks: true in config only outputs hyperlinks if we're talking to a TTY
		{displayHyperlinksConfig: "true", isTTY: true, expectedCells: hyperlinkCells},
		{displayHyperlinksConfig: "true", isTTY: false, expectedCells: noHyperlinkCells},
	}

	testMRs := []*gitlab.BasicMergeRequest{
		{
			ID:           76,
			IID:          6,
			ProjectID:    1,
			State:        "opened",
			Title:        "MergeRequest one",
			TargetBranch: "master",
			SourceBranch: "test1",
			Labels:       gitlab.Labels{"foo", "bar"},
			WebURL:       "http://gitlab.com/OWNER/REPO/merge_requests/6",
			References: &gitlab.IssueReferences{
				Full:     "OWNER/REPO/merge_requests/6",
				Relative: "#6",
				Short:    "#6",
			},
		},
		{
			ID:           77,
			IID:          7,
			ProjectID:    1,
			State:        "opened",
			Title:        "MergeRequest two",
			TargetBranch: "master",
			SourceBranch: "test2",
			Labels:       gitlab.Labels{"fooz", "baz"},
			WebURL:       "http://gitlab.com/OWNER/REPO/merge_requests/7",
			References: &gitlab.IssueReferences{
				Full:     "OWNER/REPO/merge_requests/7",
				Relative: "#7",
				Short:    "#7",
			},
		},
	}

	for _, tc := range tests {
		t.Run("", func(t *testing.T) {
			testClient := gitlabtesting.NewTestClient(t)

			testClient.MockMergeRequests.EXPECT().
				ListProjectMergeRequests("OWNER/REPO", gomock.Any()).
				Return(testMRs, nil, nil)

			apiClient, err := api.NewClient(
				func(*http.Client) (gitlab.AuthSource, error) {
					return gitlab.AccessTokenAuthSource{Token: "test-token"}, nil
				},
				api.WithGitLabClient(testClient.Client),
			)
			require.NoError(t, err)

			doHyperlinks := "never"
			if tc.forceHyperlinksEnv == "1" {
				doHyperlinks = "always"
			} else if tc.displayHyperlinksConfig == "true" {
				doHyperlinks = "auto"
			}

			ios, _, _, _ := cmdtest.TestIOStreams(
				cmdtest.WithTestIOStreamsAsTTY(tc.isTTY),
				iostreams.WithDisplayHyperLinks(doHyperlinks),
			)

			exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
				return NewCmdList(f, nil)
			}, tc.isTTY,
				cmdtest.WithIOStreamsOverride(ios),
				cmdtest.WithApiClient(apiClient),
				cmdtest.WithGitLabClient(testClient.Client),
				cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			)

			output, err := exec("")
			require.NoError(t, err)

			out := output.String()

			lines := strings.Split(strings.TrimRight(out, "\n"), "\n")

			// first two lines have the header and some separating whitespace, so skip those
			for lineNum, line := range lines[2:] {
				gotCells := strings.Split(line, "\t")
				expectedCells := tc.expectedCells[lineNum]

				assert.Len(t, gotCells, len(expectedCells))

				for cellNum, gotCell := range gotCells {
					expectedCell := expectedCells[cellNum]

					assert.Equal(t, expectedCell, strings.Trim(gotCell, " "))
				}
			}
		})
	}
}

func TestMergeRequestList_labels(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")
	// NOTE: These subtests cannot run in parallel because they use cmdutils.GroupOverride()
	// which modifies global viper state (SetEnvPrefix, BindEnv).

	type labelTest struct {
		name            string
		cli             string
		expectLabels    *gitlab.LabelOptions
		expectNotLabels *gitlab.LabelOptions
	}

	tests := []labelTest{
		{
			name:         "--label",
			cli:          "--label foo",
			expectLabels: &gitlab.LabelOptions{"foo"},
		},
		{
			name:            "--not-label",
			cli:             "--not-label fooz",
			expectNotLabels: &gitlab.LabelOptions{"fooz"},
		},
	}

	testMR := &gitlab.BasicMergeRequest{
		ID:           76,
		IID:          6,
		ProjectID:    1,
		State:        "opened",
		Title:        "MergeRequest one",
		TargetBranch: "master",
		SourceBranch: "test1",
		Labels:       gitlab.Labels{"foo", "bar"},
		WebURL:       "http://gitlab.com/OWNER/REPO/merge_requests/6",
		References: &gitlab.IssueReferences{
			Full:     "OWNER/REPO/merge_requests/6",
			Relative: "#6",
			Short:    "#6",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testClient := gitlabtesting.NewTestClient(t)

			testClient.MockMergeRequests.EXPECT().
				ListProjectMergeRequests("OWNER/REPO", gomock.Any()).
				DoAndReturn(func(pid any, opts *gitlab.ListProjectMergeRequestsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.BasicMergeRequest, *gitlab.Response, error) {
					if tc.expectLabels != nil {
						assert.Equal(t, tc.expectLabels, opts.Labels)
					}
					if tc.expectNotLabels != nil {
						assert.Equal(t, tc.expectNotLabels, opts.NotLabels)
					}
					return []*gitlab.BasicMergeRequest{testMR}, nil, nil
				})

			apiClient, err := api.NewClient(
				func(*http.Client) (gitlab.AuthSource, error) {
					return gitlab.AccessTokenAuthSource{Token: "test-token"}, nil
				},
				api.WithGitLabClient(testClient.Client),
			)
			require.NoError(t, err)

			exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
				return NewCmdList(f, nil)
			}, true,
				cmdtest.WithApiClient(apiClient),
				cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			)

			output, err := exec(tc.cli)
			require.NoError(t, err)

			assert.Contains(t, output.String(), "!6\tOWNER/REPO/merge_requests/6")
			assert.Empty(t, output.Stderr())
		})
	}
}

func TestMrListJSON(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)

	createdAt1, _ := time.Parse(time.RFC3339Nano, "2022-01-20T21:20:50.665Z")
	updatedAt1, _ := time.Parse(time.RFC3339Nano, "2022-01-20T21:47:54.11Z")
	createdAt2, _ := time.Parse(time.RFC3339Nano, "2022-01-18T17:02:23.27Z")
	updatedAt2, _ := time.Parse(time.RFC3339Nano, "2022-01-18T18:06:50.054Z")

	testMRs := []*gitlab.BasicMergeRequest{
		{
			ID:                          136297744,
			IID:                         4,
			TargetBranch:                "main",
			SourceBranch:                "1-fake-issue-3",
			ProjectID:                   29316529,
			Title:                       "Draft: Resolve \"fake issue\"",
			State:                       "opened",
			Imported:                    false,
			ImportedFrom:                "",
			CreatedAt:                   &createdAt1,
			UpdatedAt:                   &updatedAt1,
			Upvotes:                     0,
			Downvotes:                   0,
			Author:                      &gitlab.BasicUser{ID: 8814129, Username: "OWNER", Name: "Some User", State: "active", AvatarURL: "https://gitlab.com/uploads/-/system/user/avatar/8814129/avatar.png", WebURL: "https://gitlab.com/OWNER"},
			Assignee:                    &gitlab.BasicUser{ID: 8814129, Username: "OWNER", Name: "Some User", State: "active", AvatarURL: "https://gitlab.com/uploads/-/system/user/avatar/8814129/avatar.png", WebURL: "https://gitlab.com/OWNER"},
			Assignees:                   []*gitlab.BasicUser{{ID: 8814129, Username: "OWNER", Name: "Some User", State: "active", AvatarURL: "https://gitlab.com/uploads/-/system/user/avatar/8814129/avatar.png", WebURL: "https://gitlab.com/OWNER"}},
			Reviewers:                   []*gitlab.BasicUser{},
			SourceProjectID:             29316529,
			TargetProjectID:             29316529,
			Labels:                      gitlab.Labels{},
			Description:                 "Closes #1",
			Draft:                       true,
			MergeWhenPipelineSucceeds:   false,
			DetailedMergeStatus:         "draft_status",
			SHA:                         "44eb489568f7cb1a5a730fce6b247cd3797172ca",
			UserNotesCount:              0,
			ShouldRemoveSourceBranch:    false,
			ForceRemoveSourceBranch:     true,
			AllowCollaboration:          false,
			AllowMaintainerToPush:       false,
			WebURL:                      "https://gitlab.com/OWNER/REPO/-/merge_requests/4",
			References:                  &gitlab.IssueReferences{Short: "!4", Relative: "!4", Full: "OWNER/REPO!4"},
			DiscussionLocked:            false,
			TimeStats:                   &gitlab.TimeStats{},
			Squash:                      false,
			SquashOnMerge:               false,
			TaskCompletionStatus:        &gitlab.TasksCompletionStatus{Count: 0, CompletedCount: 0},
			HasConflicts:                false,
			BlockingDiscussionsResolved: true,
		},
		{
			ID:                          135750125,
			IID:                         1,
			TargetBranch:                "main",
			SourceBranch:                "OWNER-main-patch-25608",
			ProjectID:                   29316529,
			Title:                       "Update .gitlab-ci.yml",
			State:                       "opened",
			Imported:                    false,
			ImportedFrom:                "",
			CreatedAt:                   &createdAt2,
			UpdatedAt:                   &updatedAt2,
			Upvotes:                     0,
			Downvotes:                   0,
			Author:                      &gitlab.BasicUser{ID: 8814129, Username: "OWNER", Name: "Some User", State: "active", AvatarURL: "https://gitlab.com/uploads/-/system/user/avatar/8814129/avatar.png", WebURL: "https://gitlab.com/OWNER"},
			Assignees:                   []*gitlab.BasicUser{},
			Reviewers:                   []*gitlab.BasicUser{},
			SourceProjectID:             29316529,
			TargetProjectID:             29316529,
			Labels:                      gitlab.Labels{},
			Description:                 "",
			Draft:                       false,
			MergeWhenPipelineSucceeds:   false,
			DetailedMergeStatus:         "mergeable",
			SHA:                         "123f34ebfd5d97ef562974e55e01b83f06ae7b4a",
			UserNotesCount:              0,
			ShouldRemoveSourceBranch:    false,
			ForceRemoveSourceBranch:     true,
			AllowCollaboration:          false,
			AllowMaintainerToPush:       false,
			WebURL:                      "https://gitlab.com/OWNER/REPO/-/merge_requests/1",
			References:                  &gitlab.IssueReferences{Short: "!1", Relative: "!1", Full: "OWNER/REPO!1"},
			DiscussionLocked:            false,
			TimeStats:                   &gitlab.TimeStats{},
			Squash:                      false,
			SquashOnMerge:               false,
			TaskCompletionStatus:        &gitlab.TasksCompletionStatus{Count: 0, CompletedCount: 0},
			HasConflicts:                false,
			BlockingDiscussionsResolved: true,
		},
	}

	testClient.MockMergeRequests.EXPECT().
		ListProjectMergeRequests("OWNER/REPO", gomock.Any()).
		Return(testMRs, nil, nil)

	apiClient, err := api.NewClient(
		func(*http.Client) (gitlab.AuthSource, error) {
			return gitlab.AccessTokenAuthSource{Token: "test-token"}, nil
		},
		api.WithGitLabClient(testClient.Client),
	)
	require.NoError(t, err)

	exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
		return NewCmdList(f, nil)
	}, true,
		cmdtest.WithApiClient(apiClient),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	output, err := exec("-F json")
	require.NoError(t, err)

	b, err := os.ReadFile("./testdata/mrList.json")
	require.NoError(t, err)

	expectedOut := string(b)

	assert.JSONEq(t, expectedOut, output.String())
	assert.Empty(t, output.Stderr())
}

func TestMergeRequestList_GroupAndReviewer(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	testClient := gitlabtesting.NewTestClient(t)

	// Mock CurrentUser for @me lookup
	testClient.MockUsers.EXPECT().
		CurrentUser(gomock.Any()).
		Return(&gitlab.User{ID: 1, Username: "me"}, nil, nil)

	// Mock ListGroupMergeRequests and verify reviewer_id is set
	testClient.MockMergeRequests.EXPECT().
		ListGroupMergeRequests("GROUP", gomock.Any()).
		DoAndReturn(func(gid any, opts *gitlab.ListGroupMergeRequestsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.BasicMergeRequest, *gitlab.Response, error) {
			assert.NotNil(t, opts.ReviewerID) // ReviewerID is set to user ID 1
			return []*gitlab.BasicMergeRequest{
				{
					ID:           76,
					IID:          6,
					ProjectID:    1,
					State:        "opened",
					Title:        "MergeRequest one",
					TargetBranch: "master",
					SourceBranch: "test1",
					Labels:       gitlab.Labels{"foo", "bar"},
					WebURL:       "http://gitlab.com/OWNER/REPO/merge_requests/6",
					References: &gitlab.IssueReferences{
						Full:     "OWNER/REPO/merge_requests/6",
						Relative: "#6",
						Short:    "#6",
					},
				},
			}, nil, nil
		})

	apiClient, err := api.NewClient(
		func(*http.Client) (gitlab.AuthSource, error) {
			return gitlab.AccessTokenAuthSource{Token: "test-token"}, nil
		},
		api.WithGitLabClient(testClient.Client),
	)
	require.NoError(t, err)

	exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
		return NewCmdList(f, nil)
	}, true,
		cmdtest.WithApiClient(apiClient),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	output, err := exec("--group GROUP --reviewer @me")
	require.NoError(t, err)

	assert.Equal(t, heredoc.Doc(`
		Showing 1 open merge request on GROUP. (Page 1)

		!6	OWNER/REPO/merge_requests/6	MergeRequest one	(master) ← (test1)

	`), output.String())
	assert.Empty(t, output.Stderr())
}

func TestMergeRequestList_GroupAndAssignee(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	testClient := gitlabtesting.NewTestClient(t)

	// Mock CurrentUser for @me lookup
	testClient.MockUsers.EXPECT().
		CurrentUser(gomock.Any()).
		Return(&gitlab.User{ID: 1, Username: "me"}, nil, nil)

	// Mock ListGroupMergeRequests and verify assignee_id is set
	testClient.MockMergeRequests.EXPECT().
		ListGroupMergeRequests("GROUP", gomock.Any()).
		DoAndReturn(func(gid any, opts *gitlab.ListGroupMergeRequestsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.BasicMergeRequest, *gitlab.Response, error) {
			assert.NotNil(t, opts.AssigneeID)
			return []*gitlab.BasicMergeRequest{
				{
					ID:           76,
					IID:          6,
					ProjectID:    1,
					State:        "opened",
					Title:        "MergeRequest one",
					TargetBranch: "master",
					SourceBranch: "test1",
					Labels:       gitlab.Labels{"foo", "bar"},
					WebURL:       "http://gitlab.com/OWNER/REPO/merge_requests/6",
					References: &gitlab.IssueReferences{
						Full:     "OWNER/REPO/merge_requests/6",
						Relative: "#6",
						Short:    "#6",
					},
				},
			}, nil, nil
		})

	apiClient, err := api.NewClient(
		func(*http.Client) (gitlab.AuthSource, error) {
			return gitlab.AccessTokenAuthSource{Token: "test-token"}, nil
		},
		api.WithGitLabClient(testClient.Client),
	)
	require.NoError(t, err)

	exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
		return NewCmdList(f, nil)
	}, true,
		cmdtest.WithApiClient(apiClient),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	output, err := exec("--group GROUP --assignee @me")
	require.NoError(t, err)

	assert.Equal(t, heredoc.Doc(`
		Showing 1 open merge request on GROUP. (Page 1)

		!6	OWNER/REPO/merge_requests/6	MergeRequest one	(master) ← (test1)

	`), output.String())
	assert.Empty(t, output.Stderr())
}

func TestMergeRequestList_GroupWithAssigneeAndReviewer(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	testClient := gitlabtesting.NewTestClient(t)

	// Mock ListUsers for reviewer lookup (some.user -> ID 2)
	testClient.MockUsers.EXPECT().
		ListUsers(gomock.Any()).
		DoAndReturn(func(opts *gitlab.ListUsersOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.User, *gitlab.Response, error) {
			if *opts.Username == "some.user" {
				return []*gitlab.User{{ID: 2, Username: "some.user"}}, nil, nil
			}
			if *opts.Username == "other.user" {
				return []*gitlab.User{{ID: 1, Username: "other.user"}}, nil, nil
			}
			return nil, nil, nil
		}).Times(2)

	// Mock ListGroupMergeRequests - called twice (once for assignee, once for reviewer)
	// Note: CreatedAt is required because the API sorts combined results by CreatedAt
	createdAt1, _ := time.Parse(time.RFC3339, "2024-01-04T15:31:51.081Z")
	createdAt2, _ := time.Parse(time.RFC3339, "2016-01-04T15:31:51.081Z")

	reviewerMR := &gitlab.BasicMergeRequest{
		ID:           77,
		IID:          7,
		ProjectID:    2,
		State:        "opened",
		Title:        "MergeRequest one",
		TargetBranch: "master",
		SourceBranch: "test2",
		Labels:       gitlab.Labels{"baz", "bar"},
		WebURL:       "http://gitlab.com/OWNER/REPO/merge_requests/7",
		CreatedAt:    &createdAt1,
		References: &gitlab.IssueReferences{
			Full:     "OWNER/REPO/merge_requests/7",
			Relative: "#7",
			Short:    "#7",
		},
	}

	assigneeMR := &gitlab.BasicMergeRequest{
		ID:           76,
		IID:          6,
		ProjectID:    1,
		State:        "opened",
		Title:        "MergeRequest one",
		TargetBranch: "master",
		SourceBranch: "test1",
		Labels:       gitlab.Labels{"foo", "bar"},
		WebURL:       "http://gitlab.com/OWNER/REPO/merge_requests/6",
		CreatedAt:    &createdAt2,
		References: &gitlab.IssueReferences{
			Full:     "OWNER/REPO/merge_requests/6",
			Relative: "#6",
			Short:    "#6",
		},
	}

	testClient.MockMergeRequests.EXPECT().
		ListGroupMergeRequests("GROUP", gomock.Any()).
		DoAndReturn(func(gid any, opts *gitlab.ListGroupMergeRequestsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.BasicMergeRequest, *gitlab.Response, error) {
			if opts.ReviewerID != nil {
				return []*gitlab.BasicMergeRequest{reviewerMR}, nil, nil
			}
			// Assignee request
			return []*gitlab.BasicMergeRequest{assigneeMR}, nil, nil
		}).Times(2)

	apiClient, err := api.NewClient(
		func(*http.Client) (gitlab.AuthSource, error) {
			return gitlab.AccessTokenAuthSource{Token: "test-token"}, nil
		},
		api.WithGitLabClient(testClient.Client),
	)
	require.NoError(t, err)

	exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
		return NewCmdList(f, nil)
	}, true,
		cmdtest.WithApiClient(apiClient),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	output, err := exec("--group GROUP --reviewer=some.user --assignee=other.user")
	require.NoError(t, err)

	assert.Equal(t, heredoc.Doc(`
		Showing 2 open merge requests on GROUP. (Page 1)

		!7	OWNER/REPO/merge_requests/7	MergeRequest one	(master) ← (test2)
		!6	OWNER/REPO/merge_requests/6	MergeRequest one	(master) ← (test1)

	`), output.String())
	assert.Empty(t, output.Stderr())
}

func TestMergeRequestList_SortAndOrderBy(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockMergeRequests.EXPECT().
		ListProjectMergeRequests("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(pid any, opts *gitlab.ListProjectMergeRequestsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.BasicMergeRequest, *gitlab.Response, error) {
			// Verify sort and order_by are passed correctly
			assert.Equal(t, "created_at", *opts.OrderBy)
			assert.Equal(t, "desc", *opts.Sort)
			return []*gitlab.BasicMergeRequest{
				{
					ID:           76,
					IID:          6,
					ProjectID:    1,
					State:        "opened",
					Title:        "MergeRequest one",
					Draft:        true,
					TargetBranch: "master",
					SourceBranch: "test1",
					Labels:       gitlab.Labels{"foo", "bar"},
					WebURL:       "http://gitlab.com/OWNER/REPO/merge_requests/6",
					References: &gitlab.IssueReferences{
						Full:     "OWNER/REPO/merge_requests/6",
						Relative: "#6",
						Short:    "#6",
					},
				},
				{
					ID:           77,
					IID:          7,
					ProjectID:    1,
					State:        "opened",
					Title:        "MergeRequest two",
					Draft:        true,
					TargetBranch: "master",
					SourceBranch: "test2",
					Labels:       gitlab.Labels{"fooz", "baz"},
					WebURL:       "http://gitlab.com/OWNER/REPO/merge_requests/7",
					References: &gitlab.IssueReferences{
						Full:     "OWNER/REPO/merge_requests/7",
						Relative: "#7",
						Short:    "#7",
					},
				},
			}, nil, nil
		})

	apiClient, err := api.NewClient(
		func(*http.Client) (gitlab.AuthSource, error) {
			return gitlab.AccessTokenAuthSource{Token: "test-token"}, nil
		},
		api.WithGitLabClient(testClient.Client),
	)
	require.NoError(t, err)

	exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
		return NewCmdList(f, nil)
	}, true,
		cmdtest.WithApiClient(apiClient),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	output, err := exec("--order created_at --sort desc")
	require.NoError(t, err)

	assert.Empty(t, output.Stderr())
	assert.Equal(t, heredoc.Doc(`
	Showing 2 open merge requests in OWNER/REPO that match your search. (Page 1)

	!6	OWNER/REPO/merge_requests/6	MergeRequest one	(master) ← (test1)
	!7	OWNER/REPO/merge_requests/7	MergeRequest two	(master) ← (test2)

	`), output.String())
}

func TestMergeRequestList_LabelPriorityDefaultsToAsc(t *testing.T) {
	// GIVEN
	testClient := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		func(f cmdutils.Factory) *cobra.Command { return NewCmdList(f, nil) },
		false,
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
	)

	// Setup mock - verify sort=asc is passed when order=label_priority
	testClient.MockMergeRequests.EXPECT().
		ListProjectMergeRequests("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(pid any, opts *gitlab.ListProjectMergeRequestsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.BasicMergeRequest, *gitlab.Response, error) {
			assert.Equal(t, "label_priority", *opts.OrderBy)
			assert.Equal(t, "asc", *opts.Sort)
			return []*gitlab.BasicMergeRequest{}, nil, nil
		})

	// WHEN
	_, err := exec("--order label_priority")

	// THEN
	require.NoError(t, err)
}

func TestMergeRequestList_PriorityDefaultsToAsc(t *testing.T) {
	// GIVEN
	testClient := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		func(f cmdutils.Factory) *cobra.Command { return NewCmdList(f, nil) },
		false,
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
	)

	// Setup mock - verify sort=asc is passed when order=priority
	testClient.MockMergeRequests.EXPECT().
		ListProjectMergeRequests("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(pid any, opts *gitlab.ListProjectMergeRequestsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.BasicMergeRequest, *gitlab.Response, error) {
			assert.Equal(t, "priority", *opts.OrderBy)
			assert.Equal(t, "asc", *opts.Sort)
			return []*gitlab.BasicMergeRequest{}, nil, nil
		})

	// WHEN
	_, err := exec("--order priority")

	// THEN
	require.NoError(t, err)
}

func TestMergeRequestList_CreatedAtDefaultsToDesc(t *testing.T) {
	// GIVEN
	testClient := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		func(f cmdutils.Factory) *cobra.Command { return NewCmdList(f, nil) },
		false,
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
	)

	// Setup mock - verify sort=desc is passed when order=created_at
	testClient.MockMergeRequests.EXPECT().
		ListProjectMergeRequests("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(pid any, opts *gitlab.ListProjectMergeRequestsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.BasicMergeRequest, *gitlab.Response, error) {
			assert.Equal(t, "created_at", *opts.OrderBy)
			assert.Equal(t, "desc", *opts.Sort)
			return []*gitlab.BasicMergeRequest{}, nil, nil
		})

	// WHEN
	_, err := exec("--order created_at")

	// THEN
	require.NoError(t, err)
}

func TestMergeRequestList_ExplicitSortOverridesDefault(t *testing.T) {
	// GIVEN
	testClient := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		func(f cmdutils.Factory) *cobra.Command { return NewCmdList(f, nil) },
		false,
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
	)

	// Setup mock - verify explicit --sort desc overrides the default asc for label_priority
	testClient.MockMergeRequests.EXPECT().
		ListProjectMergeRequests("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(pid any, opts *gitlab.ListProjectMergeRequestsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.BasicMergeRequest, *gitlab.Response, error) {
			assert.Equal(t, "label_priority", *opts.OrderBy)
			assert.Equal(t, "desc", *opts.Sort)
			return []*gitlab.BasicMergeRequest{}, nil, nil
		})

	// WHEN
	_, err := exec("--order label_priority --sort desc")

	// THEN
	require.NoError(t, err)
}
