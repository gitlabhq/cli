//go:build !integration

package list

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
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
	"gitlab.com/gitlab-org/cli/internal/commands/issuable"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestNewCmdList(t *testing.T) {
	ios, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))

	// No API calls are made in this test since we provide a custom runFunc
	factory := cmdtest.NewTestFactory(ios)
	t.Run("Issue_NewCmdList", func(t *testing.T) {
		gotOpts := &ListOptions{}
		err := NewCmdList(factory, func(opts *ListOptions) error {
			gotOpts = opts
			return nil
		}, issuable.TypeIssue).Execute()

		require.NoError(t, err)
		assert.Equal(t, factory.IO(), gotOpts.IO)

		gotBaseRepo, _ := gotOpts.BaseRepo()
		expectedBaseRepo, _ := factory.BaseRepo()
		assert.Equal(t, expectedBaseRepo, gotBaseRepo)
	})
}

func TestIssueList_tty(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	testClient := gitlabtesting.NewTestClient(t)

	createdAt := time.Date(2016, 1, 4, 15, 31, 51, 0, time.UTC)
	incidentType := "incident"

	testClient.MockIssues.EXPECT().
		ListProjectIssues("OWNER/REPO", gomock.Any()).
		Return([]*gitlab.Issue{
			{
				ID:          76,
				IID:         6,
				ProjectID:   1,
				State:       "opened",
				Title:       "Issue one",
				Description: "a description here",
				Labels:      gitlab.Labels{"foo", "bar"},
				WebURL:      "http://gitlab.com/OWNER/REPO/issues/6",
				CreatedAt:   &createdAt,
				References: &gitlab.IssueReferences{
					Full:     "OWNER/REPO/issues/6",
					Relative: "#6",
					Short:    "#6",
				},
			},
			{
				ID:          77,
				IID:         7,
				ProjectID:   1,
				State:       "opened",
				Title:       "Issue two",
				Description: "description two here",
				Labels:      gitlab.Labels{"fooz", "baz"},
				WebURL:      "http://gitlab.com/OWNER/REPO/issues/7",
				CreatedAt:   &createdAt,
				References: &gitlab.IssueReferences{
					Full:     "OWNER/REPO/issues/7",
					Relative: "#7",
					Short:    "#7",
				},
			},
			{
				ID:          78,
				IID:         8,
				ProjectID:   1,
				State:       "opened",
				Title:       "Incident",
				Description: "description incident here",
				Labels:      gitlab.Labels{"foo", "baz"},
				WebURL:      "http://gitlab.com/OWNER/REPO/issues/8",
				CreatedAt:   &createdAt,
				IssueType:   &incidentType,
				References: &gitlab.IssueReferences{
					Full:     "OWNER/REPO/issues/8",
					Relative: "#8",
					Short:    "#8",
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
		return NewCmdList(f, nil, issuable.TypeIssue)
	}, true,
		cmdtest.WithApiClient(apiClient),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	output, err := exec("")
	if err != nil {
		t.Errorf("error running command `issue list`: %v", err)
	}

	out := output.String()
	timeRE := regexp.MustCompile(`\d+ years`)
	out = timeRE.ReplaceAllString(out, "X years")

	assert.Equal(t, heredoc.Doc(`
		Showing 3 open issues in OWNER/REPO that match your search. (Page 1)

		ID	Title    	Labels     	Created at        
		#6	Issue one	(foo, bar) 	about X years ago
		#7	Issue two	(fooz, baz)	about X years ago
		#8	Incident 	(foo, baz) 	about X years ago

	`), out)
	assert.Empty(t, output.Stderr())
}

func TestIssueList_ids(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)

	createdAt := time.Date(2016, 1, 4, 15, 31, 51, 0, time.UTC)
	incidentType := "incident"

	testClient.MockIssues.EXPECT().
		ListProjectIssues("OWNER/REPO", gomock.Any()).
		Return([]*gitlab.Issue{
			{
				ID:        76,
				IID:       6,
				ProjectID: 1,
				State:     "opened",
				Title:     "Issue one",
				Labels:    gitlab.Labels{"foo", "bar"},
				WebURL:    "http://gitlab.com/OWNER/REPO/issues/6",
				CreatedAt: &createdAt,
			},
			{
				ID:        77,
				IID:       7,
				ProjectID: 1,
				State:     "opened",
				Title:     "Issue two",
				Labels:    gitlab.Labels{"fooz", "baz"},
				WebURL:    "http://gitlab.com/OWNER/REPO/issues/7",
				CreatedAt: &createdAt,
			},
			{
				ID:        78,
				IID:       8,
				ProjectID: 1,
				State:     "opened",
				Title:     "Incident",
				Labels:    gitlab.Labels{"foo", "baz"},
				WebURL:    "http://gitlab.com/OWNER/REPO/issues/8",
				CreatedAt: &createdAt,
				IssueType: &incidentType,
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
		return NewCmdList(f, nil, issuable.TypeIssue)
	}, true,
		cmdtest.WithApiClient(apiClient),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	output, err := exec("-F ids")
	if err != nil {
		t.Errorf("error running command `issue list -F ids`: %v", err)
	}

	assert.Equal(t, "6\n7\n8\n", output.String())
	assert.Empty(t, output.Stderr())
}

func TestIssueList_urls(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)

	createdAt := time.Date(2016, 1, 4, 15, 31, 51, 0, time.UTC)
	incidentType := "incident"

	testClient.MockIssues.EXPECT().
		ListProjectIssues("OWNER/REPO", gomock.Any()).
		Return([]*gitlab.Issue{
			{
				ID:        76,
				IID:       6,
				ProjectID: 1,
				State:     "opened",
				Title:     "Issue one",
				Labels:    gitlab.Labels{"foo", "bar"},
				WebURL:    "http://gitlab.com/OWNER/REPO/issues/6",
				CreatedAt: &createdAt,
			},
			{
				ID:        77,
				IID:       7,
				ProjectID: 1,
				State:     "opened",
				Title:     "Issue two",
				Labels:    gitlab.Labels{"fooz", "baz"},
				WebURL:    "http://gitlab.com/OWNER/REPO/issues/7",
				CreatedAt: &createdAt,
			},
			{
				ID:        78,
				IID:       8,
				ProjectID: 1,
				State:     "opened",
				Title:     "Incident",
				Labels:    gitlab.Labels{"foo", "baz"},
				WebURL:    "http://gitlab.com/OWNER/REPO/issues/8",
				CreatedAt: &createdAt,
				IssueType: &incidentType,
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
		return NewCmdList(f, nil, issuable.TypeIssue)
	}, true,
		cmdtest.WithApiClient(apiClient),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	output, err := exec("-F urls")
	if err != nil {
		t.Errorf("error running command `issue list -F urls`: %v", err)
	}

	assert.Equal(t, heredoc.Doc(`
		http://gitlab.com/OWNER/REPO/issues/6
		http://gitlab.com/OWNER/REPO/issues/7
		http://gitlab.com/OWNER/REPO/issues/8
	`), output.String())
	assert.Empty(t, output.Stderr())
}

func TestIssueList_tty_withFlags(t *testing.T) {
	// NOTE: These subtests cannot run in parallel because they use cmdutils.GroupOverride()
	// which modifies global viper state (SetEnvPrefix, BindEnv).
	t.Run("project", func(t *testing.T) {
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockUsers.EXPECT().
			ListUsers(gomock.Any()).
			Return([]*gitlab.User{
				{ID: 100, Username: "someuser"},
			}, nil, nil)

		testClient.MockIssues.EXPECT().
			ListProjectIssues("OWNER/REPO", gomock.Any()).
			DoAndReturn(func(pid any, opts *gitlab.ListProjectIssuesOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.Issue, *gitlab.Response, error) {
				// Verify flags are passed correctly
				// Note: -p is Page, -P is PerPage, so "-P1 -p100" means PerPage=1, Page=100
				assert.Equal(t, "opened", *opts.State)
				assert.Equal(t, int64(100), opts.Page)
				assert.Equal(t, int64(1), opts.PerPage)
				assert.True(t, *opts.Confidential)
				assert.NotNil(t, opts.AssigneeID) // User ID 100 from someuser lookup
				assert.Equal(t, gitlab.LabelOptions{"bug"}, *opts.Labels)
				assert.Equal(t, "1", *opts.Milestone)
				return []*gitlab.Issue{}, nil, nil
			})

		apiClient, err := api.NewClient(
			func(*http.Client) (gitlab.AuthSource, error) {
				return gitlab.AccessTokenAuthSource{Token: "test-token"}, nil
			},
			api.WithGitLabClient(testClient.Client),
		)
		require.NoError(t, err)

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdList(f, nil, issuable.TypeIssue)
		}, true,
			cmdtest.WithApiClient(apiClient),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		output, err := exec("--opened -P1 -p100 --confidential -a someuser -l bug -m1")
		require.NoError(t, err)

		assert.Empty(t, output.Stderr())
		assert.Equal(t, `No open issues match your search in OWNER/REPO.


`, output.String())
	})
	t.Run("group", func(t *testing.T) {
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockIssues.EXPECT().
			ListGroupIssues("GROUP", gomock.Any()).
			Return([]*gitlab.Issue{}, nil, nil)

		apiClient, err := api.NewClient(
			func(*http.Client) (gitlab.AuthSource, error) {
				return gitlab.AccessTokenAuthSource{Token: "test-token"}, nil
			},
			api.WithGitLabClient(testClient.Client),
		)
		require.NoError(t, err)

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdList(f, nil, issuable.TypeIssue)
		}, true,
			cmdtest.WithApiClient(apiClient),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		output, err := exec("--group GROUP")
		require.NoError(t, err)

		assert.Empty(t, output.Stderr())
		assert.Equal(t, `No open issues match your search in GROUP.


`, output.String())
	})
}

func TestIssueList_filterByIteration(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockIssues.EXPECT().
		ListProjectIssues("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(pid any, opts *gitlab.ListProjectIssuesOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.Issue, *gitlab.Response, error) {
			// Verify iteration_id is passed
			assert.Equal(t, int64(9), *opts.IterationID)
			return []*gitlab.Issue{}, nil, nil
		})

	apiClient, err := api.NewClient(
		func(*http.Client) (gitlab.AuthSource, error) {
			return gitlab.AccessTokenAuthSource{Token: "test-token"}, nil
		},
		api.WithGitLabClient(testClient.Client),
	)
	require.NoError(t, err)

	exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
		return NewCmdList(f, nil, issuable.TypeIssue)
	}, true,
		cmdtest.WithApiClient(apiClient),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	output, err := exec("--iteration 9")
	require.NoError(t, err)

	assert.Empty(t, output.Stderr())
	assert.Equal(t, `No open issues match your search in OWNER/REPO.


`, output.String())
}

func TestIssueList_tty_withIssueType(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	testClient := gitlabtesting.NewTestClient(t)

	createdAt := time.Date(2016, 1, 4, 15, 31, 51, 0, time.UTC)
	incidentType := "incident"

	testClient.MockIssues.EXPECT().
		ListProjectIssues("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(pid any, opts *gitlab.ListProjectIssuesOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.Issue, *gitlab.Response, error) {
			// Verify issue_type filter is passed
			assert.Equal(t, new("incident"), opts.IssueType)
			return []*gitlab.Issue{
				{
					ID:          78,
					IID:         8,
					ProjectID:   1,
					State:       "opened",
					Title:       "Incident",
					Description: "description incident here",
					Labels:      gitlab.Labels{"foo", "baz"},
					WebURL:      "http://gitlab.com/OWNER/REPO/issues/8",
					CreatedAt:   &createdAt,
					IssueType:   &incidentType,
					References: &gitlab.IssueReferences{
						Full:     "OWNER/REPO/issues/8",
						Relative: "#8",
						Short:    "#8",
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
		return NewCmdList(f, nil, issuable.TypeIssue)
	}, true,
		cmdtest.WithApiClient(apiClient),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	output, err := exec("--issue-type=incident")
	require.NoError(t, err)

	out := output.String()
	timeRE := regexp.MustCompile(`\d+ years`)
	out = timeRE.ReplaceAllString(out, "X years")

	assert.Contains(t, out, "Showing 1 open incident in OWNER/REPO that match your search. (Page 1)")
	assert.Contains(t, out, "#8\tIncident\t(foo, baz)\tabout X years ago")
	assert.Empty(t, output.Stderr())
}

func TestIncidentList_tty_withIssueType(t *testing.T) {
	// This test doesn't need API mocking - it just tests that --issue-type flag
	// is not allowed for incident command
	exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
		return NewCmdList(f, nil, issuable.TypeIncident)
	}, true,
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	output, err := exec("--issue-type=incident")
	if err == nil {
		t.Error("expected an `unknown flag: --issue-type` error, but got nothing")
	}

	assert.Empty(t, output.String())
	assert.Empty(t, output.Stderr())
}

func TestIssueList_tty_mine(t *testing.T) {
	t.Run("mine with all flag and user exists", func(t *testing.T) {
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockUsers.EXPECT().
			CurrentUser(gomock.Any()).
			Return(&gitlab.User{Username: "john_smith"}, nil, nil)

		testClient.MockIssues.EXPECT().
			ListProjectIssues("OWNER/REPO", gomock.Any()).
			DoAndReturn(func(pid any, opts *gitlab.ListProjectIssuesOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.Issue, *gitlab.Response, error) {
				// Verify assignee ID is set from current user lookup
				assert.NotNil(t, opts.AssigneeID)
				return []*gitlab.Issue{}, nil, nil
			})

		apiClient, err := api.NewClient(
			func(*http.Client) (gitlab.AuthSource, error) {
				return gitlab.AccessTokenAuthSource{Token: "test-token"}, nil
			},
			api.WithGitLabClient(testClient.Client),
		)
		require.NoError(t, err)

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdList(f, nil, issuable.TypeIssue)
		}, true,
			cmdtest.WithApiClient(apiClient),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		output, err := exec("--mine -A")
		require.NoError(t, err)

		assert.Empty(t, output.Stderr())
		assert.Equal(t, `No issues match your search in OWNER/REPO.


`, output.String())
	})
	t.Run("user does not exists", func(t *testing.T) {
		testClient := gitlabtesting.NewTestClient(t)

		notFoundResp := &gitlab.Response{
			Response: &http.Response{StatusCode: http.StatusNotFound},
		}
		testClient.MockUsers.EXPECT().
			CurrentUser(gomock.Any()).
			Return(nil, notFoundResp, gitlab.ErrNotFound)

		apiClient, err := api.NewClient(
			func(*http.Client) (gitlab.AuthSource, error) {
				return gitlab.AccessTokenAuthSource{Token: "test-token"}, nil
			},
			api.WithGitLabClient(testClient.Client),
		)
		require.NoError(t, err)

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdList(f, nil, issuable.TypeIssue)
		}, true,
			cmdtest.WithApiClient(apiClient),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		)

		output, err := exec("--mine -A")
		require.Error(t, err)

		assert.Empty(t, output.Stderr())
		assert.Empty(t, output.String())
	})
}

func makeHyperlink(linkText, targetURL string) string {
	return fmt.Sprintf("\x1b]8;;%s\x1b\\%s\x1b]8;;\x1b\\", targetURL, linkText)
}

func TestIssueList_hyperlinks(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	noHyperlinkCells := [][]string{
		{"#6", "Issue one", "(foo, bar)", "about X years ago"},
		{"#7", "Issue two", "(fooz, baz)", "about X years ago"},
		{"#8", "Incident", "(foo, baz)", "about X years ago"},
	}

	hyperlinkCells := [][]string{
		{makeHyperlink("#6", "http://gitlab.com/OWNER/REPO/issues/6"), "Issue one", "(foo, bar)", "about X years ago"},
		{makeHyperlink("#7", "http://gitlab.com/OWNER/REPO/issues/7"), "Issue two", "(fooz, baz)", "about X years ago"},
		{makeHyperlink("#8", "http://gitlab.com/OWNER/REPO/issues/8"), "Incident", "(foo, baz)", "about X years ago"},
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

	createdAt1 := time.Date(2016, 1, 4, 15, 31, 51, 0, time.UTC)
	createdAt2 := time.Date(2016, 1, 4, 16, 31, 51, 0, time.UTC)
	incidentType := "incident"

	testIssues := []*gitlab.Issue{
		{
			ID:          76,
			IID:         6,
			ProjectID:   1,
			State:       "opened",
			Title:       "Issue one",
			Description: "a description here",
			Labels:      gitlab.Labels{"foo", "bar"},
			WebURL:      "http://gitlab.com/OWNER/REPO/issues/6",
			CreatedAt:   &createdAt1,
			References: &gitlab.IssueReferences{
				Full:     "OWNER/REPO/issues/6",
				Relative: "#6",
				Short:    "#6",
			},
		},
		{
			ID:          77,
			IID:         7,
			ProjectID:   1,
			State:       "opened",
			Title:       "Issue two",
			Description: "description two here",
			Labels:      gitlab.Labels{"fooz", "baz"},
			WebURL:      "http://gitlab.com/OWNER/REPO/issues/7",
			CreatedAt:   &createdAt1,
			References: &gitlab.IssueReferences{
				Full:     "OWNER/REPO/issues/7",
				Relative: "#7",
				Short:    "#7",
			},
		},
		{
			ID:          78,
			IID:         8,
			ProjectID:   1,
			State:       "opened",
			Title:       "Incident",
			Description: "description incident here",
			Labels:      gitlab.Labels{"foo", "baz"},
			WebURL:      "http://gitlab.com/OWNER/REPO/issues/8",
			CreatedAt:   &createdAt2,
			IssueType:   &incidentType,
			References: &gitlab.IssueReferences{
				Full:     "OWNER/REPO/issues/8",
				Relative: "#8",
				Short:    "#8",
			},
		},
	}

	for _, tc := range tests {
		t.Run("", func(t *testing.T) {
			testClient := gitlabtesting.NewTestClient(t)

			testClient.MockIssues.EXPECT().
				ListProjectIssues("OWNER/REPO", gomock.Any()).
				Return(testIssues, nil, nil)

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
				return NewCmdList(f, nil, issuable.TypeIssue)
			}, tc.isTTY,
				cmdtest.WithIOStreamsOverride(ios),
				cmdtest.WithApiClient(apiClient),
				cmdtest.WithGitLabClient(testClient.Client),
				cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			)

			output, err := exec("")
			require.NoError(t, err)

			out := output.String()
			timeRE := regexp.MustCompile(`\d+ years`)
			out = timeRE.ReplaceAllString(out, "X years")

			lines := strings.Split(strings.TrimRight(out, "\n"), "\n")

			// first two lines have the header and some separating whitespace, so skip those
			for lineNum, line := range lines[3:] {
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

func TestIssueListJSON(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)

	createdAt, _ := time.Parse(time.RFC3339, "2024-01-31T05:37:57.883Z")
	updatedAt, _ := time.Parse(time.RFC3339, "2024-02-02T00:54:02.842Z")
	issueType := "issue"

	testIssue := &gitlab.Issue{
		ID:                   141525495,
		IID:                  15,
		ProjectID:            37777023,
		Title:                "tem",
		Description:          "",
		State:                "opened",
		CreatedAt:            &createdAt,
		UpdatedAt:            &updatedAt,
		Labels:               gitlab.Labels{},
		Assignees:            []*gitlab.IssueAssignee{},
		Author:               &gitlab.IssueAuthor{ID: 11809982, Username: "jay_mccure", Name: "Jay McCure", State: "active", AvatarURL: "https://gitlab.com/uploads/-/system/user/avatar/11809982/avatar.png", WebURL: "https://gitlab.com/jay_mccure"},
		Confidential:         false,
		IssueType:            &issueType,
		WebURL:               "https://gitlab.com/jay_mccure/test2target/-/issues/15",
		TimeStats:            &gitlab.TimeStats{},
		TaskCompletionStatus: &gitlab.TasksCompletionStatus{Count: 0, CompletedCount: 0},
		Links:                &gitlab.IssueLinks{Self: "https://gitlab.com/api/v4/projects/37777023/issues/15", Notes: "https://gitlab.com/api/v4/projects/37777023/issues/15/notes", AwardEmoji: "https://gitlab.com/api/v4/projects/37777023/issues/15/award_emoji", Project: "https://gitlab.com/api/v4/projects/37777023"},
		References:           &gitlab.IssueReferences{Short: "#15", Relative: "#15", Full: "jay_mccure/test2target#15"},
	}

	testClient.MockIssues.EXPECT().
		ListProjectIssues("OWNER/REPO", gomock.Any()).
		Return([]*gitlab.Issue{testIssue}, nil, nil)

	apiClient, err := api.NewClient(
		func(*http.Client) (gitlab.AuthSource, error) {
			return gitlab.AccessTokenAuthSource{Token: "test-token"}, nil
		},
		api.WithGitLabClient(testClient.Client),
	)
	require.NoError(t, err)

	exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
		return NewCmdList(f, nil, issuable.TypeIssue)
	}, true,
		cmdtest.WithApiClient(apiClient),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	output, err := exec("--output json")
	require.NoError(t, err)

	b, err := os.ReadFile("./testdata/issueListFull.json")
	require.NoError(t, err)

	expectedOut := string(b)

	assert.JSONEq(t, expectedOut, output.String())
	assert.Empty(t, output.Stderr())
}

func TestIssueListMutualOutputFlags(t *testing.T) {
	// This test doesn't need API mocking - it just tests flag validation
	exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
		return NewCmdList(f, nil, issuable.TypeIssue)
	}, true,
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	_, err := exec("--output json --output-format ids")

	require.Error(t, err)
	assert.EqualError(t, err, "if any flags in the group [output output-format] are set none of the others can be; [output output-format] were all set")
}

func TestIssueList_epicIssues(t *testing.T) {
	// NOTE: This test cannot run in parallel because it uses cmdutils.GroupOverride()
	// which modifies global viper state (SetEnvPrefix, BindEnv).

	testdata := []*gitlab.Issue{
		{
			IID:   1,
			State: "opened",
			Assignees: []*gitlab.IssueAssignee{
				{ID: 101},
			},
			Author: &gitlab.IssueAuthor{ID: 102},
			Labels: gitlab.Labels{"label::one"},
			Milestone: &gitlab.Milestone{
				Title: "Milestone one",
			},
			Title: "This is issue one",
			Iteration: &gitlab.GroupIteration{
				ID: 103,
			},
			Confidential: false,
		},
		{
			IID:   2,
			State: "closed",
			Assignees: []*gitlab.IssueAssignee{
				{ID: 102},
			},
			Author: &gitlab.IssueAuthor{ID: 202},
			Labels: gitlab.Labels{"label::two"},
			Milestone: &gitlab.Milestone{
				Title: "Milestone two",
			},
			Title: "That is issue two",
			Iteration: &gitlab.GroupIteration{
				ID: 203,
			},
			Confidential: true,
		},
	}

	tests := []struct {
		name        string
		commandLine string
		user        *gitlab.User
		wantIDs     []int
		wantErr     string
		perPage     int
	}{
		{
			name:        "group flag",
			commandLine: `--group testGroupID --epic 42`,
			wantIDs:     []int{1},
		},
		{
			name:        "repo flag",
			commandLine: `--repo testGroupID/repo --epic 42`,
			wantIDs:     []int{1},
		},
		{
			name:        "all flag",
			commandLine: `--group testGroupID --epic 42 --all`,
			wantIDs:     []int{1, 2},
		},
		{
			name:        "closed flag",
			commandLine: `--group testGroupID --epic 42 --closed`,
			wantIDs:     []int{2},
		},
		{
			name: "assignee flag",
			user: &gitlab.User{
				ID:       101,
				Username: "one-oh-one",
			},
			commandLine: `--group testGroupID --epic 42 --all --assignee one-oh-one`,
			wantIDs:     []int{1},
		},
		{
			name: "not-assignee flag",
			user: &gitlab.User{
				ID:       101,
				Username: "one-oh-one",
			},
			commandLine: `--group testGroupID --epic 42 --all --not-assignee one-oh-one`,
			wantIDs:     []int{2},
		},
		{
			name: "author flag",
			user: &gitlab.User{
				ID:       102,
				Username: "one-oh-two",
			},
			commandLine: `--group testGroupID --epic 42 --all --author one-oh-two`,
			wantIDs:     []int{1},
		},
		{
			name: "not-author flag",
			user: &gitlab.User{
				ID:       102,
				Username: "one-oh-two",
			},
			commandLine: `--group testGroupID --epic 42 --all --not-author one-oh-two`,
			wantIDs:     []int{2},
		},
		{
			name:        "milestone flag",
			commandLine: `--group testGroupID --epic 42 --all --milestone 'milestone one'`,
			wantIDs:     []int{1},
		},
		{
			name:        "search flag",
			commandLine: `--group testGroupID --epic 42 --all --search 'iSsUe OnE'`,
			wantIDs:     []int{1},
		},
		{
			name:        "iteration flag",
			commandLine: `--group testGroupID --epic 42 --all --iteration 103`,
			wantIDs:     []int{1},
		},
		{
			name:        "confidential flag",
			commandLine: `--group testGroupID --epic 42 --all --confidential`,
			wantIDs:     []int{2},
		},
		{
			name:        "page flag",
			commandLine: `--group testGroupID --epic 42 --all --page=2`,
			wantErr:     "the --page flag",
		},
		{
			name:        "per-page flag",
			commandLine: `--group testGroupID --epic 42 --all --per-page=9999`,
			// per-page is clamped to the max supported per_page value
			perPage: api.MaxPerPage,
			wantIDs: []int{1, 2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testClient := gitlabtesting.NewTestClient(t)

			if tt.user != nil {
				testClient.MockUsers.EXPECT().
					ListUsers(gomock.Any()).
					Return([]*gitlab.User{tt.user}, nil, nil)
			}

			if tt.wantErr == "" {
				testClient.MockEpicIssues.EXPECT().
					ListEpicIssues("testGroupID", int64(42), gomock.Any()).
					DoAndReturn(func(gid any, epic int64, opts *gitlab.ListOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.Issue, *gitlab.Response, error) {
						if tt.perPage != 0 {
							assert.Equal(t, int64(tt.perPage), opts.PerPage)
						}
						return testdata, &gitlab.Response{NextPage: 0}, nil
					})
			}

			apiClient, err := api.NewClient(
				func(*http.Client) (gitlab.AuthSource, error) {
					return gitlab.AccessTokenAuthSource{Token: "test-token"}, nil
				},
				api.WithGitLabClient(testClient.Client),
			)
			require.NoError(t, err)

			exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
				return NewCmdList(f, nil, issuable.TypeIssue)
			}, true,
				cmdtest.WithApiClient(apiClient),
				cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			)

			output, err := exec(tt.commandLine + ` --output-format ids`)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
			}
			if err != nil {
				return
			}

			assert.Empty(t, output.Stderr())

			gotIDs, err := strToIntSlice(output.String())
			if err != nil {
				t.Fatalf("command %q: unexpected output:\n%s", tt.commandLine, output.String())
			}

			assert.Equal(t, tt.wantIDs, gotIDs)
		})
	}
}

func TestIssueList_filterByLabel(t *testing.T) {
	// NOTE: This test cannot run in parallel because it uses cmdutils.GroupOverride()
	// which modifies global viper state (SetEnvPrefix, BindEnv).

	tests := map[string]struct {
		respBody []*gitlab.Issue
		args     string
		expect   []int
	}{
		"with --label": {
			respBody: []*gitlab.Issue{
				{
					IID:   1,
					State: "opened",
					Assignees: []*gitlab.IssueAssignee{
						{ID: 101},
					},
					Author: &gitlab.IssueAuthor{ID: 102},
					Labels: gitlab.Labels{"label::one"},
					Milestone: &gitlab.Milestone{
						Title: "Milestone one",
					},
					Title: "This is issue one",
					Iteration: &gitlab.GroupIteration{
						ID: 103,
					},
					Confidential: false,
				},
				{
					IID:   2,
					State: "open",
					Assignees: []*gitlab.IssueAssignee{
						{ID: 101},
					},
					Author: &gitlab.IssueAuthor{ID: 102},
					Labels: gitlab.Labels{"label::one"},
					Milestone: &gitlab.Milestone{
						Title: "Milestone two",
					},
					Title: "That is issue two",
					Iteration: &gitlab.GroupIteration{
						ID: 203,
					},
					Confidential: false,
				},
			},
			args:   "--group testGroupID --epic 42 --all --label label::one",
			expect: []int{1, 2},
		},
		"with --not-label": {
			respBody: []*gitlab.Issue{
				{
					IID:   3,
					State: "open",
					Assignees: []*gitlab.IssueAssignee{
						{ID: 101},
					},
					Author: &gitlab.IssueAuthor{ID: 102},
					Labels: gitlab.Labels{"label::two"},
					Milestone: &gitlab.Milestone{
						Title: "Milestone two",
					},
					Title: "That is issue three",
					Iteration: &gitlab.GroupIteration{
						ID: 303,
					},
					Confidential: false,
				},
			},
			args:   "--group testGroupID --epic 42 --all --not-label label::one",
			expect: []int{3},
		},
		"with --label and --not-label": {
			respBody: []*gitlab.Issue{
				{
					IID:   1,
					State: "opened",
					Assignees: []*gitlab.IssueAssignee{
						{ID: 101},
					},
					Author: &gitlab.IssueAuthor{ID: 102},
					Labels: gitlab.Labels{"label::one"},
					Milestone: &gitlab.Milestone{
						Title: "Milestone one",
					},
					Title: "This is issue one",
					Iteration: &gitlab.GroupIteration{
						ID: 103,
					},
					Confidential: false,
				},
				{
					IID:   2,
					State: "open",
					Assignees: []*gitlab.IssueAssignee{
						{ID: 101},
					},
					Author: &gitlab.IssueAuthor{ID: 102},
					Labels: gitlab.Labels{"label::one"},
					Milestone: &gitlab.Milestone{
						Title: "Milestone two",
					},
					Title: "That is issue two",
					Iteration: &gitlab.GroupIteration{
						ID: 203,
					},
					Confidential: false,
				},
				{
					IID:   3,
					State: "open",
					Assignees: []*gitlab.IssueAssignee{
						{ID: 101},
					},
					Author: &gitlab.IssueAuthor{ID: 102},
					Labels: gitlab.Labels{"label::two"},
					Milestone: &gitlab.Milestone{
						Title: "Milestone two",
					},
					Title: "That is issue three",
					Iteration: &gitlab.GroupIteration{
						ID: 303,
					},
					Confidential: false,
				},
			},
			args:   "--group testGroupID --epic 42 --all --label label::one --not-label label::three",
			expect: []int{1, 2, 3},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			testClient := gitlabtesting.NewTestClient(t)

			testClient.MockEpicIssues.EXPECT().
				ListEpicIssues("testGroupID", int64(42), gomock.Any()).
				Return(tt.respBody, &gitlab.Response{NextPage: 0}, nil)

			apiClient, err := api.NewClient(
				func(*http.Client) (gitlab.AuthSource, error) {
					return gitlab.AccessTokenAuthSource{Token: "test-token"}, nil
				},
				api.WithGitLabClient(testClient.Client),
			)
			require.NoError(t, err)

			exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
				return NewCmdList(f, nil, issuable.TypeIssue)
			}, true,
				cmdtest.WithApiClient(apiClient),
				cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			)

			out, err := exec(tt.args + " --output-format ids")
			require.NoError(t, err, "command: %s err: %v", tt.args, err)
			require.Emptyf(t, out.Stderr(), "command: %s stderr: %s", tt.args, out.Stderr())

			got, err := strToIntSlice(out.String())
			require.NoErrorf(t, err, "command: %s output:\n%s", tt.args, out)

			assert.Equal(t, tt.expect, got)
		})
	}
}

func strToIntSlice(s string) ([]int, error) {
	var ret []int

	scanner := bufio.NewScanner(strings.NewReader(s))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		i, err := strconv.Atoi(line)
		if err != nil {
			return nil, err
		}

		ret = append(ret, i)
	}

	slices.Sort(ret)

	return ret, nil
}
