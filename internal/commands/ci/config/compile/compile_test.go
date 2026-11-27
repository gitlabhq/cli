//go:build !integration

package compile

import (
	"errors"
	"path"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_compileRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                 string
		testFile             string
		StdOut               string
		wantErr              bool
		errMsg               string
		expectedLintResponse *gitlab.ProjectLintResult
		showHaveBaseRepo     bool
	}{
		{
			name:             "with invalid path specified",
			testFile:         "WRONG_PATH",
			StdOut:           "",
			wantErr:          true,
			errMsg:           "WRONG_PATH: no such file or directory",
			showHaveBaseRepo: true,
		},
		{
			name:             "without base repo",
			testFile:         ".gitlab.ci.yml",
			StdOut:           "",
			wantErr:          true,
			errMsg:           "you must be in a GitLab project repository for this action: no base repository present",
			showHaveBaseRepo: false,
		},
		{
			name:             "when a valid path is specified and yaml is valid",
			testFile:         ".gitlab-ci.yml",
			StdOut:           "---\ntest:\n  script: echo hello\n",
			wantErr:          false,
			errMsg:           "",
			showHaveBaseRepo: true,
			expectedLintResponse: &gitlab.ProjectLintResult{
				Valid:      true,
				MergedYaml: "---\ntest:\n  script: echo hello\n",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tc := gitlabtesting.NewTestClient(t)
			gomock.InOrder(
				tc.MockProjects.EXPECT().GetProject("OWNER/REPO", gomock.Any()).Return(&gitlab.Project{ID: int64(123)}, nil, nil).MaxTimes(1),
				tc.MockValidate.EXPECT().ProjectNamespaceLint(int64(123), gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ any, opt *gitlab.ProjectNamespaceLintOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.ProjectLintResult, *gitlab.Response, error) {
						require.NotNil(t, opt.IncludeJobs)
						assert.False(t, *opt.IncludeJobs)
						return tt.expectedLintResponse, nil, nil
					},
				).MaxTimes(1),
			)
			options := []cmdtest.FactoryOption{cmdtest.WithGitLabClient(tc.Client)}
			if !tt.showHaveBaseRepo {
				options = append(options, cmdtest.WithBaseRepoError(errors.New("no base repository present")))
			}
			exec := cmdtest.SetupCmdForTest(t, NewCmdConfigCompile, false, options...)

			args := path.Join(cmdtest.ProjectPath, "test/testdata", tt.testFile)
			out, err := exec(args)
			if tt.wantErr {
				require.Contains(t, err.Error(), tt.errMsg)
				return
			}
			require.NoError(t, err)

			assert.Equal(t, tt.StdOut, out.OutBuf.String())
		})
	}
}

func Test_compileRun_job(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		args           string
		wantStdOut     string
		wantErr        string
		wantNoRequests bool
		resp           *gitlab.ProjectLintResult
	}{
		{
			name:       "job with before_script, script, and after_script",
			args:       "--job test-job",
			wantStdOut: "echo setup\nnpm test\nnpm run lint\n\n# after_script\necho cleanup\n",
			resp: &gitlab.ProjectLintResult{
				Valid: true,
				Jobs: []gitlab.ProjectLintJob{
					{Name: "test-job", BeforeScript: []string{"echo setup"}, Script: []string{"npm test", "npm run lint"}, AfterScript: []string{"echo cleanup"}},
					{Name: "build-job", Script: []string{"npm run build"}},
				},
			},
		},
		{
			name:       "job with only script",
			args:       "--job build-job",
			wantStdOut: "npm run build\n",
			resp: &gitlab.ProjectLintResult{
				Valid: true,
				Jobs: []gitlab.ProjectLintJob{
					{Name: "test-job", Script: []string{"npm test"}},
					{Name: "build-job", Script: []string{"npm run build"}},
				},
			},
		},
		{
			name:       "job with only after_script",
			args:       "--job cleanup-job",
			wantStdOut: "# after_script\necho cleanup\n",
			resp: &gitlab.ProjectLintResult{
				Valid: true,
				Jobs: []gitlab.ProjectLintJob{
					{Name: "cleanup-job", AfterScript: []string{"echo cleanup"}},
				},
			},
		},
		{
			name:    "job with no scripts returns error",
			args:    "--job trigger-job",
			wantErr: `job "trigger-job" has no script to print (is it a trigger job?)`,
			resp: &gitlab.ProjectLintResult{
				Valid: true,
				Jobs: []gitlab.ProjectLintJob{
					{Name: "trigger-job"},
				},
			},
		},
		{
			name:           "empty job flag returns error",
			args:           `--job ""`,
			wantErr:        "--job cannot be empty",
			wantNoRequests: true,
		},
		{
			name:    "job not found lists available jobs",
			args:    "--job deploy-job",
			wantErr: `job "deploy-job" not found in CI/CD configuration. Available jobs: test-job, build-job`,
			resp: &gitlab.ProjectLintResult{
				Valid: true,
				Jobs: []gitlab.ProjectLintJob{
					{Name: "test-job", Script: []string{"npm test"}},
					{Name: "build-job", Script: []string{"npm run build"}},
				},
			},
		},
		{
			name:    "invalid CI configuration returns validation error",
			args:    "--job test-job",
			wantErr: "could not compile " + path.Join(cmdtest.ProjectPath, "test/testdata/.gitlab-ci.yml") + ": jobs:test-job config key may not be used with script",
			resp: &gitlab.ProjectLintResult{
				Valid:  false,
				Errors: []string{"jobs:test-job config key may not be used with script"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tc := gitlabtesting.NewTestClient(t)
			if !tt.wantNoRequests {
				gomock.InOrder(
					tc.MockProjects.EXPECT().GetProject("OWNER/REPO", gomock.Any()).Return(&gitlab.Project{ID: int64(123)}, nil, nil),
					tc.MockValidate.EXPECT().ProjectNamespaceLint(int64(123), gomock.Any(), gomock.Any()).DoAndReturn(
						func(_ any, opt *gitlab.ProjectNamespaceLintOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.ProjectLintResult, *gitlab.Response, error) {
							require.NotNil(t, opt.IncludeJobs)
							assert.True(t, *opt.IncludeJobs)
							require.NotNil(t, opt.Content)
							assert.NotEmpty(t, *opt.Content)
							return tt.resp, nil, nil
						},
					),
				)
			}

			exec := cmdtest.SetupCmdForTest(t, NewCmdConfigCompile, false, cmdtest.WithGitLabClient(tc.Client))

			cliArgs := path.Join(cmdtest.ProjectPath, "test/testdata/.gitlab-ci.yml") + " " + tt.args
			out, err := exec(cliArgs)
			if tt.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantStdOut, out.OutBuf.String())
		})
	}
}
