//go:build !integration

package add

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
	"gitlab.com/gitlab-org/cli/test"
)

var testProject = &gitlab.Project{
	ID:                1,
	Name:              "my-project",
	PathWithNamespace: "alice/my-project",
	HTTPURLToRepo:     "https://gitlab.com/alice/my-project.git",
	SSHURLToRepo:      "git@gitlab.com:alice/my-project.git",
}

func remoteNamed(name string) glrepo.Remotes {
	remote := &git.Remote{
		Name: name,
		FetchURL: &url.URL{
			Scheme: "https",
			Host:   "gitlab.com",
			Path:   "/alice/my-project.git",
		},
		PushURL: &url.URL{
			Scheme: "https",
			Host:   "gitlab.com",
			Path:   "/alice/my-project.git",
		},
	}
	repo := glrepo.New("alice", "my-project", glinstance.DefaultHostname)
	return glrepo.Remotes{&glrepo.Remote{Remote: remote, Repo: repo}}
}

func TestDefaultRemoteName(t *testing.T) {
	tests := []struct {
		projectID string
		want      string
	}{
		{"alice/repo", "alice"},
		{"group/subgroup/project", "group"},
		{"a/b/c/d", "a"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, defaultRemoteName(tt.projectID))
	}
}

func TestNewCmdRemoteAdd(t *testing.T) {
	tests := []struct {
		name              string
		args              string
		remotes           glrepo.Remotes
		remotesErr        error
		mockSetup         func(tc *gitlabtesting.TestClient)
		expectedShellouts []string
		wantOut           string
		wantErr           string
	}{
		{
			name:    "adds remote with default name (namespace) using ssh",
			args:    "alice/my-project",
			remotes: glrepo.Remotes{},
			mockSetup: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					GetProject("alice/my-project", gomock.Any(), gomock.Any()).
					Return(testProject, nil, nil)
			},
			expectedShellouts: []string{"git remote add -f alice git@gitlab.com:alice/my-project.git"},
			wantOut:           "✓ Remote \"alice\" added using ssh protocol.\n",
		},
		{
			name:    "adds remote with custom --name flag",
			args:    "alice/my-project --name upstream",
			remotes: glrepo.Remotes{},
			mockSetup: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					GetProject("alice/my-project", gomock.Any(), gomock.Any()).
					Return(testProject, nil, nil)
			},
			expectedShellouts: []string{"git remote add -f upstream git@gitlab.com:alice/my-project.git"},
			wantOut:           "✓ Remote \"upstream\" added using ssh protocol.\n",
		},
		{
			name:    "subgroup path uses first component as remote name",
			args:    "group/subgroup/project",
			remotes: glrepo.Remotes{},
			mockSetup: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					GetProject("group/subgroup/project", gomock.Any(), gomock.Any()).
					Return(&gitlab.Project{
						ID:                2,
						PathWithNamespace: "group/subgroup/project",
						HTTPURLToRepo:     "https://gitlab.com/group/subgroup/project.git",
						SSHURLToRepo:      "git@gitlab.com:group/subgroup/project.git",
					}, nil, nil)
			},
			expectedShellouts: []string{"git remote add -f group git@gitlab.com:group/subgroup/project.git"},
			wantOut:           "✓ Remote \"group\" added using ssh protocol.\n",
		},
		{
			name:    "adds remote using --protocol https flag",
			args:    "alice/my-project --protocol https",
			remotes: glrepo.Remotes{},
			mockSetup: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					GetProject("alice/my-project", gomock.Any(), gomock.Any()).
					Return(testProject, nil, nil)
			},
			expectedShellouts: []string{"git remote add -f alice https://gitlab.com/alice/my-project.git"},
			wantOut:           "✓ Remote \"alice\" added using https protocol.\n",
		},
		{
			name:    "adds remote using --protocol ssh flag",
			args:    "alice/my-project --protocol ssh",
			remotes: glrepo.Remotes{},
			mockSetup: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					GetProject("alice/my-project", gomock.Any(), gomock.Any()).
					Return(testProject, nil, nil)
			},
			expectedShellouts: []string{"git remote add -f alice git@gitlab.com:alice/my-project.git"},
			wantOut:           "✓ Remote \"alice\" added using ssh protocol.\n",
		},
		{
			name:      "fails with invalid protocol value",
			args:      "alice/my-project --protocol ftp",
			mockSetup: func(tc *gitlabtesting.TestClient) {},
			wantErr:   "invalid protocol",
		},
		{
			name:      "fails without slash in project reference",
			args:      "my-project",
			mockSetup: func(tc *gitlabtesting.TestClient) {},
			wantErr:   `invalid project reference`,
		},
		{
			name:      "fails when remote name already exists",
			args:      "alice/my-project --name origin",
			remotes:   remoteNamed("origin"),
			mockSetup: func(tc *gitlabtesting.TestClient) {},
			wantErr:   `remote "origin" already exists`,
		},
		{
			name:       "fails when remotes cannot be listed",
			args:       "alice/my-project",
			remotesErr: errors.New("git remote failed"),
			mockSetup:  func(tc *gitlabtesting.TestClient) {},
			wantErr:    "failed to list remotes: git remote failed",
		},
		{
			name:    "fails when project not found",
			args:    "alice/missing-project",
			remotes: glrepo.Remotes{},
			mockSetup: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					GetProject("alice/missing-project", gomock.Any(), gomock.Any()).
					Return(nil, nil, fmt.Errorf("404 Not Found"))
			},
			wantErr: `failed to find project "alice/missing-project": 404 Not Found`,
		},
		{
			name:    "works in empty repo with no remotes",
			args:    "alice/my-project",
			remotes: nil,
			mockSetup: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					GetProject("alice/my-project", gomock.Any(), gomock.Any()).
					Return(testProject, nil, nil)
			},
			expectedShellouts: []string{"git remote add -f alice git@gitlab.com:alice/my-project.git"},
			wantOut:           "✓ Remote \"alice\" added using ssh protocol.\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs, csTeardown := test.InitCmdStubber()
			defer csTeardown()

			for _, stub := range tt.expectedShellouts {
				cs.Stub(stub)
			}

			tc := gitlabtesting.NewTestClient(t)
			tt.mockSetup(tc)

			exec := cmdtest.SetupCmdForTest(t, NewCmdRemoteAdd, false,
				cmdtest.WithApiClient(
					cmdtest.NewTestApiClient(t, nil, "", glinstance.DefaultHostname, api.WithGitLabClient(tc.Client)),
				),
				func(f *cmdtest.Factory) {
					f.RemotesStub = func() (glrepo.Remotes, error) {
						return tt.remotes, tt.remotesErr
					}
				},
			)

			out, err := exec(tt.args)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantOut, out.OutBuf.String())

			assert.Equal(t, len(tt.expectedShellouts), cs.Count)
			for i, expected := range tt.expectedShellouts {
				assert.Equal(t, expected, strings.Join(cs.Calls[i].Args, " "))
			}
		})
	}
}
