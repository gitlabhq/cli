//go:build !integration

package mirror

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestProjectMirror_PullMirror(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	// Mock getting the project details
	tc.MockProjects.EXPECT().
		GetProject("foo/bar", gomock.Any(), gomock.Any()).
		Return(&gitlab.Project{
			ID:                123,
			PathWithNamespace: "foo/bar",
		}, nil, nil)

	// Mock updating the project to enable pull mirroring
	tc.MockProjects.EXPECT().
		EditProject(int64(123), gomock.Any(), gomock.Any()).
		Return(&gitlab.Project{
			ID:        123,
			ImportURL: "https://gitlab.example.com/source/repo",
			Mirror:    true,
		}, nil, nil)

	exec := cmdtest.SetupCmdForTest(t, NewCmdMirror, true,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("foo", "bar", glinstance.DefaultHostname),
	)

	output, err := exec(`--direction=pull --url="https://gitlab.example.com/source/repo"`)

	require.NoError(t, err)
	assert.Contains(t, output.String(), "Created pull mirror")
	assert.Contains(t, output.String(), "foo/bar")
}

func TestProjectMirror_PushMirror(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	// Mock getting the project details
	tc.MockProjects.EXPECT().
		GetProject("foo/bar", gomock.Any(), gomock.Any()).
		Return(&gitlab.Project{
			ID:                123,
			PathWithNamespace: "foo/bar",
		}, nil, nil)

	// Mock creating a push mirror
	tc.MockProjectMirrors.EXPECT().
		AddProjectMirror(int64(123), gomock.Any(), gomock.Any()).
		Return(&gitlab.ProjectMirror{
			ID:      456,
			URL:     "https://gitlab-backup.example.com/target/repo",
			Enabled: true,
		}, nil, nil)

	exec := cmdtest.SetupCmdForTest(t, NewCmdMirror, true,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("foo", "bar", glinstance.DefaultHostname),
	)

	output, err := exec(`--direction=push --url="https://gitlab-backup.example.com/target/repo"`)

	require.NoError(t, err)
	assert.Contains(t, output.String(), "Created push mirror")
	assert.Contains(t, output.String(), "foo/bar")
}

func TestProjectMirror_ProjectNotFound(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	// Mock project not found
	tc.MockProjects.EXPECT().
		GetProject("foo/bar", gomock.Any(), gomock.Any()).
		Return(nil, nil, errors.New("404 Project Not Found"))

	exec := cmdtest.SetupCmdForTest(t, NewCmdMirror, true,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("foo", "bar", glinstance.DefaultHostname),
	)

	_, err := exec(`--direction=pull --url="https://gitlab.example.com/source/repo"`)

	// Should error when project is not found
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

func TestProjectMirror_AllowDivergenceWithPull(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	// Mock getting the project details
	tc.MockProjects.EXPECT().
		GetProject("foo/bar", gomock.Any(), gomock.Any()).
		Return(&gitlab.Project{
			ID:                123,
			PathWithNamespace: "foo/bar",
		}, nil, nil)

	// Mock updating the project
	tc.MockProjects.EXPECT().
		EditProject(int64(123), gomock.Any(), gomock.Any()).
		Return(&gitlab.Project{
			ID:        123,
			ImportURL: "https://gitlab.example.com/source/repo",
			Mirror:    true,
		}, nil, nil)

	exec := cmdtest.SetupCmdForTest(t, NewCmdMirror, true,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("foo", "bar", glinstance.DefaultHostname),
	)

	output, err := exec(`--direction=pull --url="https://gitlab.example.com/source/repo" --allow-divergence`)

	require.NoError(t, err)
	// Should show warning that allow-divergence has no effect for pull mirrors
	assert.Contains(t, output.String(), "[Warning]")
	assert.Contains(t, output.String(), "allow-divergence")
	assert.Contains(t, output.String(), "pull mirroring")
}

func TestProjectMirror_ProtectedBranchesOnly(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)

	// Mock getting the project details
	tc.MockProjects.EXPECT().
		GetProject("foo/bar", gomock.Any(), gomock.Any()).
		Return(&gitlab.Project{
			ID:                123,
			PathWithNamespace: "foo/bar",
		}, nil, nil)

	// Mock creating a push mirror with protected branches only
	tc.MockProjectMirrors.EXPECT().
		AddProjectMirror(int64(123), gomock.Any(), gomock.Any()).
		Return(&gitlab.ProjectMirror{
			ID:                    456,
			URL:                   "https://gitlab-backup.example.com/target/repo",
			Enabled:               true,
			OnlyProtectedBranches: true,
		}, nil, nil)

	exec := cmdtest.SetupCmdForTest(t, NewCmdMirror, true,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("foo", "bar", glinstance.DefaultHostname),
	)

	output, err := exec(`--direction=push --url="https://gitlab-backup.example.com/target/repo" --protected-branches-only`)

	require.NoError(t, err)
	assert.Contains(t, output.String(), "Created push mirror")
}
