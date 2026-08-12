//go:build !integration

package set

import (
	"bytes"
	"testing"

	"github.com/google/shlex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_NewCmdSet(t *testing.T) {
	tests := []struct {
		name     string
		cli      string
		wants    options
		stdinTTY bool
		wantsErr bool
	}{
		{
			name:     "invalid type",
			cli:      "cool_secret -g coolGroup -t 'mV'",
			wantsErr: true,
		},
		{
			name:     "value argument and value flag specified",
			cli:      `cool_secret value -v"another value"`,
			wantsErr: true,
		},
		{
			name:     "no name",
			cli:      "",
			wantsErr: true,
		},
		{
			name:     "no value, stdin is terminal",
			cli:      "cool_secret",
			stdinTTY: true,
			wantsErr: true,
		},
		{
			name: "protected var",
			cli:  `cool_secret -v"a secret" -p`,
			wants: options{
				key:       "cool_secret",
				protected: true,
				value:     "a secret",
				group:     "",
				scope:     "*",
				typ:       "env_var",
			},
		},
		{
			name: "protected var in group",
			cli:  `cool_secret --group coolGroup -v"cool"`,
			wants: options{
				key:       "cool_secret",
				protected: false,
				value:     "cool",
				group:     "coolGroup",
				scope:     "*",
				typ:       "env_var",
			},
		},
		{
			name: "leading numbers in name",
			cli:  `123_TOKEN -v"cool"`,
			wants: options{
				key:       "123_TOKEN",
				protected: false,
				value:     "cool",
				group:     "",
				scope:     "*",
				typ:       "env_var",
			},
		},
		{
			name:     "invalid characters in name",
			cli:      `BAD-SECRET -v"cool"`,
			wantsErr: true,
		},
		{
			name: "environment scope in group",
			cli:  `cool_secret --group coolGroup -v"cool" -s"production"`,
			wants: options{
				key:   "cool_secret",
				scope: "production",
				value: "cool",
				group: "coolGroup",
				typ:   "env_var",
			},
		},
		{
			name: "raw variable with flag",
			cli:  `cool_secret -r -v"$variable_name"`,
			wants: options{
				key:   "cool_secret",
				value: "$variable_name",
				raw:   true,
				group: "",
				scope: "*",
				typ:   "env_var",
			},
		},
		{
			name: "raw variable with flag in group",
			cli:  `cool_secret -r --group coolGroup -v"$variable_name"`,
			wants: options{
				key:   "cool_secret",
				value: "$variable_name",
				raw:   true,
				group: "coolGroup",
				scope: "*",
				typ:   "env_var",
			},
		},
		{
			name: "raw is false by default",
			cli:  `cool_secret -v"$variable_name"`,
			wants: options{
				key:       "cool_secret",
				value:     "$variable_name",
				raw:       false,
				group:     "",
				scope:     "*",
				protected: false,
				typ:       "env_var",
			},
		},
		{
			name: "var with description",
			cli:  `var_desc -v"var_desc" -d "cool var description"`,
			wants: options{
				key:         "var_desc",
				protected:   false,
				value:       "var_desc",
				group:       "",
				description: "cool var description",
				scope:       "*",
				typ:         "env_var",
			},
		},
		{
			name: "hidden variable",
			cli:  `var_desc -v "var_desc" --hidden`,
			wants: options{
				key:         "var_desc",
				protected:   false,
				value:       "var_desc",
				group:       "",
				description: "",
				scope:       "*",
				typ:         "env_var",
				hidden:      true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			io, _, _, _ := cmdtest.TestIOStreams()
			f := cmdtest.NewTestFactory(io)

			io.IsInTTY = tt.stdinTTY

			argv, err := shlex.Split(tt.cli)
			require.NoError(t, err)

			var gotOpts *options
			cmd := NewCmdSet(f, func(opts *options) error {
				gotOpts = opts
				return nil
			})
			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err = cmd.ExecuteC()
			if tt.wantsErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			assert.Equal(t, tt.wants.key, gotOpts.key)
			assert.Equal(t, tt.wants.value, gotOpts.value)
			assert.Equal(t, tt.wants.group, gotOpts.group)
			assert.Equal(t, tt.wants.scope, gotOpts.scope)
			assert.Equal(t, tt.wants.protected, gotOpts.protected)
			assert.Equal(t, tt.wants.description, gotOpts.description)
			assert.Equal(t, tt.wants.raw, gotOpts.raw)
			assert.Equal(t, tt.wants.masked, gotOpts.masked)
			assert.Equal(t, tt.wants.hidden, gotOpts.hidden)
			assert.Equal(t, tt.wants.typ, gotOpts.typ)
		})
	}
}

func Test_setRun_project(t *testing.T) {
	// GIVEN
	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockProjectVariables.EXPECT().
		CreateVariable("owner/repo", gomock.Any()).
		Return(&gitlab.ProjectVariable{
			Key:              "NEW_VARIABLE",
			Value:            "new value",
			VariableType:     "env_var",
			Protected:        false,
			Masked:           false,
			Raw:              false,
			EnvironmentScope: "production",
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
		key:   "NEW_VARIABLE",
		value: "new value",
		scope: "*",
	}

	// WHEN
	err := opts.run()

	// THEN
	require.NoError(t, err)
	assert.Equal(t, "✓ Created variable NEW_VARIABLE for owner/repo with scope *.\n", stdout.String())
}

func Test_setRun_group(t *testing.T) {
	// GIVEN
	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockGroupVariables.EXPECT().
		CreateVariable("mygroup", gomock.Any()).
		Return(&gitlab.GroupVariable{
			Key:              "NEW_VARIABLE",
			Value:            "new value",
			VariableType:     "env_var",
			Protected:        false,
			Masked:           false,
			Raw:              false,
			EnvironmentScope: "production",
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
		key:   "NEW_VARIABLE",
		value: "new value",
		group: "mygroup",
	}

	// WHEN
	err := opts.run()

	// THEN
	require.NoError(t, err)
	assert.Equal(t, "✓ Created variable NEW_VARIABLE for group mygroup.\n", stdout.String())
}

func Test_setRun_group_masked_and_hidden_omitted(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)

	var capturedOpts *gitlab.CreateGroupVariableOptions
	testClient.MockGroupVariables.EXPECT().
		CreateVariable("mygroup", gomock.Any()).
		DoAndReturn(func(group any, opts *gitlab.CreateGroupVariableOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.GroupVariable, *gitlab.Response, error) {
			capturedOpts = opts
			return &gitlab.GroupVariable{Key: "MY_VAR"}, nil, nil
		})

	io, _, _, _ := cmdtest.TestIOStreams()

	opts := &options{
		apiClient: func(repoHost string) (*api.Client, error) {
			return cmdtest.NewTestApiClient(t, nil, "", "gitlab.com", api.WithGitLabClient(testClient.Client)), nil
		},
		baseRepo: func() (glrepo.Interface, error) {
			return glrepo.New("owner", "repo", "gitlab.com"), nil
		},
		io:    io,
		key:   "MY_VAR",
		value: "some value",
		group: "mygroup",
	}

	err := opts.run()
	require.NoError(t, err)
	assert.Nil(t, capturedOpts.MaskedAndHidden)
}

func Test_setRun_group_masked_and_hidden_sent(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)

	var capturedOpts *gitlab.CreateGroupVariableOptions
	testClient.MockGroupVariables.EXPECT().
		CreateVariable("mygroup", gomock.Any()).
		DoAndReturn(func(group any, opts *gitlab.CreateGroupVariableOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.GroupVariable, *gitlab.Response, error) {
			capturedOpts = opts
			return &gitlab.GroupVariable{Key: "MY_VAR"}, nil, nil
		})

	io, _, _, _ := cmdtest.TestIOStreams()

	opts := &options{
		apiClient: func(repoHost string) (*api.Client, error) {
			return cmdtest.NewTestApiClient(t, nil, "", "gitlab.com", api.WithGitLabClient(testClient.Client)), nil
		},
		baseRepo: func() (glrepo.Interface, error) {
			return glrepo.New("owner", "repo", "gitlab.com"), nil
		},
		io:     io,
		key:    "MY_VAR",
		value:  "some value",
		group:  "mygroup",
		hidden: true,
	}

	err := opts.run()
	require.NoError(t, err)
	require.NotNil(t, capturedOpts.MaskedAndHidden)
	assert.True(t, *capturedOpts.MaskedAndHidden)
}

func Test_setRun_project_masked_and_hidden_omitted(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)

	var capturedOpts *gitlab.CreateProjectVariableOptions
	testClient.MockProjectVariables.EXPECT().
		CreateVariable("owner/repo", gomock.Any()).
		DoAndReturn(func(pid any, opts *gitlab.CreateProjectVariableOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.ProjectVariable, *gitlab.Response, error) {
			capturedOpts = opts
			return &gitlab.ProjectVariable{Key: "MY_VAR"}, nil, nil
		})

	io, _, _, _ := cmdtest.TestIOStreams()

	opts := &options{
		apiClient: func(repoHost string) (*api.Client, error) {
			return cmdtest.NewTestApiClient(t, nil, "", "gitlab.com", api.WithGitLabClient(testClient.Client)), nil
		},
		baseRepo: func() (glrepo.Interface, error) {
			return glrepo.New("owner", "repo", "gitlab.com"), nil
		},
		io:    io,
		key:   "MY_VAR",
		value: "some value",
		scope: "*",
	}

	err := opts.run()
	require.NoError(t, err)
	assert.Nil(t, capturedOpts.MaskedAndHidden)
}

func Test_setRun_project_masked_and_hidden_sent(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)

	var capturedOpts *gitlab.CreateProjectVariableOptions
	testClient.MockProjectVariables.EXPECT().
		CreateVariable("owner/repo", gomock.Any()).
		DoAndReturn(func(pid any, opts *gitlab.CreateProjectVariableOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.ProjectVariable, *gitlab.Response, error) {
			capturedOpts = opts
			return &gitlab.ProjectVariable{Key: "MY_VAR"}, nil, nil
		})

	io, _, _, _ := cmdtest.TestIOStreams()

	opts := &options{
		apiClient: func(repoHost string) (*api.Client, error) {
			return cmdtest.NewTestApiClient(t, nil, "", "gitlab.com", api.WithGitLabClient(testClient.Client)), nil
		},
		baseRepo: func() (glrepo.Interface, error) {
			return glrepo.New("owner", "repo", "gitlab.com"), nil
		},
		io:     io,
		key:    "MY_VAR",
		value:  "some value",
		scope:  "*",
		hidden: true,
	}

	err := opts.run()
	require.NoError(t, err)
	require.NotNil(t, capturedOpts.MaskedAndHidden)
	assert.True(t, *capturedOpts.MaskedAndHidden)
}
