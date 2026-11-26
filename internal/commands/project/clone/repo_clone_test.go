//go:build !integration

package clone

import (
	"errors"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/shlex"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
	"gitlab.com/gitlab-org/cli/test"
)

func TestMain(m *testing.M) {
	cmdtest.InitTest(m, "repo_clone_test")
}

func TestNewCmdClone(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		args        string
		wantOpts    options
		wantCtxOpts ContextOpts
		wantErr     string
	}{
		{
			name:    "no arguments",
			args:    "",
			wantErr: "specify repository argument, or use the --group flag to specify a group to clone all repos from the group",
		},
		{
			name:     "group clone rooted at leaf",
			args:     "-g 123 -p --leaf-group",
			wantOpts: options{groupName: "123", preserveNamespace: true, leafGroup: true, gitFlags: []string{}},
		},
		{
			name:    "leaf group without namespace preservation",
			args:    "-g 123 --leaf-group",
			wantErr: "--leaf-group requires --group and --preserve-namespace",
		},
		{
			name:    "leaf group without group flag",
			args:    "group/project -p --leaf-group",
			wantErr: "--leaf-group requires --group and --preserve-namespace",
		},
		{
			name: "repo argument",
			args: "NAMESPACE/REPO",
			wantOpts: options{
				gitFlags: []string{},
			},
			wantCtxOpts: ContextOpts{
				Repo: "NAMESPACE/REPO",
			},
		},
		{
			name: "directory argument",
			args: "NAMESPACE/REPO mydir",
			wantOpts: options{
				gitFlags: []string{},
				dir:      "mydir",
			},
			wantCtxOpts: ContextOpts{
				Repo: "NAMESPACE/REPO",
			},
		},
		{
			name: "wiki repository",
			args: "--wiki NAMESPACE/REPO",
			wantOpts: options{
				gitFlags: []string{},
				wiki:     true,
			},
			wantCtxOpts: ContextOpts{
				Repo: "NAMESPACE/REPO",
			},
		},
		{
			name: "git clone arguments",
			args: "NAMESPACE/REPO -- --depth 1 --recurse-submodules",
			wantOpts: options{
				gitFlags: []string{"--depth", "1", "--recurse-submodules"},
			},
			wantCtxOpts: ContextOpts{
				Repo: "NAMESPACE/REPO",
			},
		},
		{
			name: "group clone arguments",
			args: "-g NAMESPACE/REPO -- --depth 1 --recurse-submodules",
			wantOpts: options{
				gitFlags:  []string{"--depth", "1", "--recurse-submodules"},
				groupName: "NAMESPACE/REPO",
			},
			wantCtxOpts: ContextOpts{
				Repo: "",
			},
		},
		{
			name: "group clone with directory argument",
			args: "-g NAMESPACE/REPO mydir",
			wantOpts: options{
				gitFlags:  []string{},
				groupName: "NAMESPACE/REPO",
				dir:       "mydir",
			},
			wantCtxOpts: ContextOpts{
				Repo: "",
			},
		},
		{
			name: "nested group clone with directory argument",
			args: "-g NAMESPACE/NESTED/SUBGROUP mydir",
			wantOpts: options{
				gitFlags:  []string{},
				groupName: "NAMESPACE/NESTED/SUBGROUP",
				dir:       "mydir",
			},
			wantCtxOpts: ContextOpts{
				Repo: "",
			},
		},
		{
			name: "group clone with directory argument and preserve namespace",
			args: "-p -g NAMESPACE/REPO mydir",
			wantOpts: options{
				gitFlags:          []string{},
				groupName:         "NAMESPACE/REPO",
				dir:               "mydir",
				preserveNamespace: true,
			},
			wantCtxOpts: ContextOpts{
				Repo: "",
			},
		},
		{
			name: "nested group clone with directory argument and preserve namespace",
			args: "-p -g NAMESPACE/NESTED/SUBGROUP mydir",
			wantOpts: options{
				gitFlags:          []string{},
				groupName:         "NAMESPACE/NESTED/SUBGROUP",
				dir:               "mydir",
				preserveNamespace: true,
			},
			wantCtxOpts: ContextOpts{
				Repo: "",
			},
		},
		{
			name:    "unknown argument",
			args:    "NAMESPACE/REPO --depth 1",
			wantErr: "unknown flag: --depth\nseparate Git clone flags with '--'",
		},
		{
			name: "group clone with active=true",
			args: "-g mygroup --active=true",
			wantOpts: options{
				gitFlags:  []string{},
				groupName: "mygroup",
				active:    true,
				activeSet: true,
			},
			wantCtxOpts: ContextOpts{
				Repo: "",
			},
		},
		{
			name: "group clone with active=false",
			args: "-g mygroup --active=false",
			wantOpts: options{
				gitFlags:  []string{},
				groupName: "mygroup",
				active:    false,
				activeSet: true,
			},
			wantCtxOpts: ContextOpts{
				Repo: "",
			},
		},
		{
			name: "group clone with repeated exclude-subgroup",
			args: "-g mygroup --exclude-subgroup domain/backend --exclude-subgroup docs",
			wantOpts: options{
				gitFlags:         []string{},
				groupName:        "mygroup",
				excludeSubgroups: []string{"domain/backend", "docs"},
			},
		},
		{
			name: "group clone with comma-separated exclude-subgroup",
			args: "-g mygroup --exclude-subgroup domain/backend,docs",
			wantOpts: options{
				gitFlags:         []string{},
				groupName:        "mygroup",
				excludeSubgroups: []string{"domain/backend", "docs"},
			},
		},
		{
			name: "group clone without active flag",
			args: "-g mygroup",
			wantOpts: options{
				gitFlags:  []string{},
				groupName: "mygroup",
				active:    false,
				activeSet: false,
			},
			wantCtxOpts: ContextOpts{
				Repo: "",
			},
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			io, stdin, stdout, stderr := cmdtest.TestIOStreams()
			fac := cmdtest.NewTestFactory(io)

			var opts *options
			var ctxOpts *ContextOpts
			cmd := NewCmdClone(fac, func(co *options, cx *ContextOpts) error {
				opts = co
				ctxOpts = cx
				return nil
			})

			argv, err := shlex.Split(tt.args)
			require.NoError(t, err)
			cmd.SetArgs(argv)

			cmd.SetIn(stdin)
			cmd.SetOut(stdout)
			cmd.SetErr(stderr)

			_, err = cmd.ExecuteC()
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Equal(t, tt.wantErr, err.Error())
				return
			}

			require.NoError(t, err)
			assert.Empty(t, stdout.String())
			assert.Empty(t, stderr.String())

			assert.Equal(t, tt.wantCtxOpts.Repo, ctxOpts.Repo)
			assert.Equal(t, tt.wantOpts.gitFlags, opts.gitFlags)
			assert.Equal(t, tt.wantOpts.groupName, opts.groupName)
			assert.Equal(t, tt.wantOpts.active, opts.active)
			assert.Equal(t, tt.wantOpts.activeSet, opts.activeSet)
			assert.Equal(t, tt.wantOpts.dir, opts.dir)
			assert.Equal(t, tt.wantOpts.preserveNamespace, opts.preserveNamespace)
			assert.Equal(t, tt.wantOpts.leafGroup, opts.leafGroup)
			assert.Equal(t, tt.wantOpts.wiki, opts.wiki)
			assert.Equal(t, tt.wantOpts.excludeSubgroups, opts.excludeSubgroups)
		})
	}
}

func TestCloneRepo(t *testing.T) {
	t.Parallel()

	const defaultHost = "gitlab.com"

	testCases := []struct {
		name         string
		repoArg      string
		subfolder    string
		wantHost     string
		wantFullName string
		wantErr      bool
	}{
		{name: "default host URL", repoArg: "https://gitlab.com/gitlab-org/cli", wantHost: defaultHost, wantFullName: "gitlab-org/cli"},
		{name: "self-managed URL uses its own host", repoArg: "https://mygitlab.com/mygroup/myrepo", wantHost: "mygitlab.com", wantFullName: "mygroup/myrepo"},
		{name: "scp-style URL uses its own host", repoArg: "git@mygitlab.com:mygroup/myrepo.git", wantHost: "mygitlab.com", wantFullName: "mygroup/myrepo"},
		{name: "ported host keeps its port", repoArg: "https://mygitlab.com:8080/mygroup/myrepo", wantHost: "mygitlab.com:8080", wantFullName: "mygroup/myrepo"},
		{name: "URL resolves without a configured subfolder", repoArg: "https://example.com/gitlab/myrepo", wantHost: "example.com", wantFullName: "gitlab/myrepo"},
		{name: "configured subfolder is stripped from the full name", repoArg: "https://example.com/gitlab/mygroup/myrepo", subfolder: "gitlab", wantHost: "example.com", wantFullName: "mygroup/myrepo"},
		{name: "configured subfolder leaving no owner is rejected", repoArg: "https://example.com/gitlab/myrepo", subfolder: "gitlab", wantErr: true},
		{name: "URL without a repo segment is rejected", repoArg: "https://gitlab.com/group", wantErr: true},
		{name: "trailing-slash URL is rejected", repoArg: "https://gitlab.com/mygroup/", wantErr: true},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := config.NewBlankConfig()
			if tt.subfolder != "" {
				require.NoError(t, cfg.Set("example.com", "subfolder", tt.subfolder))
			}

			repo, err := cloneRepo(tt.repoArg, defaultHost, cfg)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantHost, repo.RepoHost())
			assert.Equal(t, tt.wantFullName, repo.FullName())
		})
	}
}

func TestCloneFullURLMakesNoAPICall(t *testing.T) {
	io, _, _, _ := cmdtest.TestIOStreams()
	fac := cmdtest.NewTestFactory(io)

	apiCalled := false
	fac.ApiClientStub = func(string) (*api.Client, error) {
		apiCalled = true
		return &api.Client{}, nil
	}

	cs, restore := test.InitCmdStubber()
	defer restore()
	cs.Stub("")

	cmd := NewCmdClone(fac, nil)
	cmd.SetArgs([]string{"https://gitlab.example.com/mygroup/myrepo"})
	cmd.SetIn(io.In)
	cmd.SetOut(io.StdOut)
	cmd.SetErr(io.StdErr)

	_, err := cmd.ExecuteC()
	require.NoError(t, err)
	assert.False(t, apiCalled, "cloning a full URL must not create an API client")
	require.Len(t, cs.Calls, 1)
	assert.Equal(t, "git clone -- https://gitlab.example.com/mygroup/myrepo.git", strings.Join(cs.Calls[0].Args, " "))
}

func TestCloneWikiFromFullURLUsesURLHost(t *testing.T) {
	io, _, _, _ := cmdtest.TestIOStreams()

	var apiClientHost string
	fac := cmdtest.NewTestFactory(io, func(f *cmdtest.Factory) {
		f.ApiClientStub = func(repoHost string) (*api.Client, error) {
			apiClientHost = repoHost
			tc := gitlabtesting.NewTestClient(t)
			tc.MockProjects.EXPECT().GetProject("mygroup/myrepo", gomock.Any()).Return(
				&gitlab.Project{
					PathWithNamespace: "mygroup/myrepo",
					SSHURLToRepo:      "git@gitlab.example.com:mygroup/myrepo.git",
					HTTPURLToRepo:     "https://gitlab.example.com/mygroup/myrepo.git",
					WikiAccessLevel:   gitlab.EnabledAccessControl,
				}, nil, nil)
			return cmdtest.NewTestApiClient(t, nil, "", repoHost, api.WithGitLabClient(tc.Client)), nil
		}
	})

	cs, restore := test.InitCmdStubber()
	defer restore()
	cs.Stub("")

	cmd := NewCmdClone(fac, nil)
	cmd.SetArgs([]string{"--wiki", "https://gitlab.example.com/mygroup/myrepo"})
	cmd.SetIn(io.In)
	cmd.SetOut(io.StdOut)
	cmd.SetErr(io.StdErr)

	_, err := cmd.ExecuteC()
	require.NoError(t, err)
	assert.Equal(t, "gitlab.example.com", apiClientHost, "a full URL wiki clone must build the API client for the URL's host, not the default")
}

func TestCloneWikiFromFullURLStripsConfiguredSubfolder(t *testing.T) {
	io, _, _, _ := cmdtest.TestIOStreams()

	cfg := config.NewBlankConfig()
	require.NoError(t, cfg.Set("example.com", "subfolder", "gitlab"))

	fac := cmdtest.NewTestFactory(io, func(f *cmdtest.Factory) {
		f.ConfigStub = func() config.Config { return cfg }
		f.ApiClientStub = func(repoHost string) (*api.Client, error) {
			tc := gitlabtesting.NewTestClient(t)
			tc.MockProjects.EXPECT().GetProject("mygroup/myrepo", gomock.Any()).Return(
				&gitlab.Project{
					PathWithNamespace: "mygroup/myrepo",
					SSHURLToRepo:      "git@example.com:gitlab/mygroup/myrepo.git",
					HTTPURLToRepo:     "https://example.com/gitlab/mygroup/myrepo.git",
					WikiAccessLevel:   gitlab.EnabledAccessControl,
				}, nil, nil)
			return cmdtest.NewTestApiClient(t, nil, "", repoHost, api.WithGitLabClient(tc.Client)), nil
		}
	})

	cs, restore := test.InitCmdStubber()
	defer restore()
	cs.Stub("")

	cmd := NewCmdClone(fac, nil)
	cmd.SetArgs([]string{"--wiki", "https://example.com/gitlab/mygroup/myrepo"})
	cmd.SetIn(io.In)
	cmd.SetOut(io.StdOut)
	cmd.SetErr(io.StdErr)

	_, err := cmd.ExecuteC()
	require.NoError(t, err)
}

func TestCloneWikiRepository(t *testing.T) {
	opts := &options{
		currentUser: &gitlab.User{Username: "OWNER"},
		protocol:    "ssh",
		wiki:        true,
	}
	ctxOpts := &ContextOpts{
		Project: &gitlab.Project{
			PathWithNamespace: "OWNER/REPO",
			SSHURLToRepo:      "git@gitlab.com:OWNER/REPO.git",
			WikiAccessLevel:   gitlab.EnabledAccessControl,
		},
		Repo: "OWNER/REPO",
	}

	cs, restore := test.InitCmdStubber()
	defer restore()
	cs.Stub("")

	require.NoError(t, cloneRun(opts, ctxOpts))
	require.Len(t, cs.Calls, 1)
	assert.Equal(t, "git clone -- git@gitlab.com:OWNER/REPO.wiki.git", strings.Join(cs.Calls[0].Args, " "))
}

func TestCloneWikiRepositoryRejectsDisabledWiki(t *testing.T) {
	t.Parallel()

	opts := &options{
		currentUser: &gitlab.User{Username: "OWNER"},
		protocol:    "ssh",
		wiki:        true,
	}
	ctxOpts := &ContextOpts{
		Project: &gitlab.Project{
			PathWithNamespace: "OWNER/REPO",
			SSHURLToRepo:      "git@gitlab.com:OWNER/REPO.git",
			WikiAccessLevel:   gitlab.DisabledAccessControl,
		},
		Repo: "OWNER/REPO",
	}

	err := cloneRun(opts, ctxOpts)

	require.EqualError(t, err, "wiki is not enabled for OWNER/REPO")
}

func TestCloneRejectsInvalidRemoteURL(t *testing.T) {
	opts := &options{
		currentUser: &gitlab.User{Username: "OWNER"},
		protocol:    "https",
	}
	ctxOpts := &ContextOpts{
		Project: &gitlab.Project{
			PathWithNamespace: "OWNER/REPO",
			HTTPURLToRepo:     "--upload-pack=touch x",
		},
		Repo: "OWNER/REPO",
	}

	cs, restore := test.InitCmdStubber()
	defer restore()

	err := cloneRun(opts, ctxOpts)

	require.ErrorContains(t, err, "invalid git remote URL")
	assert.Zero(t, cs.Count)
}

func TestCloneProjectNamedWiki(t *testing.T) {
	opts := &options{
		currentUser: &gitlab.User{Username: "OWNER"},
		protocol:    "ssh",
	}
	ctxOpts := &ContextOpts{
		Project: &gitlab.Project{
			PathWithNamespace: "OWNER/docs.wiki",
			SSHURLToRepo:      "git@gitlab.com:OWNER/docs.wiki.git",
		},
		Repo: "OWNER/docs.wiki",
	}

	cs, restore := test.InitCmdStubber()
	defer restore()
	cs.Stub("")

	require.NoError(t, cloneRun(opts, ctxOpts))
	require.Len(t, cs.Calls, 1)
	assert.Equal(t, "git clone -- git@gitlab.com:OWNER/docs.wiki.git", strings.Join(cs.Calls[0].Args, " "))
}

func TestCloneRejectsInvalidAPIPaths(t *testing.T) {
	tests := []struct {
		name    string
		opts    *options
		project *gitlab.Project
	}{
		{
			name: "preserve namespace with traversal",
			opts: &options{
				currentUser:       &gitlab.User{Username: "OWNER"},
				protocol:          "https",
				preserveNamespace: true,
				dir:               "work",
			},
			project: &gitlab.Project{
				PathWithNamespace: "../../evil",
				Path:              "evil",
				HTTPURLToRepo:     "https://gitlab.com/OWNER/REPO.git",
			},
		},
		{
			name: "group clone with dir and dotdot path",
			opts: &options{
				currentUser: &gitlab.User{Username: "OWNER"},
				protocol:    "https",
				groupName:   "group",
				dir:         "work",
			},
			project: &gitlab.Project{
				PathWithNamespace: "group/evil",
				Path:              "..",
				HTTPURLToRepo:     "https://gitlab.com/group/evil.git",
			},
		},
		{
			name: "fork upstream dir with absolute path",
			opts: &options{
				currentUser: &gitlab.User{Username: "OWNER"},
				protocol:    "https",
			},
			project: &gitlab.Project{
				PathWithNamespace: "OWNER/evil",
				Path:              "/abs",
				HTTPURLToRepo:     "https://gitlab.com/OWNER/evil.git",
				ForkedFromProject: &gitlab.ForkParent{PathWithNamespace: "up/evil"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs, restore := test.InitCmdStubber()
			defer restore()

			err := cloneRun(tt.opts, &ContextOpts{Project: tt.project, Repo: "OWNER/REPO"})

			require.ErrorContains(t, err, "invalid project path")
			assert.Zero(t, cs.Count)
		})
	}
}

func TestValidateAPIPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		ok   bool
	}{
		{"gitlab-org/cli", true},
		{"group/sub/project", true},
		{"my.project", true},
		{"", false},
		{"/abs", false},
		{"..", false},
		{".", false},
		{"../../x", false},
		{"a/../b", false},
		{"a//b", false},
		{"a/", false},
		{`a\b`, false},
		{"C:/x", false},
		{"-flag", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()
			err := validateAPIPath(tt.path)
			if tt.ok {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "invalid project path")
			}
		})
	}
}

func TestIsExcludedSubgroup(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		path     string
		excludes []string
		want     bool
	}{
		{name: "project in excluded subgroup", path: "mygroup/domain/backend/api", excludes: []string{"domain/backend"}, want: true},
		{name: "project in nested excluded subgroup", path: "mygroup/domain/backend/svc/api", excludes: []string{"domain/backend"}, want: true},
		{name: "project path equals exclude", path: "mygroup/domain/backend", excludes: []string{"domain/backend"}, want: true},
		{name: "exclude prefixed with group", path: "mygroup/domain/backend/api", excludes: []string{"mygroup/domain/backend"}, want: true},
		{name: "sibling sharing string prefix", path: "mygroup/domain/backend-utils/api", excludes: []string{"domain/backend"}, want: false},
		{name: "parent subgroup project", path: "mygroup/domain/api", excludes: []string{"domain/backend"}, want: false},
		{name: "different case", path: "mygroup/domain/backend/api", excludes: []string{"Domain/Backend"}, want: true},
		{name: "surrounding slashes", path: "mygroup/domain/backend/api", excludes: []string{"/domain/backend/"}, want: true},
		{name: "one of several excludes", path: "mygroup/docs/site", excludes: []string{"domain/backend", "docs"}, want: true},
		{name: "no excludes", path: "mygroup/domain/backend/api", excludes: nil, want: false},
		{name: "empty exclude", path: "mygroup/api", excludes: []string{"", "/"}, want: false},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, isExcludedSubgroup(tt.path, "mygroup", tt.excludes))
		})
	}
}

func TestCloneRepositoryHostResolution(t *testing.T) {
	const (
		defaultConfig = `---
hosts:
  gitlab.com:
    git_protocol: https
`
		hostlessConfig = `---
git_protocol: https
`
		multiHostConfig = `---
hosts:
  gitlab.com:
    git_protocol: https
  gitlab.example.org:
    git_protocol: ssh
  gitlab.other.example:
    git_protocol: https
`
		otherHostConfig = `---
hosts:
  gitlab.other.example:
    git_protocol: https
`
		singleLabelHostConfig = `---
hosts:
  gitlab.com:
    git_protocol: https
  localhost:
    git_protocol: ssh
`
		mixedCaseHostConfig = `---
hosts:
  gitlab.com:
    git_protocol: https
  GitLab.Example.ORG:
    git_protocol: ssh
`
	)

	testCases := []struct {
		name            string
		repoArg         string
		defaultHost     string
		config          string
		wantAPIHost     string
		wantProjectPath string
		project         *gitlab.Project
		wantClone       string
	}{
		{
			name:            "configured custom host selects its API client and protocol",
			repoArg:         "gitlab.example.org/group/project",
			defaultHost:     "gitlab.com",
			config:          multiHostConfig,
			wantAPIHost:     "gitlab.example.org",
			wantProjectPath: "group/project",
			project: &gitlab.Project{
				Path:              "project",
				PathWithNamespace: "group/project",
				SSHURLToRepo:      "git@gitlab.example.org:group/project.git",
				HTTPURLToRepo:     "https://gitlab.example.org/group/project.git",
			},
			wantClone: "git clone -- git@gitlab.example.org:group/project.git",
		},
		{
			name:            "configured custom host supports nested namespaces",
			repoArg:         "gitlab.example.org/group/subgroup/project",
			defaultHost:     "gitlab.com",
			config:          multiHostConfig,
			wantAPIHost:     "gitlab.example.org",
			wantProjectPath: "group/subgroup/project",
			project: &gitlab.Project{
				Path:              "project",
				PathWithNamespace: "group/subgroup/project",
				SSHURLToRepo:      "git@gitlab.example.org:group/subgroup/project.git",
				HTTPURLToRepo:     "https://gitlab.example.org/group/subgroup/project.git",
			},
			wantClone: "git clone -- git@gitlab.example.org:group/subgroup/project.git",
		},
		{
			name:            "configured host can also be the first namespace",
			repoArg:         "gitlab.example.org/gitlab.example.org/myrepo",
			defaultHost:     "gitlab.example.org",
			config:          multiHostConfig,
			wantAPIHost:     "gitlab.example.org",
			wantProjectPath: "gitlab.example.org/myrepo",
			project: &gitlab.Project{
				Path:              "myrepo",
				PathWithNamespace: "gitlab.example.org/myrepo",
				SSHURLToRepo:      "git@gitlab.example.org:gitlab.example.org/myrepo.git",
				HTTPURLToRepo:     "https://gitlab.example.org/gitlab.example.org/myrepo.git",
			},
			wantClone: "git clone -- git@gitlab.example.org:gitlab.example.org/myrepo.git",
		},
		{
			name:            "unconfigured dotted prefix remains in the default host namespace",
			repoArg:         "department.example/group/project",
			defaultHost:     "gitlab.com",
			config:          defaultConfig,
			wantAPIHost:     "gitlab.com",
			wantProjectPath: "department.example/group/project",
			project: &gitlab.Project{
				Path:              "project",
				PathWithNamespace: "department.example/group/project",
				SSHURLToRepo:      "git@gitlab.com:department.example/group/project.git",
				HTTPURLToRepo:     "https://gitlab.com/department.example/group/project.git",
			},
			wantClone: "git clone -- https://gitlab.com/department.example/group/project.git",
		},
		{
			name:            "case-mismatched host key remains in the default host namespace",
			repoArg:         "gitlab.example.org/group/project",
			defaultHost:     "gitlab.com",
			config:          mixedCaseHostConfig,
			wantAPIHost:     "gitlab.com",
			wantProjectPath: "gitlab.example.org/group/project",
			project: &gitlab.Project{
				Path:              "project",
				PathWithNamespace: "gitlab.example.org/group/project",
				SSHURLToRepo:      "git@gitlab.com:gitlab.example.org/group/project.git",
				HTTPURLToRepo:     "https://gitlab.com/gitlab.example.org/group/project.git",
			},
			wantClone: "git clone -- https://gitlab.com/gitlab.example.org/group/project.git",
		},
		{
			name:            "exact mixed-case host key retains its config lookup",
			repoArg:         "GitLab.Example.ORG/group/project",
			defaultHost:     "gitlab.com",
			config:          mixedCaseHostConfig,
			wantAPIHost:     "GitLab.Example.ORG",
			wantProjectPath: "group/project",
			project: &gitlab.Project{
				Path:              "project",
				PathWithNamespace: "group/project",
				SSHURLToRepo:      "git@gitlab.example.org:group/project.git",
				HTTPURLToRepo:     "https://gitlab.example.org/group/project.git",
			},
			wantClone: "git clone -- git@gitlab.example.org:group/project.git",
		},
		{
			name:            "ordinary repository retains a mixed-case default host",
			repoArg:         "group/project",
			defaultHost:     "GitLab.Example.ORG",
			config:          mixedCaseHostConfig,
			wantAPIHost:     "GitLab.Example.ORG",
			wantProjectPath: "group/project",
			project: &gitlab.Project{
				Path:              "project",
				PathWithNamespace: "group/project",
				SSHURLToRepo:      "git@gitlab.example.org:group/project.git",
				HTTPURLToRepo:     "https://gitlab.example.org/group/project.git",
			},
			wantClone: "git clone -- git@gitlab.example.org:group/project.git",
		},
		{
			name:            "ordinary owner and repository remain unchanged",
			repoArg:         "group/project",
			defaultHost:     "gitlab.com",
			config:          defaultConfig,
			wantAPIHost:     "gitlab.com",
			wantProjectPath: "group/project",
			project: &gitlab.Project{
				Path:              "project",
				PathWithNamespace: "group/project",
				SSHURLToRepo:      "git@gitlab.com:group/project.git",
				HTTPURLToRepo:     "https://gitlab.com/group/project.git",
			},
			wantClone: "git clone -- https://gitlab.com/group/project.git",
		},
		{
			name:            "ordinary repository works without a hosts map",
			repoArg:         "group/project",
			defaultHost:     "gitlab.com",
			config:          hostlessConfig,
			wantAPIHost:     "gitlab.com",
			wantProjectPath: "group/project",
			project: &gitlab.Project{
				Path:              "project",
				PathWithNamespace: "group/project",
				SSHURLToRepo:      "git@gitlab.com:group/project.git",
				HTTPURLToRepo:     "https://gitlab.com/group/project.git",
			},
			wantClone: "git clone -- https://gitlab.com/group/project.git",
		},
		{
			name:            "project ID remains unchanged",
			repoArg:         "4356677",
			defaultHost:     "gitlab.com",
			config:          defaultConfig,
			wantAPIHost:     "gitlab.com",
			wantProjectPath: "4356677",
			project: &gitlab.Project{
				Path:              "project",
				PathWithNamespace: "group/project",
				SSHURLToRepo:      "git@gitlab.com:group/project.git",
				HTTPURLToRepo:     "https://gitlab.com/group/project.git",
			},
			wantClone: "git clone -- https://gitlab.com/group/project.git",
		},
		{
			name:            "short repository name remains relative to the current user",
			repoArg:         "project",
			defaultHost:     "gitlab.com",
			config:          defaultConfig,
			wantAPIHost:     "gitlab.com",
			wantProjectPath: "OWNER/project",
			project: &gitlab.Project{
				Path:              "project",
				PathWithNamespace: "OWNER/project",
				SSHURLToRepo:      "git@gitlab.com:OWNER/project.git",
				HTTPURLToRepo:     "https://gitlab.com/OWNER/project.git",
			},
			wantClone: "git clone -- https://gitlab.com/OWNER/project.git",
		},
		{
			name:        "full URL remains unchanged",
			repoArg:     "https://gitlab.example.org/group/project",
			defaultHost: "gitlab.com",
			config:      multiHostConfig,
			wantClone:   "git clone -- https://gitlab.example.org/group/project.git",
		},
		{
			name:            "default hostname prefix is recognized",
			repoArg:         "gitlab.other.example/group/project",
			defaultHost:     "gitlab.other.example",
			config:          defaultConfig,
			wantAPIHost:     "gitlab.other.example",
			wantProjectPath: "group/project",
			project: &gitlab.Project{
				Path:              "project",
				PathWithNamespace: "group/project",
				SSHURLToRepo:      "git@gitlab.other.example:group/project.git",
				HTTPURLToRepo:     "https://gitlab.other.example/group/project.git",
			},
			wantClone: "git clone -- git@gitlab.other.example:group/project.git",
		},
		{
			name:            "configured host prefix overrides a conflicting default hostname",
			repoArg:         "gitlab.example.org/group/project",
			defaultHost:     "gitlab.other.example",
			config:          multiHostConfig,
			wantAPIHost:     "gitlab.example.org",
			wantProjectPath: "group/project",
			project: &gitlab.Project{
				Path:              "project",
				PathWithNamespace: "group/project",
				SSHURLToRepo:      "git@gitlab.example.org:group/project.git",
				HTTPURLToRepo:     "https://gitlab.example.org/group/project.git",
			},
			wantClone: "git clone -- git@gitlab.example.org:group/project.git",
		},
		{
			name:            "configured gitlab.com prefix overrides a conflicting default hostname",
			repoArg:         "gitlab.com/group/project",
			defaultHost:     "gitlab.other.example",
			config:          multiHostConfig,
			wantAPIHost:     "gitlab.com",
			wantProjectPath: "group/project",
			project: &gitlab.Project{
				Path:              "project",
				PathWithNamespace: "group/project",
				SSHURLToRepo:      "git@gitlab.com:group/project.git",
				HTTPURLToRepo:     "https://gitlab.com/group/project.git",
			},
			wantClone: "git clone -- https://gitlab.com/group/project.git",
		},
		{
			name:            "unconfigured gitlab.com prefix remains in the self-managed namespace",
			repoArg:         "gitlab.com/group/project",
			defaultHost:     "gitlab.other.example",
			config:          otherHostConfig,
			wantAPIHost:     "gitlab.other.example",
			wantProjectPath: "gitlab.com/group/project",
			project: &gitlab.Project{
				Path:              "project",
				PathWithNamespace: "gitlab.com/group/project",
				SSHURLToRepo:      "git@gitlab.other.example:gitlab.com/group/project.git",
				HTTPURLToRepo:     "https://gitlab.other.example/gitlab.com/group/project.git",
			},
			wantClone: "git clone -- https://gitlab.other.example/gitlab.com/group/project.git",
		},
		{
			name:            "configured single-label host is recognized",
			repoArg:         "localhost/group/project",
			defaultHost:     "gitlab.com",
			config:          singleLabelHostConfig,
			wantAPIHost:     "localhost",
			wantProjectPath: "group/project",
			project: &gitlab.Project{
				Path:              "project",
				PathWithNamespace: "group/project",
				SSHURLToRepo:      "git@localhost:group/project.git",
				HTTPURLToRepo:     "https://localhost/group/project.git",
			},
			wantClone: "git clone -- git@localhost:group/project.git",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			testClient := gitlabtesting.NewTestClient(t)
			testClient.MockUsers.EXPECT().CurrentUser().Return(&gitlab.User{Username: "OWNER"}, nil, nil).AnyTimes()
			if tc.wantProjectPath != "" {
				testClient.MockProjects.EXPECT().GetProject(tc.wantProjectPath, gomock.Any()).Return(tc.project, nil, nil)
			}

			apiClient := cmdtest.NewTestApiClient(t, nil, "", tc.defaultHost, api.WithGitLabClient(testClient.Client))
			var apiClientHost string

			cs, restore := test.InitCmdStubber()
			defer restore()
			cs.Stub("")

			exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
				return NewCmdClone(f, nil)
			}, false,
				cmdtest.WithApiClientFunc(func(repoHost string) (*api.Client, error) {
					apiClientHost = repoHost
					return apiClient, nil
				}),
				cmdtest.WithConfig(config.NewFromString(tc.config)),
				func(f *cmdtest.Factory) {
					f.DefaultHostnameStub = tc.defaultHost
				},
			)

			_, err := exec(tc.repoArg)

			require.NoError(t, err)
			if tc.wantAPIHost != "" {
				assert.Equal(t, tc.wantAPIHost, apiClientHost)
			}
			require.Len(t, cs.Calls, 1)
			assert.Equal(t, tc.wantClone, strings.Join(cs.Calls[0].Args, " "))
		})
	}
}

func TestCloneRepositoryRejectsTrailingSlash(t *testing.T) {
	apiCalled := false
	exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
		return NewCmdClone(f, nil)
	}, false,
		cmdtest.WithApiClientFunc(func(string) (*api.Client, error) {
			apiCalled = true
			return &api.Client{}, nil
		}),
		cmdtest.WithConfig(config.NewFromString("hosts:\n  gitlab.example.org: {}\n")),
	)

	_, err := exec("gitlab.example.org/group/project/")

	require.EqualError(t, err, `expected the "[HOST/]OWNER/[NAMESPACE/]REPO" format, got "gitlab.example.org/group/project/"`)
	assert.False(t, apiCalled)
}

func TestGroupCloneExcludeSubgroup(t *testing.T) {
	projects := []*gitlab.Project{
		{Path: "api", PathWithNamespace: "mygroup/api", SSHURLToRepo: "git@gitlab.com:mygroup/api.git"},
		{Path: "svc", PathWithNamespace: "mygroup/domain/backend/svc", SSHURLToRepo: "git@gitlab.com:mygroup/domain/backend/svc.git"},
		{Path: "lib", PathWithNamespace: "mygroup/domain/backend-utils/lib", SSHURLToRepo: "git@gitlab.com:mygroup/domain/backend-utils/lib.git"},
		{Path: "shared", PathWithNamespace: "othergroup/domain/backend/shared", SSHURLToRepo: "git@gitlab.com:othergroup/domain/backend/shared.git"},
	}
	clonesWithoutBackend := []string{
		"git clone -- git@gitlab.com:mygroup/api.git",
		"git clone -- git@gitlab.com:mygroup/domain/backend-utils/lib.git",
		"git clone -- git@gitlab.com:othergroup/domain/backend/shared.git",
	}

	testCases := []struct {
		name        string
		cli         string
		group       string
		projects    []*gitlab.Project
		numericID   bool
		getGroupErr error
		wantClones  []string
		wantErr     string
		wantStderr  string
	}{
		{
			name:       "skips projects in excluded subgroup",
			cli:        "-g mygroup --exclude-subgroup domain/backend",
			group:      "mygroup",
			wantClones: clonesWithoutBackend,
		},
		{
			name:       "accepts exclude prefixed with group path",
			cli:        "-g mygroup --exclude-subgroup mygroup/domain/backend",
			group:      "mygroup",
			wantClones: clonesWithoutBackend,
		},
		{
			name:       "resolves numeric group ID to its full path",
			cli:        "-g 123 --exclude-subgroup domain/backend",
			group:      "123",
			numericID:  true,
			wantClones: clonesWithoutBackend,
		},
		{
			name:       "no projects left after exclusion",
			cli:        "-g mygroup --exclude-subgroup domain",
			group:      "mygroup",
			projects:   projects[1:3],
			wantErr:    cmdutils.SilentError.Error(),
			wantStderr: `No projects left to clone in group "mygroup" after applying --exclude-subgroup.`,
		},
		{
			name:        "group lookup fails",
			cli:         "-g 123 --exclude-subgroup domain/backend",
			group:       "123",
			getGroupErr: errors.New("404 Group Not Found"),
			wantErr:     "404 Group Not Found",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			testClient := gitlabtesting.NewTestClient(t)
			testClient.MockUsers.EXPECT().CurrentUser().Return(&gitlab.User{Username: "OWNER"}, nil, nil)
			if tc.projects == nil {
				tc.projects = projects
			}
			if tc.getGroupErr != nil {
				testClient.MockGroups.EXPECT().GetGroup(tc.group, gomock.Any()).Return(nil, nil, tc.getGroupErr)
			} else {
				if tc.numericID {
					testClient.MockGroups.EXPECT().GetGroup(tc.group, gomock.Any()).Return(&gitlab.Group{FullPath: "mygroup"}, nil, nil)
				}
				testClient.MockGroups.EXPECT().ListGroupProjects(tc.group, gomock.Any()).Return(slices.Clone(tc.projects), nil, nil)
			}

			cs, restore := test.InitCmdStubber()
			defer restore()
			for range tc.wantClones {
				cs.Stub("")
			}

			exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
				return NewCmdClone(f, nil)
			}, false,
				cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
				cmdtest.WithConfig(config.NewFromString("git_protocol: ssh\n")),
			)

			out, err := exec(tc.cli)

			if tc.wantErr != "" {
				require.EqualError(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			if tc.wantStderr != "" {
				assert.Contains(t, out.Stderr(), tc.wantStderr)
			}
			require.Len(t, cs.Calls, len(tc.wantClones))
			for i, want := range tc.wantClones {
				assert.Equal(t, want, strings.Join(cs.Calls[i].Args, " "))
			}
		})
	}
}

func TestGroupCloneLeafGroup(t *testing.T) {
	for _, tc := range []struct {
		name       string
		group      string
		fullPath   string
		sharedPath string
		wantRoot   string
		wantErr    string
	}{
		{
			name:       "numeric group ID",
			group:      "123",
			fullPath:   "org/platform/backend",
			sharedPath: "org/platform/frontend/shared",
			wantRoot:   "checkout/backend",
		},
		{
			name:       "nested group path",
			group:      "org/platform/backend",
			fullPath:   "org/platform/backend",
			sharedPath: "org/platform/frontend/shared",
			wantRoot:   "checkout/backend",
		},
		{
			name:       "top-level group",
			group:      "org",
			fullPath:   "org",
			sharedPath: "shared-org/platform/frontend/shared",
			wantRoot:   "checkout/org",
		},
		{
			name:       "colliding shared project aborts before cloning",
			group:      "org/platform/backend",
			fullPath:   "org/platform/backend",
			sharedPath: "backend/services/api",
			wantErr:    "both clone to " + strconv.Quote(filepath.FromSlash("checkout/backend/services/api")),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testClient := gitlabtesting.NewTestClient(t)
			testClient.MockUsers.EXPECT().CurrentUser().Return(&gitlab.User{Username: "OWNER"}, nil, nil)
			testClient.MockGroups.EXPECT().GetGroup(tc.group, gomock.Any()).Return(&gitlab.Group{FullPath: tc.fullPath}, nil, nil)
			testClient.MockGroups.EXPECT().ListGroupProjects(tc.group, gomock.Any()).Return([]*gitlab.Project{
				{Path: "api", PathWithNamespace: tc.fullPath + "/api", SSHURLToRepo: "git@gitlab.com:" + tc.fullPath + "/api.git"},
				{Path: "api", PathWithNamespace: tc.fullPath + "/services/api", SSHURLToRepo: "git@gitlab.com:" + tc.fullPath + "/services/api.git"},
				{Path: path.Base(tc.sharedPath), PathWithNamespace: tc.sharedPath, SSHURLToRepo: "git@gitlab.com:" + tc.sharedPath + ".git"},
			}, nil, nil)

			cs, restore := test.InitCmdStubber()
			defer restore()
			cs.Stub("")
			cs.Stub("")
			cs.Stub("")
			exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
				return NewCmdClone(f, nil)
			}, false,
				cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
				cmdtest.WithConfig(config.NewFromString("git_protocol: ssh\n")),
			)

			_, err := exec("-g " + tc.group + " --preserve-namespace --leaf-group checkout")

			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				require.ErrorContains(t, err, tc.fullPath+"/services/api")
				require.ErrorContains(t, err, tc.sharedPath)
				assert.Empty(t, cs.Calls)
				return
			}

			require.NoError(t, err)
			wantDirs := []string{tc.wantRoot + "/api", tc.wantRoot + "/services/api", "checkout/" + tc.sharedPath}
			require.Len(t, cs.Calls, len(wantDirs))
			for i, wantDir := range wantDirs {
				args := cs.Calls[i].Args
				require.NotEmpty(t, args)
				assert.Equal(t, filepath.FromSlash(wantDir), args[len(args)-1])
			}
		})
	}
}
