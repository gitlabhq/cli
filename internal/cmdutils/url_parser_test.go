//go:build !integration

package cmdutils

import (
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
)

func TestParseGitLabURL(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		wantID      int
		wantType    GitLabResourceType
		wantRepoURL string
	}{
		// Merge Request URLs
		{
			name:        "valid MR URL",
			url:         "https://gitlab.com/gitlab-org/cli/-/merge_requests/1234",
			wantID:      1234,
			wantType:    GitLabResourceMergeRequest,
			wantRepoURL: "https://gitlab.com/gitlab-org/cli/",
		},
		{
			name:        "valid MR URL without dash",
			url:         "https://gitlab.com/gitlab-org/cli/merge_requests/1234",
			wantID:      1234,
			wantType:    GitLabResourceMergeRequest,
			wantRepoURL: "https://gitlab.com/gitlab-org/cli/",
		},
		{
			name:        "valid MR URL with nested subgroup",
			url:         "https://gitlab.com/namespace/project/subproject/repo/-/merge_requests/100",
			wantID:      100,
			wantType:    GitLabResourceMergeRequest,
			wantRepoURL: "https://gitlab.com/namespace/project/subproject/repo/",
		},
		{
			name:        "valid MR URL with very deep nested subgroups",
			url:         "https://gitlab.com/gitlab-org/subgroup/othersubgroup/oh/please/make/it/stop/-/merge_requests/2091",
			wantID:      2091,
			wantType:    GitLabResourceMergeRequest,
			wantRepoURL: "https://gitlab.com/gitlab-org/subgroup/othersubgroup/oh/please/make/it/stop/",
		},
		{
			name:        "valid MR URL with self-managed instance (invent.kde.org)",
			url:         "https://invent.kde.org/kde/krita/-/merge_requests/42",
			wantID:      42,
			wantType:    GitLabResourceMergeRequest,
			wantRepoURL: "https://invent.kde.org/kde/krita/",
		},
		{
			name:        "valid MR URL with self-managed instance",
			url:         "https://gitlab.example.com/org/project/-/merge_requests/42",
			wantID:      42,
			wantType:    GitLabResourceMergeRequest,
			wantRepoURL: "https://gitlab.example.com/org/project/",
		},
		{
			name:        "MR URL with fragment (note)",
			url:         "https://gitlab.com/gitlab-org/cli/-/merge_requests/2091#note_2510904692",
			wantID:      2091,
			wantType:    GitLabResourceMergeRequest,
			wantRepoURL: "https://gitlab.com/gitlab-org/cli/",
		},
		{
			name:        "MR URL with query parameters",
			url:         "https://gitlab.com/gitlab-org/cli/-/merge_requests/1234?tab=notes",
			wantID:      1234,
			wantType:    GitLabResourceMergeRequest,
			wantRepoURL: "https://gitlab.com/gitlab-org/cli/",
		},
		{
			name:        "MR URL with both query and fragment",
			url:         "https://gitlab.com/gitlab-org/cli/-/merge_requests/1234?tab=notes#note_123",
			wantID:      1234,
			wantType:    GitLabResourceMergeRequest,
			wantRepoURL: "https://gitlab.com/gitlab-org/cli/",
		},
		{
			name:        "MR URL with trailing slash",
			url:         "https://gitlab.com/gitlab-org/cli/-/merge_requests/1234/",
			wantID:      1234,
			wantType:    GitLabResourceMergeRequest,
			wantRepoURL: "https://gitlab.com/gitlab-org/cli/",
		},
		// Issue URLs
		{
			name:        "valid issue URL",
			url:         "https://gitlab.com/namespace/repo/-/issues/1",
			wantID:      1,
			wantType:    GitLabResourceIssue,
			wantRepoURL: "https://gitlab.com/namespace/repo/",
		},
		{
			name:        "valid issue URL without dash",
			url:         "https://gitlab.com/namespace/repo/issues/1",
			wantID:      1,
			wantType:    GitLabResourceIssue,
			wantRepoURL: "https://gitlab.com/namespace/repo/",
		},
		{
			name:        "valid issue URL with nested subgroup",
			url:         "https://gitlab.com/namespace/project/subproject/repo/-/issues/100",
			wantID:      100,
			wantType:    GitLabResourceIssue,
			wantRepoURL: "https://gitlab.com/namespace/project/subproject/repo/",
		},
		{
			name:        "valid incident URL",
			url:         "https://gitlab.com/namespace/repo/-/issues/incident/1",
			wantID:      1,
			wantType:    GitLabResourceIssue,
			wantRepoURL: "https://gitlab.com/namespace/repo/",
		},
		{
			name:        "valid incident URL without dash",
			url:         "https://gitlab.com/namespace/repo/issues/incident/1",
			wantID:      1,
			wantType:    GitLabResourceIssue,
			wantRepoURL: "https://gitlab.com/namespace/repo/",
		},
		{
			name:        "valid work item URL",
			url:         "https://gitlab.com/gitlab-org/gitlab/-/work_items/337780",
			wantID:      337780,
			wantType:    GitLabResourceIssue,
			wantRepoURL: "https://gitlab.com/gitlab-org/gitlab/",
		},
		{
			name:        "valid work item URL without dash",
			url:         "https://gitlab.com/namespace/repo/work_items/1",
			wantID:      1,
			wantType:    GitLabResourceIssue,
			wantRepoURL: "https://gitlab.com/namespace/repo/",
		},
		{
			name:        "issue URL with trailing slash",
			url:         "https://gitlab.com/namespace/repo/-/issues/1/",
			wantID:      1,
			wantType:    GitLabResourceIssue,
			wantRepoURL: "https://gitlab.com/namespace/repo/",
		},
		{
			name:        "issue URL with query and fragment",
			url:         "https://gitlab.com/namespace/repo/-/issues/1?foo=bar#note_123",
			wantID:      1,
			wantType:    GitLabResourceIssue,
			wantRepoURL: "https://gitlab.com/namespace/repo/",
		},
		{
			name:        "issue URL over http",
			url:         "http://gitlab.example.com/namespace/repo/-/issues/1",
			wantID:      1,
			wantType:    GitLabResourceIssue,
			wantRepoURL: "https://gitlab.example.com/namespace/repo/",
		},
		{
			name:        "scheme-less issue URL",
			url:         "//gitlab.com/namespace/repo/-/issues/1",
			wantID:      1,
			wantType:    GitLabResourceIssue,
			wantRepoURL: "https://gitlab.com/namespace/repo/",
		},
		{
			name:        "issue URL with trailing merge_requests segment resolves as merge request",
			url:         "https://gitlab.com/namespace/repo/issues/12/merge_requests/3",
			wantID:      3,
			wantType:    GitLabResourceMergeRequest,
			wantRepoURL: "https://gitlab.com/namespace/repo/issues/12/",
		},
		// Invalid URLs
		{
			name:     "invalid URL - no resource ID",
			url:      "https://gitlab.com/gitlab-org/cli/-/merge_requests/",
			wantID:   0,
			wantType: "",
		},
		{
			name:     "invalid URL - not a GitLab resource",
			url:      "https://gitlab.com/gitlab-org/cli",
			wantID:   0,
			wantType: "",
		},
		{
			name:     "invalid URL - wrong protocol",
			url:      "ftp://gitlab.com/gitlab-org/cli/-/merge_requests/123",
			wantID:   0,
			wantType: "",
		},
		{
			name:     "invalid URL - malformed",
			url:      "not-a-url",
			wantID:   0,
			wantType: "",
		},
		{
			name:     "invalid URL - missing repo name",
			url:      "https://gitlab.com/gitlab-org/-/merge_requests/123",
			wantID:   0,
			wantType: "",
		},
		{
			name:     "invalid issue URL - no issue number",
			url:      "https://gitlab.com/namespace/repo/issues",
			wantID:   0,
			wantType: "",
		},
		{
			name:     "invalid incident URL - no incident number",
			url:      "https://gitlab.com/namespace/repo/issues/incident",
			wantID:   0,
			wantType: "",
		},
		{
			name:     "invalid issue URL - missing repo name",
			url:      "https://gitlab.com/namespace/issues/100",
			wantID:   0,
			wantType: "",
		},
		{
			name:     "invalid issue URL - wrong protocol",
			url:      "ftp://gitlab.com/namespace/repo/-/issues/1",
			wantID:   0,
			wantType: "",
		},
		{
			name:     "invalid issue URL - no hostname",
			url:      "/namespace/repo/-/issues/1",
			wantID:   0,
			wantType: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseGitLabURL(tt.url, glinstance.DefaultHostname, config.NewBlankConfig())

			if tt.wantID == 0 {
				require.Nil(t, result, "Expected nil result for invalid URL")
				return
			}

			require.NotNil(t, result, "Expected non-nil result for valid URL")
			require.Equal(t, tt.wantID, result.ID)
			require.Equal(t, tt.wantType, result.Type)

			if tt.wantRepoURL != "" {
				expectedRepo, err := glrepo.FromFullName(tt.wantRepoURL, glinstance.DefaultHostname, config.NewBlankConfig())
				require.NoError(t, err)
				require.Equal(t, expectedRepo, result.Repo)
			}
		})
	}
}

func TestParseMergeRequestFromURL(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		wantID      int
		wantRepoURL string
	}{
		{
			name:        "valid MR URL",
			url:         "https://gitlab.com/gitlab-org/cli/-/merge_requests/1234",
			wantID:      1234,
			wantRepoURL: "https://gitlab.com/gitlab-org/cli/",
		},
		{
			name:   "invalid URL should return 0",
			url:    "not-a-url",
			wantID: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, repo := ParseMergeRequestFromURL(tt.url, glinstance.DefaultHostname, config.NewBlankConfig())
			require.Equal(t, tt.wantID, id)

			if tt.wantID != 0 && tt.wantRepoURL != "" {
				expectedRepo, err := glrepo.FromFullName(tt.wantRepoURL, glinstance.DefaultHostname, config.NewBlankConfig())
				require.NoError(t, err)
				require.Equal(t, expectedRepo, repo)
			} else {
				require.Nil(t, repo)
			}
		})
	}
}

func TestParseIssueFromURL(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		wantID      int
		wantRepoURL string
	}{
		{
			name:        "valid issue URL",
			url:         "https://gitlab.com/gitlab-org/cli/-/issues/8398",
			wantID:      8398,
			wantRepoURL: "https://gitlab.com/gitlab-org/cli/",
		},
		{
			name:        "valid work item URL",
			url:         "https://gitlab.com/gitlab-org/cli/-/work_items/8398",
			wantID:      8398,
			wantRepoURL: "https://gitlab.com/gitlab-org/cli/",
		},
		{
			name:        "issue URL with trailing merge_requests segment",
			url:         "https://gitlab.com/namespace/repo/issues/12/merge_requests/3",
			wantID:      12,
			wantRepoURL: "https://gitlab.com/namespace/repo/",
		},
		{
			name:   "merge request URL should return 0",
			url:    "https://gitlab.com/gitlab-org/cli/-/merge_requests/1234",
			wantID: 0,
		},
		{
			name:   "wrong protocol should return 0",
			url:    "ftp://gitlab.com/gitlab-org/cli/-/issues/1",
			wantID: 0,
		},
		{
			name:   "URL without hostname should return 0",
			url:    "/gitlab-org/cli/-/issues/1",
			wantID: 0,
		},
		{
			name:   "invalid URL should return 0",
			url:    "not-a-url",
			wantID: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, repo := ParseIssueFromURL(tt.url, glinstance.DefaultHostname, config.NewBlankConfig())
			require.Equal(t, tt.wantID, id)

			if tt.wantID != 0 {
				expectedRepo, err := glrepo.FromFullName(tt.wantRepoURL, glinstance.DefaultHostname, config.NewBlankConfig())
				require.NoError(t, err)
				require.Equal(t, expectedRepo, repo)
			} else {
				require.Nil(t, repo)
			}
		})
	}
}

func TestParseGroupWorkItemFromURL(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		wantGroup string
		wantID    int
	}{
		{name: "work item URL", url: "https://gitlab.com/groups/gitlab-org/-/work_items/7", wantGroup: "gitlab-org", wantID: 7},
		{name: "epic URL in a subgroup", url: "https://gitlab.com/groups/gitlab-org/sub/-/epics/7#note_1", wantGroup: "gitlab-org/sub", wantID: 7},
		{name: "epic URL without dash", url: "https://gitlab.com/groups/gitlab-org/epics/7", wantGroup: "gitlab-org", wantID: 7},
		{name: "project work item URL", url: "https://gitlab.com/gitlab-org/cli/-/work_items/7"},
		{name: "wrong protocol", url: "ftp://gitlab.com/groups/gitlab-org/-/epics/7"},
		{name: "work item URL under a subfolder", url: "https://example.com/gitlab/groups/my-group/-/work_items/7", wantGroup: "my-group", wantID: 7},
		{name: "epic URL under a subfolder", url: "https://example.com/gitlab/groups/my-group/-/epics/7", wantGroup: "my-group", wantID: 7},
	}

	cfg := config.NewBlankConfig()
	require.NoError(t, cfg.Set("example.com", "subfolder", "gitlab"))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			group, id := ParseGroupWorkItemFromURL(tt.url, cfg)
			require.Equal(t, tt.wantGroup, group)
			require.Equal(t, tt.wantID, id)
		})
	}
}
