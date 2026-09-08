//go:build !integration

package list

import (
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestRunnerList_Project(t *testing.T) {
	t.Setenv("NO_COLOR", "true")

	tc := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	tc.MockRunners.EXPECT().
		ListProjectRunners("OWNER/REPO", &gitlab.ListProjectRunnersOptions{
			ListOptions: gitlab.ListOptions{Page: 1, PerPage: 30},
		}, gomock.Any()).
		Return([]*gitlab.Runner{
			{ID: 1, Description: "runner-1", Status: "online", RunnerType: "project_type", Paused: false},
			{ID: 2, Description: "runner-2", Status: "offline", RunnerType: "project_type", Paused: true},
		}, nil, nil)

	out, err := exec("")
	require.NoError(t, err)

	assert.Equal(t, heredoc.Doc(`
		Showing 2 runners on OWNER/REPO. (Page 1)

		ID	Description	Status	Paused
		1	runner-1	online	false
		2	runner-2	offline	true

	`), out.String())
	assert.Empty(t, out.Stderr())
}

func TestRunnerList_Group(t *testing.T) {
	t.Setenv("NO_COLOR", "true")

	tc := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	tc.MockRunners.EXPECT().
		ListGroupsRunners("mygroup", &gitlab.ListGroupsRunnersOptions{
			ListOptions: gitlab.ListOptions{Page: 1, PerPage: 30},
		}, gomock.Any()).
		Return([]*gitlab.Runner{
			{ID: 10, Description: "group-runner", Status: "online", RunnerType: "group_type", Paused: false},
		}, nil, nil)

	out, err := exec("--group mygroup")
	require.NoError(t, err)

	assert.Equal(t, heredoc.Doc(`
		Showing 1 runner on mygroup. (Page 1)

		ID	Description	Status	Paused
		10	group-runner	online	false

	`), out.String())
	assert.Empty(t, out.Stderr())
}

func TestRunnerList_Instance(t *testing.T) {
	t.Setenv("NO_COLOR", "true")

	tc := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	tc.MockRunners.EXPECT().
		ListRunners(&gitlab.ListRunnersOptions{
			ListOptions: gitlab.ListOptions{Page: 1, PerPage: 30},
		}, gomock.Any()).
		Return([]*gitlab.Runner{
			{ID: 100, Description: "instance-runner", Status: "online", RunnerType: "instance_type", Paused: false},
		}, nil, nil)

	out, err := exec("--instance")
	require.NoError(t, err)

	assert.Equal(t, heredoc.Doc(`
		Showing 1 runner on instance. (Page 1)

		ID	Description	Status	Paused
		100	instance-runner	online	false

	`), out.String())
	assert.Empty(t, out.Stderr())
}

func TestRunnerList_Pagination(t *testing.T) {
	t.Setenv("NO_COLOR", "true")

	tc := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	tc.MockRunners.EXPECT().
		ListProjectRunners("OWNER/REPO", &gitlab.ListProjectRunnersOptions{
			ListOptions: gitlab.ListOptions{Page: 2, PerPage: 5},
		}, gomock.Any()).
		Return([]*gitlab.Runner{
			{ID: 3, Description: "runner-p3", Status: "online", RunnerType: "project_type", Paused: false},
		}, nil, nil)

	out, err := exec("--page 2 --per-page 5")
	require.NoError(t, err)

	assert.Equal(t, heredoc.Doc(`
		Showing 1 runner on OWNER/REPO. (Page 2)

		ID	Description	Status	Paused
		3	runner-p3	online	false

	`), out.String())
	assert.Empty(t, out.Stderr())
}

func TestRunnerList_JSON(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
	)

	tc.MockRunners.EXPECT().
		ListProjectRunners("OWNER/REPO", &gitlab.ListProjectRunnersOptions{
			ListOptions: gitlab.ListOptions{Page: 1, PerPage: 30},
		}, gomock.Any()).
		Return([]*gitlab.Runner{
			{ID: 1, Description: "runner-1", Status: "online", RunnerType: "project_type", Paused: false},
		}, nil, nil)

	out, err := exec("--output json")
	require.NoError(t, err)

	// Compact JSON (no space after colons) from PrintJSON
	assert.Contains(t, out.String(), `"id":1`)
	assert.Contains(t, out.String(), `"description":"runner-1"`)
	assert.Contains(t, out.String(), `"status":"online"`)
	assert.Empty(t, out.Stderr())
}
