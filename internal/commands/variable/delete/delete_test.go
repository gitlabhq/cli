//go:build !integration

package delete

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/google/shlex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_NewCmdDelete(t *testing.T) {
	tests := []struct {
		name     string
		cli      string
		wants    options
		stdinTTY bool
		wantsErr bool
	}{
		{
			name:     "delete var",
			cli:      "cool_secret",
			wantsErr: false,
		},
		{
			name:     "delete scoped var",
			cli:      "cool_secret --scope prod",
			wantsErr: false,
		},
		{
			name:     "delete group var",
			cli:      "cool_secret -g mygroup",
			wantsErr: false,
		},
		{
			name:     "delete scoped group var",
			cli:      "cool_secret -g mygroup --scope prod",
			wantsErr: false,
		},
		{
			name:     "no name",
			cli:      "",
			wantsErr: true,
		},
		{
			name:     "invalid characters in name",
			cli:      "BAD-SECRET",
			wantsErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			io, _, _, _ := cmdtest.TestIOStreams()
			f := cmdtest.NewTestFactory(io,
				func(f *cmdtest.Factory) {
					f.ApiClientStub = func(repoHost string) (*api.Client, error) {
						tc := gitlabtesting.NewTestClient(t)
						tc.MockProjectVariables.EXPECT().RemoveVariable(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
						tc.MockGroupVariables.EXPECT().RemoveVariable(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
						return cmdtest.NewTestApiClient(t, nil, "", repoHost, api.WithGitLabClient(tc.Client)), nil
					}
				},
				cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			)

			io.IsInTTY = tt.stdinTTY

			argv, err := shlex.Split(tt.cli)
			require.NoError(t, err)

			cmd := NewCmdDelete(f)
			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err = cmd.ExecuteC()
			if tt.wantsErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func Test_deleteRun(t *testing.T) {
	tests := []struct {
		name        string
		key         string
		scope       string
		group       string
		wantsErr    bool
		wantsOutput string
		setupMock   func(tc *gitlabtesting.TestClient)
	}{
		{
			name:        "delete project variable no scope",
			key:         "TEST_VAR",
			scope:       "*",
			wantsErr:    false,
			wantsOutput: "✓ Deleted variable TEST_VAR with scope * for owner/repo.\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjectVariables.EXPECT().
					RemoveVariable("owner/repo", "TEST_VAR", gomock.Any(), gomock.Any()).
					Return(nil, nil)
			},
		},
		{
			name:        "delete project variable with stage scope",
			key:         "TEST_VAR",
			scope:       "stage",
			wantsErr:    false,
			wantsOutput: "✓ Deleted variable TEST_VAR with scope stage for owner/repo.\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjectVariables.EXPECT().
					RemoveVariable("owner/repo", "TEST_VAR", gomock.Any(), gomock.Any()).
					Return(nil, nil)
			},
		},
		{
			name:        "delete group variable",
			key:         "TEST_VAR",
			scope:       "*",
			group:       "testGroup",
			wantsErr:    false,
			wantsOutput: "✓ Deleted variable TEST_VAR for group testGroup with scope *.\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroupVariables.EXPECT().
					RemoveVariable("testGroup", "TEST_VAR", gomock.Any(), gomock.Any()).
					Return(nil, nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tt.setupMock(testClient)

			io, _, stdout, _ := cmdtest.TestIOStreams()

			opts := &options{
				apiClient: func(repoHost string) (*api.Client, error) {
					return cmdtest.NewTestApiClient(t, nil, "", "gitlab.com", api.WithGitLabClient(testClient.Client)), nil
				},
				baseRepo: func() (glrepo.Interface, error) {
					return glrepo.New("owner", "repo", "gitlab.com"), nil
				},
				io:    io,
				key:   tt.key,
				scope: tt.scope,
				group: tt.group,
			}

			// WHEN
			err := opts.run(t.Context())

			// THEN
			if tt.wantsErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantsOutput, stdout.String())
		})
	}
}

func Test_deleteGroupScope(t *testing.T) {
	t.Parallel()

	apiErr := errors.New("group variable delete failed")
	for _, tt := range []struct {
		name   string
		cli    string
		scope  string
		apiErr error
	}{
		{name: "default scope", cli: "TEST_VARIABLE -g mygroup", scope: "*"},
		{name: "explicit wildcard", cli: "TEST_VARIABLE -g mygroup -s '*'", scope: "*"},
		{name: "stage scope", cli: "TEST_VARIABLE --group mygroup --scope stage", scope: "stage"},
		{name: "wildcard environment", cli: "TEST_VARIABLE -g mygroup -s 'review/*'", scope: "review/*"},
		{name: "API error", cli: "TEST_VARIABLE -g mygroup -s stage", scope: "stage", apiErr: apiErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tc := gitlabtesting.NewTestClient(t)
			tc.MockGroupVariables.EXPECT().RemoveVariable("mygroup", "TEST_VARIABLE", &gitlab.RemoveGroupVariableOptions{
				Filter: &gitlab.VariableFilter{EnvironmentScope: tt.scope},
			}, gomock.Any()).Return(nil, tt.apiErr)
			exec := cmdtest.SetupCmdForTest(t, NewCmdDelete, false,
				cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "gitlab.com", api.WithGitLabClient(tc.Client))))

			out, err := exec(tt.cli)
			if tt.apiErr != nil {
				require.ErrorIs(t, err, tt.apiErr)
				assert.Empty(t, out.String())
				return
			}
			require.NoError(t, err)
			assert.Equal(t, fmt.Sprintf("✓ Deleted variable TEST_VARIABLE for group mygroup with scope %s.\n", tt.scope), out.String())
			assert.Empty(t, out.Stderr())
		})
	}
}
