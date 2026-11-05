//go:build !integration

package help

import (
	"bytes"
	"strings"
	"testing"

	"charm.land/fang/v2"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/commands/alias"
	"gitlab.com/gitlab-org/cli/internal/commands/alias/set"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestDedent(t *testing.T) {
	type c struct {
		input    string
		expected string
	}

	cases := []c{
		{
			input:    "      --help      Show help for command.\n      --version   Show glab version\n",
			expected: "--help      Show help for command.\n--version   Show glab version\n",
		},
		{
			input:    "      --help              Show help for command.\n  -R, --repo OWNER/REPO   Select another repository using the OWNER/REPO format\n",
			expected: "    --help              Show help for command.\n-R, --repo OWNER/REPO   Select another repository using the OWNER/REPO format\n",
		},
		{
			input:    "  line 1\n\n  line 2\n line 3",
			expected: " line 1\n\n line 2\nline 3",
		},
		{
			input:    "  line 1\n  line 2\n  line 3\n\n",
			expected: "line 1\nline 2\nline 3\n\n",
		},
		{
			input:    "\n\n\n\n\n\n",
			expected: "\n\n\n\n\n\n",
		},
		{
			input:    "",
			expected: "",
		},
	}

	for _, tt := range cases {
		got := dedent(tt.input)
		if got != tt.expected {
			t.Errorf("expected: %q, got: %q", tt.expected, got)
		}
	}
}

func TestRootHelpFunc(t *testing.T) {
	type args struct {
		command *cobra.Command
		args    []string
	}
	tests := []struct {
		name    string
		args    args
		wantOut string
	}{
		{
			name: "alias",
			args: args{
				command: alias.NewCmdAlias(cmdtest.NewTestFactory(nil)),
			},
			wantOut: "Aliases are shortcuts for longer glab commands. Use aliases to save\n  keystrokes for commands you run often, or to compose shell pipelines\n  around glab commands.\n\nUSAGE\n  alias [command] [flags]\n\nCORE COMMANDS\n  delete:     Delete an alias.\n  list:       List aliases.\n  set:        Set an alias for a longer command.",
		},

		{
			name: "test nested alias cmd",
			args: args{
				command: set.NewCmdSet(cmdtest.NewTestFactory(nil)),
				args:    []string{"set", "-h"},
			},
			wantOut: "USAGE\n  alias set <alias name> '<command>' [flags]\n\nFLAGS\n  -s, --shell ",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			streams, _, stdout, _ := cmdtest.TestIOStreams()
			cmd := tt.args.command
			if len(tt.args.args) > 0 {
				// falsify a parent command
				alias.NewCmdAlias(cmdtest.NewTestFactory(streams)).AddCommand(cmd)
			}
			RootHelpFunc(streams, cmd, tt.args.args)

			output := stdout.String()
			// Normalize whitespace for comparison
			normalizedOutput := strings.Join(strings.Fields(output), " ")
			normalizedWantOut := strings.Join(strings.Fields(tt.wantOut), " ")
			assert.Contains(t, normalizedOutput, normalizedWantOut)
		})
	}
}

func TestRootHelpFuncAnnotationSectionsMatchAnnotationSections(t *testing.T) {
	streams, _, stdout, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
	cmd := &cobra.Command{
		Use:     "child",
		Short:   "Child command.",
		Example: "glab child",
		Run:     func(*cobra.Command, []string) {},
		Annotations: map[string]string{
			"help:arguments":   "\nA child can be supplied by number.\n",
			"help:environment": "",
		},
	}

	RootHelpFunc(streams, cmd, nil)

	out := stdout.String()
	assert.Contains(t, out, "ARGUMENTS\n  A child can be supplied by number.\n")
	assert.NotContains(t, out, "ENVIRONMENT VARIABLES")
}

func TestRootUsageFunc(t *testing.T) {
	type args struct {
		command *cobra.Command
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			args: args{
				command: alias.NewCmdAlias(cmdtest.NewTestFactory(nil)),
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := RootUsageFunc(&out, tt.args.command); (err != nil) != tt.wantErr {
				t.Errorf("RootUsageFunc() error = %v, wantErr %v", err, tt.wantErr)
			}
			assert.Contains(t, out.String(), "Usage:  alias [command] [flags]")
		})
	}
}

func TestAppendAnnotationSectionsFollowFangHelp(t *testing.T) {
	streams, _, streamsOut, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
	root := &cobra.Command{Use: "glab"}
	child := &cobra.Command{
		Use:   "child",
		Short: "Child command.",
		Run:   func(*cobra.Command, []string) {},
		Annotations: map[string]string{
			"help:arguments":   "A child can be supplied by number.\n",
			"help:environment": "CHILD_VAR: a variable the child reads.\n",
		},
	}
	grandchild := &cobra.Command{Use: "grandchild", Short: "Grandchild command.", Run: func(*cobra.Command, []string) {}}
	child.AddCommand(grandchild)
	root.AddCommand(child)
	var helpOut bytes.Buffer
	root.SetOut(&helpOut)
	AppendAnnotationSections(root, streams)

	execute := func(args ...string) string {
		helpOut.Reset()
		root.SetArgs(args)
		require.NoError(t, fang.Execute(t.Context(), root, fang.WithoutCompletions(), fang.WithoutManpage(), fang.WithoutVersion()))
		assert.Empty(t, streamsOut.String(), "help must follow the command's output writer, not IOStreams")
		return helpOut.String()
	}

	out := execute("child", "--help")
	usage := strings.Index(out, "USAGE")
	arguments := strings.Index(out, "ARGUMENTS\n\n    A child can be supplied by number.\n")
	environment := strings.Index(out, "ENVIRONMENT VARIABLES\n\n    CHILD_VAR: a variable the child reads.\n")
	require.NotEqual(t, -1, usage, out)
	require.NotEqual(t, -1, arguments, out)
	require.NotEqual(t, -1, environment, out)
	assert.Less(t, usage, arguments)
	assert.Less(t, arguments, environment)

	out = execute("child", "grandchild", "--help")
	assert.Contains(t, out, "USAGE")
	assert.NotContains(t, out, "ENVIRONMENT VARIABLES")
	assert.NotContains(t, out, "ARGUMENTS")
}
