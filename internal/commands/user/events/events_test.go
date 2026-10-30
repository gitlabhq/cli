//go:build !integration

package events

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestUserEvents_HeaderTotal(t *testing.T) {
	t.Parallel()

	events := []*gitlab.ContributionEvent{
		{ProjectID: 1, ActionName: "opened", TargetType: "Issue", TargetTitle: "new issue"},
	}

	tests := []struct {
		name       string
		resp       *gitlab.Response
		wantHeader string
	}{
		{
			name:       "shows the total from the API",
			resp:       &gitlab.Response{CurrentPage: 2, TotalPages: 61, TotalItems: 61},
			wantHeader: "Showing 1 of 61 user events on all projects. (Page 2)",
		},
		{
			name:       "without a total from the API",
			resp:       &gitlab.Response{CurrentPage: 2, NextPage: 3},
			wantHeader: "Showing 1 user event on all projects. (Page 2)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tc := gitlabtesting.NewTestClient(t)
			tc.MockEvents.EXPECT().
				ListCurrentUserContributionEvents(gomock.Any()).
				Return(events, tt.resp, nil)
			tc.MockProjects.EXPECT().
				GetProject(int64(1), gomock.Any()).
				Return(&gitlab.Project{ID: 1, NameWithNamespace: "OWNER / REPO"}, nil, nil)

			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdEvents,
				false,
				cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
			)

			output, err := exec("--all --page 2 --per-page 1")
			require.NoError(t, err)

			header, _, _ := strings.Cut(output.String(), "\n")
			assert.Equal(t, tt.wantHeader, header)
			assert.Empty(t, output.Stderr())
		})
	}
}
