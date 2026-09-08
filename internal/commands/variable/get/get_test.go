//go:build !integration

package get

import (
	"bytes"
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

func Test_NewCmdGet(t *testing.T) {
	tests := []struct {
		name     string
		cli      string
		wants    options
		wantsErr bool
	}{
		{
			name:     "good key",
			cli:      "good_key",
			wantsErr: false,
			wants: options{
				key: "good_key",
			},
		},
		{
			name:     "bad key",
			cli:      "bad-key",
			wantsErr: true,
		},
		{
			name:     "no key",
			cli:      "",
			wantsErr: true,
		},
		{
			name: "good key for group",
			cli:  "-g group good_key",
			wants: options{
				key:   "good_key",
				group: "group",
			},
			wantsErr: false,
		},
		{
			name: "good key, with scope",
			cli:  "-s foo -g group good_key",
			wants: options{
				key:   "good_key",
				group: "group",
				scope: "foo",
			},
			wantsErr: false,
		},
		{
			name: "good key, with default scope",
			cli:  "-g group good_key",
			wants: options{
				key:   "good_key",
				group: "group",
				scope: "*",
			},
			wantsErr: false,
		},
		{
			name: "bad key for group",
			cli:  "-g group bad-key",
			wants: options{
				group: "group",
			},
			wantsErr: true,
		},
		{
			name:     "good key but no group",
			cli:      "good_key --group",
			wantsErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			io, _, _, _ := cmdtest.TestIOStreams()
			f := cmdtest.NewTestFactory(io)

			argv, err := shlex.Split(test.cli)
			require.NoError(t, err)

			var gotOpts *options
			cmd := NewCmdGet(f, func(opts *options) error {
				gotOpts = opts
				return nil
			})
			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err = cmd.ExecuteC()
			if test.wantsErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			assert.Equal(t, test.wants.key, gotOpts.key)
			assert.Equal(t, test.wants.group, gotOpts.group)
		})
	}
}

func Test_getRun_project(t *testing.T) {
	varContent := `
		TEST variable\n
		content
	`

	// GIVEN
	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockProjectVariables.EXPECT().
		GetVariable("owner/repo", "TEST_VAR", gomock.Any()).
		Return(&gitlab.ProjectVariable{
			Key:              "TEST_VAR",
			VariableType:     "env_var",
			Value:            varContent,
			Protected:        false,
			Masked:           false,
			EnvironmentScope: "*",
		}, nil, nil)

	io, _, stdout, _ := cmdtest.TestIOStreams()

	opts := &options{
		apiClient: func(repoHost string) (*api.Client, error) {
			return cmdtest.NewTestApiClient(t, nil, "", "gitlab.com", api.WithGitLabClient(testClient.Client)), nil
		},
		baseRepo: func() (glrepo.Interface, error) {
			return glrepo.New("owner", "repo", "gitlab.com"), nil
		},
		io:  io,
		key: "TEST_VAR",
	}

	// WHEN
	err := opts.run()

	// THEN
	require.NoError(t, err)
	assert.Equal(t, varContent, stdout.String())
}

func Test_getRun_group(t *testing.T) {
	varContent := `group variable content`

	// GIVEN
	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockGroupVariables.EXPECT().
		GetVariable("mygroup", "GROUP_VAR", gomock.Any()).
		Return(&gitlab.GroupVariable{
			Key:              "GROUP_VAR",
			VariableType:     "env_var",
			Value:            varContent,
			Protected:        false,
			Masked:           false,
			EnvironmentScope: "*",
		}, nil, nil)

	io, _, stdout, _ := cmdtest.TestIOStreams()

	opts := &options{
		apiClient: func(repoHost string) (*api.Client, error) {
			return cmdtest.NewTestApiClient(t, nil, "", "gitlab.com", api.WithGitLabClient(testClient.Client)), nil
		},
		baseRepo: func() (glrepo.Interface, error) {
			return glrepo.New("owner", "repo", "gitlab.com"), nil
		},
		io:    io,
		key:   "GROUP_VAR",
		group: "mygroup",
	}

	// WHEN
	err := opts.run()

	// THEN
	require.NoError(t, err)
	assert.Equal(t, varContent, stdout.String())
}
