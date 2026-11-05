//go:build !integration

package commands

import (
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/help"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestMain(m *testing.M) {
	cmdtest.InitTest(m, "")
}

func TestRootVersion(t *testing.T) {
	ios, _, stdout, _ := cmdtest.TestIOStreams()
	rootCmd := NewCmdRoot(cmdutils.NewFactory(ios, false, config.NewBlankConfig(), api.BuildInfo{Version: "v1.0.0", Commit: "abcdefgh"}))
	rootCmd.SetOut(stdout)
	assert.NoError(t, rootCmd.Flag("version").Value.Set("true"))
	require.NoError(t, rootCmd.Execute())

	assert.Equal(t, "glab 1.0.0 (abcdefgh)\n", stdout.String())
}

func TestRootNoArg(t *testing.T) {
	ios, _, stdout, _ := cmdtest.TestIOStreams()
	rootCmd := NewCmdRoot(cmdutils.NewFactory(ios, false, config.NewBlankConfig(), api.BuildInfo{Version: "v1.0.0", Commit: "abcdefgh"}))
	require.NoError(t, rootCmd.Execute())

	assert.Contains(t, stdout.String(), "GLab is an open source GitLab CLI tool that brings GitLab to your command line.\n")
	assert.Contains(t, stdout.String(), `USAGE
  glab <command> <subcommand> [flags]

CORE COMMANDS`)
}

// Regression test for gitlab-org/cli#8371: pflag/cobra deprecation warnings
// must not land on stdout. We deliberately do NOT call rootCmd.SetOut in the
// test — cobra's c.Print falls back to os.Stderr in production, and we want
// the test to fail if anyone re-wires rootCmd.Out to f.IO().StdOut (in which
// case the deprecation would land in the captured stdout buffer below).
// Positive "appears on stderr" coverage is left to manual spot-checks since
// we cannot capture os.Stderr from cmdtest without heavier plumbing.
func TestDeprecationWarningStaysOffStdOut(t *testing.T) {
	ios, _, stdout, _ := cmdtest.TestIOStreams()
	rootCmd := NewCmdRoot(cmdutils.NewFactory(ios, false, config.NewBlankConfig(), api.BuildInfo{Version: "v1.0.0", Commit: "abcdefgh"}))

	child := &cobra.Command{
		Use:  "deprecation-test-child",
		RunE: func(cmd *cobra.Command, args []string) error { return nil },
	}
	child.Flags().Bool("old", false, "old flag")
	require.NoError(t, child.Flags().MarkDeprecated("old", "use --new instead"))
	rootCmd.AddCommand(child)

	rootCmd.SetArgs([]string{"deprecation-test-child", "--old"})
	require.NoError(t, rootCmd.Execute())

	assert.NotContains(t, stdout.String(), "deprecated", "deprecation warning leaked to stdout")
}

// NewCmdRoot must not wire cobra's Out to f.IO().StdOut. Doing so routes
// every diagnostic message cobra prints (deprecation warnings, "unknown help
// topic", usage-on-error) onto the stdout data channel — see
// gitlab-org/cli#8371 and spf13/cobra#1708.
func TestRootDoesNotWireCobraOutToStdOut(t *testing.T) {
	ios, _, stdout, _ := cmdtest.TestIOStreams()
	rootCmd := NewCmdRoot(cmdutils.NewFactory(ios, false, config.NewBlankConfig(), api.BuildInfo{Version: "v1.0.0", Commit: "abcdefgh"}))

	// OutOrStderr returns cmd.Out if set, otherwise os.Stderr.
	// We assert it is NOT the StdOut buffer the factory was built with.
	got := rootCmd.OutOrStderr()
	assert.NotSame(t, stdout, got, "rootCmd.Out must not be wired to StdOut")
}

// TestAllDeprecatedFlagsRouteToStderr walks the entire registered command
// tree, finds every deprecated flag, and exercises each one through
// ParseFlags. It asserts no deprecation message leaks to the stdout channel.
//
// This is the auto-scaling regression net for gitlab-org/cli#8371: any
// deprecated flag added in the future is exercised automatically. If anyone
// re-wires rootCmd.SetOut to f.IO().StdOut, the deprecation warning would
// land in the captured stdout buffer below and this test fails.
//
// We deliberately do NOT override rootCmd.SetOut in the test. In production
// cobra's c.Print falls back to os.Stderr when Out is unset, and that is the
// behavior we want to verify. Capturing os.Stderr to assert positive
// presence would require global state hackery; manual spot-checks cover
// that side.
func TestAllDeprecatedFlagsRouteToStderr(t *testing.T) {
	ios, _, stdout, _ := cmdtest.TestIOStreams()
	rootCmd := NewCmdRoot(cmdutils.NewFactory(ios, false, config.NewBlankConfig(), api.BuildInfo{Version: "v1.0.0", Commit: "abcdefgh"}))

	type target struct {
		cmd  *cobra.Command
		flag *pflag.Flag
	}
	var targets []target

	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		c.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if f.Deprecated != "" {
				targets = append(targets, target{c, f})
			}
		})
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)

	require.NotEmpty(t, targets, "expected to find deprecated flags in the command tree — did the walk break?")

	for _, tg := range targets {
		name := tg.cmd.CommandPath() + " --" + tg.flag.Name
		t.Run(name, func(t *testing.T) {
			stdout.Reset()

			// Build a parseable arg. Bool flags don't need a value;
			// non-bool flags accept their stringified DefValue.
			arg := "--" + tg.flag.Name
			if tg.flag.Value.Type() != "bool" {
				arg = arg + "=" + tg.flag.DefValue
			}

			// ParseFlags triggers cobra's deprecation flush via c.Print().
			// Ignore the error — required-positional-arg failures still let
			// the deprecation message flush first.
			_ = tg.cmd.ParseFlags([]string{arg})

			assert.NotContains(t, stdout.String(), "deprecated", "deprecation warning leaked to stdout for %s", name)
		})
	}
}

// AppendAnnotationSections replaces the help func of every direct child of
// root, so one set there before main.go calls it would be lost silently.
func TestTopLevelCommandsInheritRootHelpFunc(t *testing.T) {
	ios, _, _, _ := cmdtest.TestIOStreams()
	rootCmd := NewCmdRoot(cmdtest.NewTestFactory(ios))
	rootHelp := reflect.ValueOf(rootCmd.HelpFunc()).Pointer()

	for _, c := range rootCmd.Commands() {
		assert.Equal(t, rootHelp, reflect.ValueOf(c.HelpFunc()).Pointer(),
			"%s sets its own help func, which help.AppendAnnotationSections would replace. Set it on a subcommand instead, or make AppendAnnotationSections keep it.", c.CommandPath())
	}
}

// A command that sets its own help func must chain to its parent's, or its
// help loses the sections that AppendAnnotationSections adds.
func TestAnnotatedCommandHelpShowsAnnotationSections(t *testing.T) {
	ios, _, stdout, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
	rootCmd := NewCmdRoot(cmdtest.NewTestFactory(ios))
	rootCmd.SetHelpFunc(func(*cobra.Command, []string) {})
	rootCmd.SetOut(stdout)
	help.AppendAnnotationSections(rootCmd, ios)

	var annotated []*cobra.Command
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if len(help.AnnotationSections(sub)) > 0 {
				annotated = append(annotated, sub)
			}
			walk(sub)
		}
	}
	walk(rootCmd)
	require.NotEmpty(t, annotated, "expected annotated commands in the command tree — did the walk break?")

	for _, c := range annotated {
		t.Run(c.CommandPath(), func(t *testing.T) {
			stdout.Reset()
			c.HelpFunc()(c, nil)
			for _, s := range help.AnnotationSections(c) {
				assert.Contains(t, stdout.String(), strings.ToUpper(s.Title))
			}
		})
	}
}
