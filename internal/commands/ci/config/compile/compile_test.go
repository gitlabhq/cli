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
			StdOut:           "",
			wantErr:          false,
			errMsg:           "",
			showHaveBaseRepo: true,
			expectedLintResponse: &gitlab.ProjectLintResult{
				Valid: true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tc := gitlabtesting.NewTestClient(t)
			gomock.InOrder(
				tc.MockProjects.EXPECT().GetProject("OWNER/REPO", gomock.Any()).Return(&gitlab.Project{ID: int64(123)}, nil, nil).MaxTimes(1),
				tc.MockValidate.EXPECT().ProjectNamespaceLint(int64(123), gomock.Any()).Return(tt.expectedLintResponse, nil, nil).MaxTimes(1),
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
