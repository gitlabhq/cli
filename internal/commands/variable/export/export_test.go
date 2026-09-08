//go:build !integration

package export

import (
	"bytes"
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
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_NewCmdExport(t *testing.T) {
	tests := []struct {
		name     string
		cli      string
		wants    options
		wantsErr bool
	}{
		{
			name:     "no arguments",
			cli:      "",
			wantsErr: false,
		},
		{
			name:     "with group",
			cli:      "--group STH",
			wantsErr: false,
			wants: options{
				group: "STH",
			},
		},
		{
			name:     "missing group",
			cli:      "--group",
			wantsErr: true,
			wants: options{
				group: "STH",
			},
		},
		{
			name:     "with pagination",
			cli:      "--page 11 --per-page 12",
			wantsErr: false,
		},
		{
			name:     "with invalid pagination",
			cli:      "--page aa --per-page bb",
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
			cmd := NewCmdExport(f, func(opts *options) error {
				gotOpts = opts
				return nil
			})
			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err = cmd.ExecuteC()
			if test.wantsErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)

			assert.Equal(t, test.wants.group, gotOpts.group)
		})
	}
}

func Test_exportRun_project(t *testing.T) {
	mockProjectVariables := []*gitlab.ProjectVariable{
		{
			Key:              "VAR1",
			Value:            "value1",
			EnvironmentScope: "dev",
		},
		{
			Key:              "VAR2",
			Value:            "value2.1",
			EnvironmentScope: "prod",
		},
		{
			Key:              "VAR2",
			Value:            "value2.2",
			EnvironmentScope: "*",
		},
		{
			Key:              "VAR3",
			Value:            "value3",
			EnvironmentScope: "dev/a",
		},
		{
			Key:              "VAR4",
			Value:            "value4.1",
			EnvironmentScope: "dev/b",
		},
		{
			Key:              "VAR4",
			Value:            "value4.2",
			EnvironmentScope: "feature-1",
		},
		{
			Key:              "VAR4",
			Value:            "value4.3",
			EnvironmentScope: "feature-2",
		},
		{
			Key:              "VAR5",
			Value:            "value5",
			EnvironmentScope: "feature-*",
		},
	}

	tests := []struct {
		scope          string
		format         string
		expectedStderr string
		expectedStdout string
	}{
		{
			scope:          "*",
			format:         "json",
			expectedStderr: "Exporting variables from the owner/repo project:\n",
			expectedStdout: `[{"key":"VAR1","value":"value1","variable_type":"","protected":false,"masked":false,"hidden":false,"raw":false,"environment_scope":"dev","description":""},{"key":"VAR2","value":"value2.1","variable_type":"","protected":false,"masked":false,"hidden":false,"raw":false,"environment_scope":"prod","description":""},{"key":"VAR2","value":"value2.2","variable_type":"","protected":false,"masked":false,"hidden":false,"raw":false,"environment_scope":"*","description":""},{"key":"VAR3","value":"value3","variable_type":"","protected":false,"masked":false,"hidden":false,"raw":false,"environment_scope":"dev/a","description":""},{"key":"VAR4","value":"value4.1","variable_type":"","protected":false,"masked":false,"hidden":false,"raw":false,"environment_scope":"dev/b","description":""},{"key":"VAR4","value":"value4.2","variable_type":"","protected":false,"masked":false,"hidden":false,"raw":false,"environment_scope":"feature-1","description":""},{"key":"VAR4","value":"value4.3","variable_type":"","protected":false,"masked":false,"hidden":false,"raw":false,"environment_scope":"feature-2","description":""},{"key":"VAR5","value":"value5","variable_type":"","protected":false,"masked":false,"hidden":false,"raw":false,"environment_scope":"feature-*","description":""}]` + "\n",
		},
		{
			scope:          "dev/b",
			format:         "json",
			expectedStderr: "Exporting variables from the owner/repo project:\n",
			expectedStdout: `[{"key":"VAR2","value":"value2.2","variable_type":"","protected":false,"masked":false,"hidden":false,"raw":false,"environment_scope":"*","description":""},{"key":"VAR4","value":"value4.1","variable_type":"","protected":false,"masked":false,"hidden":false,"raw":false,"environment_scope":"dev/b","description":""}]` + "\n",
		},
		{
			scope:          "*",
			format:         "env",
			expectedStderr: "Exporting variables from the owner/repo project:\n",
			expectedStdout: "VAR1=\"value1\"\nVAR2=\"value2.1\"\nVAR3=\"value3\"\nVAR4=\"value4.1\"\nVAR4=\"value4.2\"\nVAR4=\"value4.3\"\nVAR5=\"value5\"\n",
		},
		{
			scope:          "*",
			format:         "export",
			expectedStderr: "Exporting variables from the owner/repo project:\n",
			expectedStdout: "export VAR1=\"value1\"\nexport VAR2=\"value2.1\"\nexport VAR3=\"value3\"\nexport VAR4=\"value4.1\"\nexport VAR4=\"value4.2\"\nexport VAR4=\"value4.3\"\nexport VAR5=\"value5\"\n",
		},
		{
			scope:          "dev",
			format:         "env",
			expectedStderr: "Exporting variables from the owner/repo project:\n",
			expectedStdout: "VAR1=\"value1\"\nVAR2=\"value2.2\"\n",
		},
		{
			scope:          "dev",
			format:         "export",
			expectedStderr: "Exporting variables from the owner/repo project:\n",
			expectedStdout: "export VAR1=\"value1\"\nexport VAR2=\"value2.2\"\n",
		},
		{
			scope:          "prod",
			format:         "env",
			expectedStderr: "Exporting variables from the owner/repo project:\n",
			expectedStdout: "VAR2=\"value2.1\"\n",
		},
		{
			scope:          "prod",
			format:         "export",
			expectedStderr: "Exporting variables from the owner/repo project:\n",
			expectedStdout: "export VAR2=\"value2.1\"\n",
		},
		{
			scope:          "dev/a",
			format:         "env",
			expectedStderr: "Exporting variables from the owner/repo project:\n",
			expectedStdout: "VAR3=\"value3\"\nVAR2=\"value2.2\"\n",
		},
		{
			scope:          "dev/a",
			format:         "export",
			expectedStderr: "Exporting variables from the owner/repo project:\n",
			expectedStdout: "export VAR3=\"value3\"\nexport VAR2=\"value2.2\"\n",
		},
		{
			scope:          "feature-1",
			format:         "env",
			expectedStderr: "Exporting variables from the owner/repo project:\n",
			expectedStdout: "VAR4=\"value4.2\"\nVAR2=\"value2.2\"\nVAR5=\"value5\"\n",
		},
		{
			scope:          "feature-1",
			format:         "export",
			expectedStderr: "Exporting variables from the owner/repo project:\n",
			expectedStdout: "export VAR4=\"value4.2\"\nexport VAR2=\"value2.2\"\nexport VAR5=\"value5\"\n",
		},
		{
			scope:          "feature-2",
			format:         "env",
			expectedStderr: "Exporting variables from the owner/repo project:\n",
			expectedStdout: "VAR4=\"value4.3\"\nVAR2=\"value2.2\"\nVAR5=\"value5\"\n",
		},
		{
			scope:          "feature-2",
			format:         "export",
			expectedStderr: "Exporting variables from the owner/repo project:\n",
			expectedStdout: "export VAR4=\"value4.3\"\nexport VAR2=\"value2.2\"\nexport VAR5=\"value5\"\n",
		},
	}

	for _, test := range tests {
		t.Run(test.scope+"_"+test.format, func(t *testing.T) {
			tc := gitlabtesting.NewTestClient(t)

			tc.MockProjectVariables.EXPECT().ListVariables("owner/repo", gomock.Any(), gomock.Any()).Return(mockProjectVariables, nil, nil)

			exec := cmdtest.SetupCmdForTest(
				t,
				func(f cmdutils.Factory) *cobra.Command {
					return NewCmdExport(f, nil)
				},
				false,
				cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "testtoken", "gitlab.example.com", api.WithGitLabClient(tc.Client))),
				cmdtest.WithBaseRepo("owner", "repo", glinstance.DefaultHostname),
			)

			out, err := exec(fmt.Sprintf("--page 1 --per-page 10 --format %s --scope %s", test.format, test.scope))
			require.NoError(t, err)
			assert.Equal(t, test.expectedStderr, out.ErrBuf.String())
			assert.Equal(t, test.expectedStdout, out.OutBuf.String())
		})
	}
}

func Test_exportRun_group(t *testing.T) {
	mockGroupVariables := []*gitlab.GroupVariable{
		{
			Key:              "VAR1",
			Value:            "\"value1\"",
			EnvironmentScope: "dev",
		},
		{
			Key:              "VAR2",
			Value:            "value2.1",
			EnvironmentScope: "prod",
		},
		{
			Key:              "VAR2",
			Value:            "value2.2",
			EnvironmentScope: "*",
		},
		{
			Key:              "VAR3",
			Value:            "value3",
			EnvironmentScope: "dev/a",
		},
		{
			Key:              "VAR4",
			Value:            "value4.1",
			EnvironmentScope: "dev/b",
		},
		{
			Key:              "VAR4",
			Value:            "value4.2",
			EnvironmentScope: "feature-1",
		},
		{
			Key:              "VAR4",
			Value:            "value4.3",
			EnvironmentScope: "feature-2",
		},
		{
			Key:              "VAR5",
			Value:            "value5",
			EnvironmentScope: "feature-*",
		},
	}

	tests := []struct {
		scope          string
		format         string
		expectedStderr string
		expectedStdout string
	}{
		{
			scope:          "*",
			format:         "json",
			expectedStderr: "Exporting variables from the group group:\n",
			expectedStdout: `[{"key":"VAR1","value":"\"value1\"","variable_type":"","protected":false,"masked":false,"hidden":false,"raw":false,"environment_scope":"dev","description":""},{"key":"VAR2","value":"value2.1","variable_type":"","protected":false,"masked":false,"hidden":false,"raw":false,"environment_scope":"prod","description":""},{"key":"VAR2","value":"value2.2","variable_type":"","protected":false,"masked":false,"hidden":false,"raw":false,"environment_scope":"*","description":""},{"key":"VAR3","value":"value3","variable_type":"","protected":false,"masked":false,"hidden":false,"raw":false,"environment_scope":"dev/a","description":""},{"key":"VAR4","value":"value4.1","variable_type":"","protected":false,"masked":false,"hidden":false,"raw":false,"environment_scope":"dev/b","description":""},{"key":"VAR4","value":"value4.2","variable_type":"","protected":false,"masked":false,"hidden":false,"raw":false,"environment_scope":"feature-1","description":""},{"key":"VAR4","value":"value4.3","variable_type":"","protected":false,"masked":false,"hidden":false,"raw":false,"environment_scope":"feature-2","description":""},{"key":"VAR5","value":"value5","variable_type":"","protected":false,"masked":false,"hidden":false,"raw":false,"environment_scope":"feature-*","description":""}]` + "\n",
		},
		{
			scope:          "dev/b",
			format:         "json",
			expectedStderr: "Exporting variables from the group group:\n",
			expectedStdout: `[{"key":"VAR2","value":"value2.2","variable_type":"","protected":false,"masked":false,"hidden":false,"raw":false,"environment_scope":"*","description":""},{"key":"VAR4","value":"value4.1","variable_type":"","protected":false,"masked":false,"hidden":false,"raw":false,"environment_scope":"dev/b","description":""}]` + "\n",
		},
		{
			scope:          "*",
			format:         "env",
			expectedStderr: "Exporting variables from the group group:\n",
			expectedStdout: "VAR1=\"value1\"\nVAR2=value2.1\nVAR3=value3\nVAR4=value4.1\nVAR4=value4.2\nVAR4=value4.3\nVAR5=value5\n",
		},
		{
			scope:          "*",
			format:         "export",
			expectedStderr: "Exporting variables from the group group:\n",
			expectedStdout: "export VAR1=\"value1\"\nexport VAR2=value2.1\nexport VAR3=value3\nexport VAR4=value4.1\nexport VAR4=value4.2\nexport VAR4=value4.3\nexport VAR5=value5\n",
		},
		{
			scope:          "dev",
			format:         "env",
			expectedStderr: "Exporting variables from the group group:\n",
			expectedStdout: "VAR1=\"value1\"\nVAR2=value2.2\n",
		},
		{
			scope:          "dev",
			format:         "export",
			expectedStderr: "Exporting variables from the group group:\n",
			expectedStdout: "export VAR1=\"value1\"\nexport VAR2=value2.2\n",
		},
		{
			scope:          "prod",
			format:         "env",
			expectedStderr: "Exporting variables from the group group:\n",
			expectedStdout: "VAR2=value2.1\n",
		},
		{
			scope:          "prod",
			format:         "export",
			expectedStderr: "Exporting variables from the group group:\n",
			expectedStdout: "export VAR2=value2.1\n",
		},
		{
			scope:          "dev/a",
			format:         "env",
			expectedStderr: "Exporting variables from the group group:\n",
			expectedStdout: "VAR3=value3\nVAR2=value2.2\n",
		},
		{
			scope:          "dev/a",
			format:         "export",
			expectedStderr: "Exporting variables from the group group:\n",
			expectedStdout: "export VAR3=value3\nexport VAR2=value2.2\n",
		},
		{
			scope:          "feature-1",
			format:         "env",
			expectedStderr: "Exporting variables from the group group:\n",
			expectedStdout: "VAR4=value4.2\nVAR2=value2.2\nVAR5=value5\n",
		},
		{
			scope:          "feature-1",
			format:         "export",
			expectedStderr: "Exporting variables from the group group:\n",
			expectedStdout: "export VAR4=value4.2\nexport VAR2=value2.2\nexport VAR5=value5\n",
		},
		{
			scope:          "feature-2",
			format:         "env",
			expectedStderr: "Exporting variables from the group group:\n",
			expectedStdout: "VAR4=value4.3\nVAR2=value2.2\nVAR5=value5\n",
		},
		{
			scope:          "feature-2",
			format:         "export",
			expectedStderr: "Exporting variables from the group group:\n",
			expectedStdout: "export VAR4=value4.3\nexport VAR2=value2.2\nexport VAR5=value5\n",
		},
	}

	for _, test := range tests {
		t.Run(test.scope+"_"+test.format, func(t *testing.T) {
			tc := gitlabtesting.NewTestClient(t)

			tc.MockGroupVariables.EXPECT().ListVariables("group", gomock.Any(), gomock.Any()).Return(mockGroupVariables, nil, nil)

			exec := cmdtest.SetupCmdForTest(
				t,
				func(f cmdutils.Factory) *cobra.Command {
					return NewCmdExport(f, nil)
				},
				false,
				cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "testtoken", "gitlab.example.com", api.WithGitLabClient(tc.Client))),
				cmdtest.WithBaseRepo("owner", "repo", glinstance.DefaultHostname),
			)

			out, err := exec(fmt.Sprintf("--page 1 --per-page 10 --group group --format %s --scope %s", test.format, test.scope))
			require.NoError(t, err)
			assert.Equal(t, test.expectedStderr, out.ErrBuf.String())
			assert.Equal(t, test.expectedStdout, out.OutBuf.String())
		})
	}
}
