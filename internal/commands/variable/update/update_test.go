//go:build !integration

package update

import (
	"bytes"
	"errors"
	"fmt"
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
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_NewCmdUpdate(t *testing.T) {
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
				typ:       "env_var",
				scope:     "*",
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
				typ:       "env_var",
				scope:     "*",
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
				key:   "cool_secret",
				value: "$variable_name",
				raw:   false,
				group: "",
				scope: "*",
				typ:   "env_var",
			},
		},
		{
			name: "var with description",
			cli:  `cool_secret -d"description"`,
			wants: options{
				key:         "cool_secret",
				raw:         false,
				group:       "",
				scope:       "*",
				typ:         "env_var",
				description: "description",
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			io, _, _, _ := cmdtest.TestIOStreams()
			f := cmdtest.NewTestFactory(io)

			io.IsInTTY = tt.stdinTTY

			argv, err := shlex.Split(tt.cli)
			require.NoError(t, err)

			var gotOpts *options
			cmd := NewCmdUpdate(f, func(opts *options) error {
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
			assert.Equal(t, tt.wants.protected, gotOpts.protected)
			assert.Equal(t, tt.wants.raw, gotOpts.raw)
			assert.Equal(t, tt.wants.masked, gotOpts.masked)
			assert.Equal(t, tt.wants.typ, gotOpts.typ)
			assert.Equal(t, tt.wants.description, gotOpts.description)
			assert.Equal(t, tt.wants.scope, gotOpts.scope)
		})
	}
}

func Test_updateRun_project(t *testing.T) {
	// GIVEN
	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockProjectVariables.EXPECT().
		UpdateVariable("owner/repo", "TEST_VARIABLE", gomock.Any(), gomock.Any()).
		Return(&gitlab.ProjectVariable{
			Key:          "TEST_VARIABLE",
			Value:        "foo",
			VariableType: "env_var",
			Protected:    false,
			Masked:       false,
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
		key:   "TEST_VARIABLE",
		value: "foo",
		scope: "*",
	}

	// WHEN
	err := opts.run(t.Context())

	// THEN
	require.NoError(t, err)
	assert.Equal(t, "✓ Updated variable TEST_VARIABLE for project owner/repo with scope *.\n", stdout.String())
}

func Test_updateRun_group(t *testing.T) {
	// GIVEN
	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockGroupVariables.EXPECT().
		GetVariable("mygroup", "TEST_VARIABLE", &gitlab.GetGroupVariableOptions{
			Filter: &gitlab.VariableFilter{EnvironmentScope: "*"},
		}, gomock.Any()).
		Return(&gitlab.GroupVariable{Key: "TEST_VARIABLE", EnvironmentScope: "*"}, nil, nil)
	testClient.MockGroupVariables.EXPECT().
		UpdateVariable("mygroup", "TEST_VARIABLE", gomock.Any(), gomock.Any()).
		Return(&gitlab.GroupVariable{
			Key:          "TEST_VARIABLE",
			Value:        "blargh",
			VariableType: "env_var",
			Protected:    false,
			Masked:       false,
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
		key:   "TEST_VARIABLE",
		value: "blargh",
		group: "mygroup",
		scope: "*",
	}

	// WHEN
	err := opts.run(t.Context())

	// THEN
	require.NoError(t, err)
	assert.Equal(t, "✓ Updated variable TEST_VARIABLE for group mygroup with scope *.\n", stdout.String())
}

func Test_updateGroupScope(t *testing.T) {
	t.Parallel()

	apiErr := errors.New("group variable update failed")
	for _, tt := range []struct {
		name        string
		cli         string
		scope       string
		stdin       string
		typ         *gitlab.VariableTypeValue
		protected   *bool
		masked      *bool
		raw         *bool
		description *string
		apiErr      error
	}{
		{name: "default scope", cli: "TEST_VARIABLE -g mygroup -v value", scope: "*"},
		{name: "explicit wildcard", cli: "TEST_VARIABLE -g mygroup -s '*' -v value", scope: "*"},
		{name: "production scope", cli: "TEST_VARIABLE --group mygroup --scope prod -v value", scope: "prod"},
		{name: "wildcard environment", cli: "TEST_VARIABLE -g mygroup -s 'review/*' -v value", scope: "review/*"},
		{name: "protected file from stdin", cli: "TEST_VARIABLE -p --scope prod -g mygroup -t file", scope: "prod", stdin: "value", typ: new(gitlab.FileVariableType), protected: new(true)},
		{name: "explicit false attributes", cli: "TEST_VARIABLE -g mygroup -s prod -v value --protected=false --masked=false --raw=false", scope: "prod", protected: new(false), masked: new(false), raw: new(false)},
		{name: "explicit true attributes", cli: "TEST_VARIABLE -g mygroup -s prod -v value -p -m -r", scope: "prod", protected: new(true), masked: new(true), raw: new(true)},
		{name: "explicit empty description", cli: `TEST_VARIABLE -g mygroup -s prod -v value -d ""`, scope: "prod", description: new("")},
		{name: "description only", cli: `TEST_VARIABLE -g mygroup -s prod -v value -d "new description"`, scope: "prod", description: new("new description")},
		{name: "explicit default type", cli: "TEST_VARIABLE -g mygroup -s prod -v value -t env_var", scope: "prod", typ: new(gitlab.EnvVariableType)},
		{name: "API error", cli: "TEST_VARIABLE -g mygroup -s prod -v value", scope: "prod", apiErr: apiErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tc := gitlabtesting.NewTestClient(t)
			tc.MockGroupVariables.EXPECT().GetVariable("mygroup", "TEST_VARIABLE", &gitlab.GetGroupVariableOptions{
				Filter: &gitlab.VariableFilter{EnvironmentScope: tt.scope},
			}, gomock.Any()).Return(&gitlab.GroupVariable{Key: "TEST_VARIABLE", EnvironmentScope: tt.scope}, nil, nil)
			tc.MockGroupVariables.EXPECT().UpdateVariable("mygroup", "TEST_VARIABLE", &gitlab.UpdateGroupVariableOptions{
				Value:            new("value"),
				VariableType:     tt.typ,
				Masked:           tt.masked,
				Protected:        tt.protected,
				Raw:              tt.raw,
				EnvironmentScope: new(tt.scope),
				Filter:           &gitlab.VariableFilter{EnvironmentScope: tt.scope},
				Description:      tt.description,
			}, gomock.Any()).Return(nil, nil, tt.apiErr)
			exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
				return NewCmdUpdate(f, nil)
			}, false, cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "gitlab.com", api.WithGitLabClient(tc.Client))), cmdtest.WithStdin(tt.stdin))

			out, err := exec(tt.cli)
			if tt.apiErr != nil {
				require.ErrorIs(t, err, tt.apiErr)
				assert.Empty(t, out.String())
				return
			}
			require.NoError(t, err)
			assert.Equal(t, fmt.Sprintf("✓ Updated variable TEST_VARIABLE for group mygroup with scope %s.\n", tt.scope), out.String())
			assert.Empty(t, out.Stderr())
		})
	}
}

func Test_updateGroupScopePreflight(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name      string
		scope     string
		variable  *gitlab.GroupVariable
		lookupErr error
		wantErr   string
	}{
		{
			name:      "mistyped scope returns 404",
			scope:     "prdo",
			lookupErr: errors.New("404 group variable not found"),
			wantErr:   "404 group variable not found",
		},
		{
			name:  "API returns a different scope",
			scope: "prdo",
			variable: &gitlab.GroupVariable{
				Key: "TEST_VARIABLE", EnvironmentScope: "*",
			},
			wantErr: `group variable "TEST_VARIABLE" does not exist in scope "prdo"`,
		},
		{
			name:    "nil lookup response",
			scope:   "prod",
			wantErr: `group variable "TEST_VARIABLE" does not exist in scope "prod"`,
		},
		{
			name:      "read access denied",
			scope:     "prod",
			lookupErr: errors.New("403 forbidden"),
			wantErr:   "403 forbidden",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tc := gitlabtesting.NewTestClient(t)
			tc.MockGroupVariables.EXPECT().GetVariable("mygroup", "TEST_VARIABLE", &gitlab.GetGroupVariableOptions{
				Filter: &gitlab.VariableFilter{EnvironmentScope: tt.scope},
			}, gomock.Any()).Return(tt.variable, nil, tt.lookupErr)
			// No UpdateVariable expectation: attempting a write must fail this test.

			exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
				return NewCmdUpdate(f, nil)
			}, false, cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "gitlab.com", api.WithGitLabClient(tc.Client))))

			out, err := exec(fmt.Sprintf("TEST_VARIABLE -g mygroup -s %s -v changed", tt.scope))
			require.ErrorContains(t, err, tt.wantErr)
			assert.Empty(t, out.String())
			assert.Empty(t, out.Stderr())
		})
	}
}

func Test_NewCmdUpdate_changedFlags(t *testing.T) {
	tests := []struct {
		name string
		cli  string
		want options
	}{
		{
			name: "no optional flags",
			cli:  `cool_secret -v"a secret"`,
			want: options{},
		},
		{
			name: "protected only",
			cli:  `cool_secret -v"a secret" -p`,
			want: options{protectedChanged: true},
		},
		{
			name: "explicitly disabled boolean flag counts as changed",
			cli:  `cool_secret -v"a secret" --protected=false --masked=false --raw=false`,
			want: options{protectedChanged: true, maskedChanged: true, rawChanged: true},
		},
		{
			name: "type and description",
			cli:  `cool_secret -v"a secret" -t file -d ""`,
			want: options{typeChanged: true, descriptionChanged: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			io, _, _, _ := cmdtest.TestIOStreams()
			f := cmdtest.NewTestFactory(io)

			argv, err := shlex.Split(tt.cli)
			require.NoError(t, err)

			var gotOpts *options
			cmd := NewCmdUpdate(f, func(opts *options) error {
				gotOpts = opts
				return nil
			})
			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err = cmd.ExecuteC()
			require.NoError(t, err)

			assert.Equal(t, tt.want.typeChanged, gotOpts.typeChanged)
			assert.Equal(t, tt.want.protectedChanged, gotOpts.protectedChanged)
			assert.Equal(t, tt.want.maskedChanged, gotOpts.maskedChanged)
			assert.Equal(t, tt.want.rawChanged, gotOpts.rawChanged)
			assert.Equal(t, tt.want.descriptionChanged, gotOpts.descriptionChanged)
		})
	}
}

func Test_updateRun_project_onlySendsChangedAttributes(t *testing.T) {
	tests := []struct {
		name  string
		cli   string
		check func(t *testing.T, got *gitlab.UpdateProjectVariableOptions)
	}{
		{
			name: "value only leaves every other attribute unset",
			cli:  `TEST_VARIABLE -v"foo"`,
			check: func(t *testing.T, got *gitlab.UpdateProjectVariableOptions) {
				t.Helper()
				assert.Equal(t, "foo", *got.Value)
				assert.Nil(t, got.VariableType)
				assert.Nil(t, got.Protected)
				assert.Nil(t, got.Masked)
				assert.Nil(t, got.Raw)
				assert.Nil(t, got.Description)
			},
		},
		{
			name: "only the passed attributes are sent",
			cli:  `TEST_VARIABLE -v"foo" -p -d"new description"`,
			check: func(t *testing.T, got *gitlab.UpdateProjectVariableOptions) {
				t.Helper()
				require.NotNil(t, got.Protected)
				assert.True(t, *got.Protected)
				require.NotNil(t, got.Description)
				assert.Equal(t, "new description", *got.Description)
				assert.Nil(t, got.VariableType)
				assert.Nil(t, got.Masked)
				assert.Nil(t, got.Raw)
			},
		},
		{
			name: "attributes can be turned off explicitly",
			cli:  `TEST_VARIABLE -v"foo" --protected=false --masked=false --raw=false`,
			check: func(t *testing.T, got *gitlab.UpdateProjectVariableOptions) {
				t.Helper()
				require.NotNil(t, got.Protected)
				assert.False(t, *got.Protected)
				require.NotNil(t, got.Masked)
				assert.False(t, *got.Masked)
				require.NotNil(t, got.Raw)
				assert.False(t, *got.Raw)
			},
		},
		{
			name: "type and an empty description are sent when passed",
			cli:  `TEST_VARIABLE -v"foo" -t file -d ""`,
			check: func(t *testing.T, got *gitlab.UpdateProjectVariableOptions) {
				t.Helper()
				require.NotNil(t, got.VariableType)
				assert.Equal(t, gitlab.FileVariableType, *got.VariableType)
				require.NotNil(t, got.Description)
				assert.Empty(t, *got.Description)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got *gitlab.UpdateProjectVariableOptions
			testClient := gitlabtesting.NewTestClient(t)
			testClient.MockProjectVariables.EXPECT().
				UpdateVariable("owner/repo", "TEST_VARIABLE", gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ any, _ string, opt *gitlab.UpdateProjectVariableOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.ProjectVariable, *gitlab.Response, error) {
					got = opt
					return &gitlab.ProjectVariable{Key: "TEST_VARIABLE"}, nil, nil
				})

			io, _, _, _ := cmdtest.TestIOStreams()
			f := cmdtest.NewTestFactory(io,
				cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "gitlab.com", api.WithGitLabClient(testClient.Client))),
				cmdtest.WithBaseRepo("owner", "repo", "gitlab.com"),
			)

			argv, err := shlex.Split(tt.cli)
			require.NoError(t, err)

			cmd := NewCmdUpdate(f, nil)
			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err = cmd.ExecuteC()
			require.NoError(t, err)
			require.NotNil(t, got)
			tt.check(t, got)
		})
	}
}

func Test_updateRun_group_onlySendsChangedAttributes(t *testing.T) {
	var got *gitlab.UpdateGroupVariableOptions
	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockGroupVariables.EXPECT().
		GetVariable("mygroup", "TEST_VARIABLE", &gitlab.GetGroupVariableOptions{
			Filter: &gitlab.VariableFilter{EnvironmentScope: "*"},
		}, gomock.Any()).
		Return(&gitlab.GroupVariable{Key: "TEST_VARIABLE", EnvironmentScope: "*"}, nil, nil)
	testClient.MockGroupVariables.EXPECT().
		UpdateVariable("mygroup", "TEST_VARIABLE", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ any, _ string, opt *gitlab.UpdateGroupVariableOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.GroupVariable, *gitlab.Response, error) {
			got = opt
			return &gitlab.GroupVariable{Key: "TEST_VARIABLE"}, nil, nil
		})

	io, _, _, _ := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(io,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "gitlab.com", api.WithGitLabClient(testClient.Client))),
		cmdtest.WithBaseRepo("owner", "repo", "gitlab.com"),
	)

	cmd := NewCmdUpdate(f, nil)
	cmd.SetArgs([]string{"TEST_VARIABLE", "-v", "foo", "--group", "mygroup", "-m"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	_, err := cmd.ExecuteC()
	require.NoError(t, err)
	require.NotNil(t, got)

	require.NotNil(t, got.Masked)
	assert.True(t, *got.Masked)
	assert.Nil(t, got.Protected)
	assert.Nil(t, got.Raw)
	assert.Nil(t, got.VariableType)
	assert.Nil(t, got.Description)
}
