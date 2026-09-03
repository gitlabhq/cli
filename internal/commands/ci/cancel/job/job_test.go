//go:build !integration

package job

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestCIPipelineCancelWithoutArgument(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(t, NewCmdCancel, false)

	out, err := exec("")
	require.EqualError(t, err, "you must pass a job ID")

	assert.Empty(t, out.OutBuf.String())
	assert.Empty(t, out.ErrBuf.String())
}

func TestCIDryRunDeleteNothing(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(t, NewCmdCancel, false)

	out, err := exec("--dry-run 11111111,22222222")
	require.NoError(t, err)

	stdout := out.OutBuf.String()
	assert.Contains(t, stdout, "Job #11111111 will be canceled.")
	assert.Contains(t, stdout, "Job #22222222 will be canceled.")
	assert.Empty(t, out.ErrBuf.String())
}

func TestCIJobCancel(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	gomock.InOrder(
		tc.MockProjects.EXPECT().GetProject("OWNER/REPO", gomock.Any()).Return(&gitlab.Project{ID: 123}, nil, nil),
		tc.MockJobs.EXPECT().CancelJob(int64(123), int64(11111111)).Return(nil, nil, nil),
	)
	exec := cmdtest.SetupCmdForTest(t, NewCmdCancel, false, cmdtest.WithGitLabClient(tc.Client))

	out, err := exec("11111111")
	require.NoError(t, err)

	assert.Contains(t, out.OutBuf.String(), "Job #11111111 is canceled successfully.")
	assert.Empty(t, out.ErrBuf.String())
}

func TestCIJobCancelMultiple(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	// The project is looked up once and reused for every job in the batch.
	gomock.InOrder(
		tc.MockProjects.EXPECT().GetProject("OWNER/REPO", gomock.Any()).Return(&gitlab.Project{ID: 123}, nil, nil),
		tc.MockJobs.EXPECT().CancelJob(int64(123), int64(11111111)).Return(nil, nil, nil),
		tc.MockJobs.EXPECT().CancelJob(int64(123), int64(22222222)).Return(nil, nil, nil),
	)
	exec := cmdtest.SetupCmdForTest(t, NewCmdCancel, false, cmdtest.WithGitLabClient(tc.Client))

	out, err := exec("11111111,22222222")
	require.NoError(t, err)

	stdout := out.OutBuf.String()
	assert.Contains(t, stdout, "Job #11111111 is canceled successfully.")
	assert.Contains(t, stdout, "Job #22222222 is canceled successfully.")
	assert.Empty(t, out.ErrBuf.String())
}

func TestCIJobCancelError(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	gomock.InOrder(
		tc.MockProjects.EXPECT().GetProject("OWNER/REPO", gomock.Any()).Return(&gitlab.Project{ID: 123}, nil, nil),
		tc.MockJobs.EXPECT().CancelJob(int64(123), int64(11111111)).Return(nil, nil, errors.New(`{"message": "404 Not found"}`)),
	)
	exec := cmdtest.SetupCmdForTest(t, NewCmdCancel, false, cmdtest.WithGitLabClient(tc.Client))

	out, err := exec("11111111")
	require.Error(t, err)

	assert.Empty(t, out.OutBuf.String())
	assert.Empty(t, out.ErrBuf.String())
}

func TestCIJobCancelForceAndDryRunMutuallyExclusive(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(t, NewCmdCancel, false)

	out, err := exec("11111111 --force --dry-run")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "if any flags in the group [dry-run force] are set none of the others can be")

	assert.Empty(t, out.OutBuf.String())
	assert.Empty(t, out.ErrBuf.String())
}

func TestCIJobCancelWithForce(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	gomock.InOrder(
		tc.MockProjects.EXPECT().GetProject("OWNER/REPO", gomock.Any()).Return(&gitlab.Project{ID: 123}, nil, nil),
		tc.MockJobs.EXPECT().CancelJobWithOptions(int64(123), int64(11111111), &gitlab.CancelJobOptions{
			Force: new(true),
		}).Return(nil, nil, nil),
	)
	exec := cmdtest.SetupCmdForTest(t, NewCmdCancel, false, cmdtest.WithGitLabClient(tc.Client))

	out, err := exec("11111111 --force")
	require.NoError(t, err)

	assert.Contains(t, out.OutBuf.String(), "Job #11111111 is canceled successfully.")
	assert.Empty(t, out.ErrBuf.String())
}

func TestCIJobCancelWithForceMultiple(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	// The project is looked up once and reused for every job in the batch.
	gomock.InOrder(
		tc.MockProjects.EXPECT().GetProject("OWNER/REPO", gomock.Any()).Return(&gitlab.Project{ID: 123}, nil, nil),
		tc.MockJobs.EXPECT().CancelJobWithOptions(int64(123), int64(11111111), &gitlab.CancelJobOptions{
			Force: new(true),
		}).Return(nil, nil, nil),
		tc.MockJobs.EXPECT().CancelJobWithOptions(int64(123), int64(22222222), &gitlab.CancelJobOptions{
			Force: new(true),
		}).Return(nil, nil, nil),
	)
	exec := cmdtest.SetupCmdForTest(t, NewCmdCancel, false, cmdtest.WithGitLabClient(tc.Client))

	out, err := exec("11111111,22222222 --force")
	require.NoError(t, err)

	stdout := out.OutBuf.String()
	assert.Contains(t, stdout, "Job #11111111 is canceled successfully.")
	assert.Contains(t, stdout, "Job #22222222 is canceled successfully.")
	assert.Empty(t, out.ErrBuf.String())
}
