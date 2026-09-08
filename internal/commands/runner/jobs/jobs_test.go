//go:build !integration

package jobs

import (
	"encoding/json"
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

func TestRunnerJobs_Success(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	jobsResponse := []*gitlab.Job{
		{ID: 2, Status: "success", Stage: "test", Name: "test", Ref: "main", Project: &gitlab.Project{PathWithNamespace: "owner/repo"}},
	}

	tc.MockRunners.EXPECT().
		ListRunnerJobs(int64(9), gomock.Any(), gomock.Any()).
		Return(jobsResponse, nil, nil)

	out, err := exec("9")
	require.NoError(t, err)

	expectedOutput := heredoc.Docf(`
		Showing 1 job on . (Page 1)

		ID%[1]sStatus%[1]sStage%[1]sName%[1]sRef%[1]sProject
		2%[1]ssuccess%[1]stest%[1]stest%[1]smain%[1]sowner/repo

	`, "\t")
	assert.Equal(t, expectedOutput, out.OutBuf.String())
}

func TestRunnerJobs_OutputJSON(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	jobsResponse := []*gitlab.Job{
		{ID: 7, Status: "running", Stage: "build", Name: "compile", Ref: "feature", Project: &gitlab.Project{PathWithNamespace: "group/proj"}},
	}

	tc.MockRunners.EXPECT().
		ListRunnerJobs(int64(3), gomock.Any(), gomock.Any()).
		Return(jobsResponse, nil, nil)

	out, err := exec("3 --output json")
	require.NoError(t, err)
	var decoded []*gitlab.Job
	err = json.Unmarshal(out.OutBuf.Bytes(), &decoded)
	require.NoError(t, err)
	require.Len(t, decoded, 1)
	assert.Equal(t, int64(7), decoded[0].ID)
	assert.Equal(t, "running", decoded[0].Status)
	assert.Equal(t, "build", decoded[0].Stage)
	assert.Equal(t, "compile", decoded[0].Name)
	assert.Equal(t, "feature", decoded[0].Ref)
	require.NotNil(t, decoded[0].Project)
	assert.Equal(t, "group/proj", decoded[0].Project.PathWithNamespace)
}

func TestRunnerJobs_InvalidRunnerID(t *testing.T) {
	t.Parallel()
	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
	_, err := exec("invalid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
}

func TestRunnerJobs_WithStatus(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(tc.Client))),
	)

	tc.MockRunners.EXPECT().
		ListRunnerJobs(int64(5), gomock.Any(), gomock.Any()).
		Return([]*gitlab.Job{}, nil, nil)

	out, err := exec("5 --status running")
	require.NoError(t, err)

	expectedOutput := heredoc.Docf(`
		No jobs available on .
		ID%[1]sStatus%[1]sStage%[1]sName%[1]sRef%[1]sProject

	`, "\t")
	assert.Equal(t, expectedOutput, out.OutBuf.String())
}
