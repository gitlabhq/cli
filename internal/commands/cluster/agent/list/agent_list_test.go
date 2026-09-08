//go:build !integration

package list

import (
	"testing"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlab_testing "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestAgentList(t *testing.T) {
	// GIVEN
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	tc := gitlab_testing.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(t, NewCmdAgentList, false, cmdtest.WithGitLabClient(tc.Client))

	tc.MockClusterAgents.EXPECT().
		ListAgents("OWNER/REPO", &gitlab.ListAgentsOptions{ListOptions: gitlab.ListOptions{Page: 1, PerPage: 30}}).
		Return([]*gitlab.Agent{
			{
				ID:        1,
				Name:      "local",
				CreatedAt: new(time.Now().Add(-24 * time.Hour)),
			},
			{
				ID:        2,
				Name:      "prd",
				CreatedAt: new(time.Now().Add(-24 * time.Hour)),
			},
		}, &gitlab.Response{}, nil).
		Times(1)

	// WHEN
	output, err := exec("")
	if err != nil {
		t.Errorf("error running command `cluster agent list`: %v", err)
	}

	// THEN
	assert.Equal(t, heredoc.Doc(`
		Showing 2 agents on OWNER/REPO. (Page 1)

		ID	Name	Created At
		1	local	about 1 day ago
		2	prd	about 1 day ago

	`), output.String())
	assert.Empty(t, output.Stderr())
}

func TestAgentList_Pagination(t *testing.T) {
	// GIVEN
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	tc := gitlab_testing.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(t, NewCmdAgentList, false, cmdtest.WithGitLabClient(tc.Client))

	tc.MockClusterAgents.EXPECT().
		ListAgents("OWNER/REPO", &gitlab.ListAgentsOptions{ListOptions: gitlab.ListOptions{Page: 2, PerPage: 1}}).
		Return([]*gitlab.Agent{
			{
				ID:        2,
				Name:      "prd",
				CreatedAt: new(time.Now().Add(-24 * time.Hour)),
			},
		}, &gitlab.Response{NextPage: 0}, nil)

	// WHEN
	output, err := exec("--page 2 --per-page 1")
	if err != nil {
		t.Errorf("error running command `cluster agent list`: %v", err)
	}

	assert.Equal(t, heredoc.Doc(`
		Showing 1 agent on OWNER/REPO. (Page 2)

		ID	Name	Created At
		2	prd	about 1 day ago

	`), output.String())
	assert.Empty(t, output.Stderr())
}

func TestAgentList_JSON(t *testing.T) {
	t.Parallel()

	tc := gitlab_testing.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(t, NewCmdAgentList, false, cmdtest.WithGitLabClient(tc.Client))

	tc.MockClusterAgents.EXPECT().
		ListAgents("OWNER/REPO", &gitlab.ListAgentsOptions{ListOptions: gitlab.ListOptions{Page: 1, PerPage: 30}}).
		Return([]*gitlab.Agent{
			{
				ID:        1,
				Name:      "local",
				CreatedAt: new(time.Now().Add(-24 * time.Hour)),
			},
			{
				ID:        2,
				Name:      "prd",
				CreatedAt: new(time.Now().Add(-24 * time.Hour)),
			},
		}, &gitlab.Response{}, nil)

	output, err := exec("--output json")
	require.NoError(t, err)

	assert.Contains(t, output.String(), `"id":1`)
	assert.Contains(t, output.String(), `"name":"local"`)
	assert.Contains(t, output.String(), `"id":2`)
	assert.Contains(t, output.String(), `"name":"prd"`)
	assert.Empty(t, output.Stderr())
}
