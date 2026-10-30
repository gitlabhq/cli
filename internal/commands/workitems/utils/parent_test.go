package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/config"
)

func TestParseWorkItemRef(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		ref      string
		wantHost string
		wantPath string
		wantIID  int64
		wantErr  bool
	}{
		{name: "bare IID", ref: "42", wantPath: "OWNER/REPO", wantIID: 42},
		{name: "IID with hash", ref: "#42", wantPath: "OWNER/REPO", wantIID: 42},
		{name: "project reference", ref: "group/sub/project#42", wantPath: "group/sub/project", wantIID: 42},
		{name: "epic reference", ref: "group&7", wantPath: "group", wantIID: 7},
		{name: "project work item URL", ref: "https://gitlab.com/group/project/-/work_items/42", wantPath: "group/project", wantIID: 42},
		{name: "project issue URL", ref: "https://gitlab.com/group/project/-/issues/42#note_1", wantPath: "group/project", wantIID: 42},
		{name: "project issue URL without dash", ref: "https://gitlab.com/group/project/issues/42", wantPath: "group/project", wantIID: 42},
		{name: "incident URL", ref: "https://gitlab.com/group/sub/project/-/issues/incident/42", wantPath: "group/sub/project", wantIID: 42},
		{name: "group work item URL", ref: "https://gitlab.com/groups/group/sub/-/work_items/7", wantPath: "group/sub", wantIID: 7},
		{name: "group epic URL without dash", ref: "https://gitlab.com/groups/group/sub/epics/7", wantPath: "group/sub", wantIID: 7},
		{name: "group epic URL", ref: "https://gitlab.com/groups/group/-/epics/7", wantPath: "group", wantIID: 7},
		{name: "self-managed URL", ref: "https://gitlab.example.com/group/project/-/work_items/42", wantHost: "gitlab.example.com", wantPath: "group/project", wantIID: 42},
		{name: "unsupported URL", ref: "https://gitlab.com/group/project/-/merge_requests/42", wantErr: true},
		{name: "non-numeric", ref: "abc", wantErr: true},
		{name: "zero", ref: "0", wantErr: true},
		{name: "empty", ref: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			host, path, iid, err := parseWorkItemRef(tt.ref, "OWNER/REPO", nil)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.wantHost != "" {
				assert.Equal(t, tt.wantHost, host)
			}
			assert.Equal(t, tt.wantPath, path)
			assert.Equal(t, tt.wantIID, iid)
		})
	}
}

func TestResolveParentID_Host(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ref     string
		wantErr string
	}{
		{name: "web host mapped to the configured api_host", ref: "https://gitlab.example.com/group/project/-/work_items/42"},
		{name: "another instance", ref: "https://other.example.com/group/project/-/work_items/42", wantErr: "is not on api.example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := config.NewBlankConfig()
			require.NoError(t, cfg.Set("gitlab.example.com", "api_host", "api.example.com"))

			tc := gitlabtesting.NewTestClient(t, gitlab.WithBaseURL("https://api.example.com/api/v4"))
			if tt.wantErr == "" {
				tc.MockWorkItems.EXPECT().
					GetWorkItem("group/project", int64(42), gomock.Any()).
					Return(&gitlab.WorkItem{ID: 9001}, &gitlab.Response{}, nil)
			}

			id, err := ResolveParentID(t.Context(), tc.Client, cfg, tt.ref, "OWNER/REPO")
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, int64(9001), id)
		})
	}
}
