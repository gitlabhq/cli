//go:build !integration

package issueutils

import (
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_issueMetadataFromURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		str  string
		want int64
		path string
	}{
		{
			name: "valid URL",
			str:  "https://gitlab.com/namespace/repo/-/issues/1",
			want: 1,
			path: "https://gitlab.com/namespace/repo/",
		},
		{
			name: "valid URL with nested subgroup",
			str:  "https://gitlab.com/namespace/project/subproject/repo/-/issues/100",
			want: 100,
			path: "https://gitlab.com/namespace/project/subproject/repo/",
		},
		{
			name: "valid URL without dash",
			str:  "https://gitlab.com/namespace/project/subproject/repo/issues/1",
			want: 1,
			path: "https://gitlab.com/namespace/project/subproject/repo/",
		},
		{
			name: "valid incident URL",
			str:  "https://gitlab.com/namespace/repo/-/issues/incident/1",
			want: 1,
			path: "https://gitlab.com/namespace/repo/",
		},
		{
			name: "valid incident URL with nested subgroup",
			str:  "https://gitlab.com/namespace/project/subproject/repo/-/issues/incident/100",
			want: 100,
			path: "https://gitlab.com/namespace/project/subproject/repo/",
		},
		{
			name: "valid incident URL without dash",
			str:  "https://gitlab.com/namespace/project/subproject/repo/issues/incident/1",
			want: 1,
			path: "https://gitlab.com/namespace/project/subproject/repo/",
		},
		{
			name: "valid work item URL",
			str:  "https://gitlab.com/namespace/repo/-/work_items/1",
			want: 1,
			path: "https://gitlab.com/namespace/repo/",
		},
		{
			name: "valid work item URL with hyphenated namespace",
			str:  "https://gitlab.com/gitlab-org/gitlab/-/work_items/337780",
			want: 337780,
			path: "https://gitlab.com/gitlab-org/gitlab/",
		},
		{
			name: "valid work item URL without dash",
			str:  "https://gitlab.com/namespace/project/subproject/repo/work_items/1",
			want: 1,
			path: "https://gitlab.com/namespace/project/subproject/repo/",
		},
		{
			name: "valid work item URL with trailing slash",
			str:  "https://gitlab.com/namespace/repo/-/work_items/1/",
			want: 1,
			path: "https://gitlab.com/namespace/repo/",
		},
		{
			name: "valid work item URL with query string",
			str:  "https://gitlab.com/namespace/repo/-/work_items/1?foo=bar",
			want: 1,
			path: "https://gitlab.com/namespace/repo/",
		},
		{
			name: "invalid URL with no issue number",
			str:  "https://gitlab.com/namespace/project/subproject/repo/issues",
			want: 0,
			path: "",
		},
		{
			name: "invalid incident URL with no incident number",
			str:  "https://gitlab.com/namespace/project/subproject/repo/issues/incident",
			want: 0,
			path: "",
		},
		{
			name: "invalid URL with only namespace, missing repo",
			str:  "https://gitlab.com/namespace/issues/100",
			want: 0,
			path: "",
		},
		{
			name: "invalid incident URL with only namespace, missing repo",
			str:  "https://gitlab.com/namespace/issues/incident/100",
			want: 0,
			path: "",
		},
		{
			name: "invalid issue URL",
			str:  "https://gitlab.com/namespace/repo",
			want: 0,
			path: "",
		},
		{
			name: "invalid scheme-less URL",
			str:  "//gitlab.com/namespace/repo/-/issues/1",
			want: 0,
			path: "",
		},
		{
			name: "invalid merge request URL",
			str:  "https://gitlab.com/namespace/repo/-/merge_requests/1",
			want: 0,
			path: "",
		},
		{
			name: "valid URL with trailing merge_requests segment",
			str:  "https://gitlab.com/namespace/repo/issues/12/merge_requests/3",
			want: 12,
			path: "https://gitlab.com/namespace/repo/",
		},
		{
			name: "invalid issue URL, missing issues path",
			str:  "https://gitlab.com/namespace/project/subproject/repo/10/",
			want: 0,
			path: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			id, repo := issueMetadataFromURL(tt.str, glinstance.DefaultHostname, config.NewBlankConfig())
			require.Equal(t, tt.want, id)

			if tt.want != 0 && tt.path != "" {
				expectedRepo, err := glrepo.FromFullName(tt.path, glinstance.DefaultHostname, config.NewBlankConfig())
				require.NoError(t, err)
				require.Equal(t, expectedRepo, repo)
			}
		})
	}
}

func Test_DisplayIssue(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		issue     *gitlab.Issue
		isTTY     bool
		wantTTY   string
		wantNoTTY string
	}{
		{
			name: "opened issue with CreatedAt set, TTY",
			issue: &gitlab.Issue{
				IID:       1,
				State:     "opened",
				Title:     "Fix the bug",
				WebURL:    "https://gitlab.com/gitlab-org/cli/-/issues/1",
				CreatedAt: &now,
			},
			isTTY: true,
		},
		{
			name: "opened issue with nil CreatedAt, TTY - no panic and no empty parentheses",
			issue: &gitlab.Issue{
				IID:       2,
				State:     "opened",
				Title:     "External Jira issue",
				WebURL:    "https://gitlab.com/gitlab-org/cli/-/issues/2",
				CreatedAt: nil,
			},
			isTTY:   true,
			wantTTY: "#2 External Jira issue\n https://gitlab.com/gitlab-org/cli/-/issues/2\n",
		},
		{
			name: "closed issue with nil CreatedAt, TTY",
			issue: &gitlab.Issue{
				IID:       3,
				State:     "closed",
				Title:     "Closed external issue",
				WebURL:    "https://gitlab.com/gitlab-org/cli/-/issues/3",
				CreatedAt: nil,
			},
			isTTY:   true,
			wantTTY: "#3 Closed external issue\n https://gitlab.com/gitlab-org/cli/-/issues/3\n",
		},
		{
			name: "non-TTY output returns only WebURL regardless of CreatedAt",
			issue: &gitlab.Issue{
				IID:       4,
				State:     "opened",
				Title:     "Some issue",
				WebURL:    "https://gitlab.com/gitlab-org/cli/-/issues/4",
				CreatedAt: nil,
			},
			isTTY:     false,
			wantNoTTY: "https://gitlab.com/gitlab-org/cli/-/issues/4",
		},
		{
			name: "non-TTY output with CreatedAt set returns only WebURL",
			issue: &gitlab.Issue{
				IID:       5,
				State:     "opened",
				Title:     "Some issue with date",
				WebURL:    "https://gitlab.com/gitlab-org/cli/-/issues/5",
				CreatedAt: &now,
			},
			isTTY:     false,
			wantNoTTY: "https://gitlab.com/gitlab-org/cli/-/issues/5",
		},
	}

	streams, _, _, _ := cmdtest.TestIOStreams()
	c := streams.Color()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Ensure no panic occurs (covers the nil pointer dereference fix)
			got := DisplayIssue(c, tt.issue, tt.isTTY)

			if tt.isTTY {
				if tt.wantTTY != "" {
					assert.Equal(t, tt.wantTTY, got)
				}
				// For issues with a non-nil CreatedAt, just verify no empty parentheses
				if tt.issue.CreatedAt != nil {
					assert.NotContains(t, got, "()")
					assert.Contains(t, got, tt.issue.Title)
					assert.Contains(t, got, tt.issue.WebURL)
				}
			} else {
				assert.Equal(t, tt.wantNoTTY, got)
			}
		})
	}
}

func Test_DisplayIssueList(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		issues    []*gitlab.Issue
		wantEmpty bool
	}{
		{
			name: "list with all CreatedAt set - no panic",
			issues: []*gitlab.Issue{
				{
					IID:       1,
					State:     "opened",
					Title:     "First issue",
					WebURL:    "https://gitlab.com/gitlab-org/cli/-/issues/1",
					CreatedAt: &now,
				},
				{
					IID:       2,
					State:     "closed",
					Title:     "Second issue",
					WebURL:    "https://gitlab.com/gitlab-org/cli/-/issues/2",
					CreatedAt: &now,
				},
			},
		},
		{
			name: "list with nil CreatedAt - no panic, empty cell rendered",
			issues: []*gitlab.Issue{
				{
					IID:       0,
					State:     "opened",
					Title:     "External Jira issue",
					WebURL:    "https://jira.example.com/browse/PROJ-1",
					CreatedAt: nil,
				},
			},
		},
		{
			name: "mixed list with some nil CreatedAt - no panic",
			issues: []*gitlab.Issue{
				{
					IID:       1,
					State:     "opened",
					Title:     "Normal issue",
					WebURL:    "https://gitlab.com/gitlab-org/cli/-/issues/1",
					CreatedAt: &now,
				},
				{
					IID:       0,
					State:     "opened",
					Title:     "External issue without date",
					WebURL:    "https://jira.example.com/browse/PROJ-2",
					CreatedAt: nil,
				},
			},
		},
		{
			name:      "empty list",
			issues:    []*gitlab.Issue{},
			wantEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			streams, _, _, _ := cmdtest.TestIOStreams()

			// Ensure no panic occurs (covers the nil pointer dereference fix)
			got := DisplayIssueList(streams, tt.issues, "gitlab-org/cli")

			if tt.wantEmpty {
				assert.Empty(t, got)
				return
			}

			// Verify all issue titles appear in the output
			for _, issue := range tt.issues {
				assert.Contains(t, got, issue.Title)
			}
		})
	}
}

func Test_IssuesFromArgs_LimitsConcurrency(t *testing.T) {
	ios, _, _, _ := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(ios, cmdtest.WithBaseRepo("foo", "bar", ""))

	var inFlight, maxInFlight atomic.Int32
	release := make(chan struct{})
	origGetIssue := api.GetIssue
	t.Cleanup(func() { api.GetIssue = origGetIssue })
	api.GetIssue = func(_ *gitlab.Client, _ any, issueID int64) (*gitlab.Issue, error) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			m := maxInFlight.Load()
			if n <= m || maxInFlight.CompareAndSwap(m, n) {
				break
			}
		}
		<-release
		return &gitlab.Issue{IID: issueID}, nil
	}

	args := make([]string, 4*api.MaxConcurrentRequests)
	for i := range args {
		args[i] = strconv.Itoa(i + 1)
	}

	type result struct {
		issues []*gitlab.Issue
		err    error
	}
	done := make(chan result)
	go func() {
		issues, _, err := IssuesFromArgs(t.Context(), nil, nil, f.BaseRepo, glinstance.DefaultHostname, nil, args)
		done <- result{issues, err}
	}()

	require.Eventually(t, func() bool {
		return inFlight.Load() == api.MaxConcurrentRequests
	}, time.Second, time.Millisecond)
	close(release)
	res := <-done

	require.NoError(t, res.err)
	require.Len(t, res.issues, len(args))
	assert.Equal(t, int32(api.MaxConcurrentRequests), maxInFlight.Load())
}
