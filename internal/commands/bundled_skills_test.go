//go:build !integration

package commands

import (
	"path"
	"regexp"
	"strings"
	"testing"

	"github.com/google/shlex"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/skills/bundled"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// Checks that every glab command and flag in the bundled SKILL.md files
// exists. Agents copy these commands exactly, so a renamed command or flag
// leaves them with instructions that fail.
//
// It lives in this package rather than in bundled because it needs the full
// command tree. Being here also means make test-changed runs it whenever a
// command changes.
func TestBundledSkillsUseRealCommandsAndFlags(t *testing.T) {
	ios, _, _, _ := cmdtest.TestIOStreams()
	root := NewCmdRoot(cmdutils.NewFactory(ios, false, config.NewBlankConfig(), api.BuildInfo{Version: "v1.0.0", Commit: "abcdefgh"}))

	skills, err := bundled.All()
	require.NoError(t, err)
	require.NotEmpty(t, skills)

	checked := 0
	for _, s := range skills {
		for name, contents := range s.Files {
			if path.Ext(name) != ".md" {
				continue
			}
			for _, inv := range glabInvocations(string(contents)) {
				checked++
				for _, problem := range checkInvocation(root, inv) {
					t.Errorf("%s/%s: %s:\n\t%s", s.Name, name, problem, inv)
				}
			}
		}
	}
	require.NotZero(t, checked, "no glab invocations found in bundled skills")
}

var codeSpanRE = regexp.MustCompile("`(glab(?:\\s[^`]*)?)`")

// glabInvocations returns the glab commands in fenced blocks and inline code
// spans. Fenced lines ending in a backslash are joined with the next line.
func glabInvocations(md string) []string {
	var out, prose []string
	inFence := false
	pending := ""
	for line := range strings.SplitSeq(md, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			pending = ""
			continue
		}
		if !inFence {
			prose = append(prose, line)
			continue
		}
		if pending != "" {
			trimmed = pending + " " + trimmed
			pending = ""
		}
		if !strings.HasPrefix(trimmed, "glab ") {
			continue
		}
		if cont, ok := strings.CutSuffix(trimmed, "\\"); ok {
			pending = strings.TrimSpace(cont)
			continue
		}
		out = append(out, trimmed)
	}
	for _, m := range codeSpanRE.FindAllStringSubmatch(strings.Join(prose, "\n"), -1) {
		out = append(out, strings.Join(strings.Fields(m[1]), " "))
	}
	return out
}

func checkInvocation(root *cobra.Command, inv string) []string {
	words, err := shlex.Split(inv)
	if err != nil {
		return []string{"cannot parse: " + err.Error()}
	}
	words = words[1:]
	for i, w := range words {
		if isShellOperator(w) {
			words = words[:i]
			break
		}
	}

	// Skills write arguments as placeholders like <iid> or <branch>...,
	// which root.Find would reject as unknown commands.
	pathEnd := len(words)
	for i, w := range words {
		if strings.HasPrefix(w, "-") || strings.HasPrefix(w, "<") {
			pathEnd = i
			break
		}
	}
	cmd, args, err := root.Find(words[:pathEnd])
	if err != nil {
		return []string{err.Error()}
	}
	if len(args) > 0 && cmd.HasSubCommands() {
		return []string{"unknown subcommand " + args[0] + " for " + cmd.CommandPath()}
	}
	cmd.InitDefaultHelpFlag()

	var problems []string
	for _, w := range words[pathEnd:] {
		if len(w) < 2 || w[0] != '-' || (w[1] >= '0' && w[1] <= '9') {
			continue
		}
		if long, ok := strings.CutPrefix(w, "--"); ok {
			long, _, _ = strings.Cut(long, "=")
			if cmd.Flag(long) == nil {
				problems = append(problems, "unknown flag --"+long+" for "+cmd.CommandPath())
			}
			continue
		}
		short, _, _ := strings.Cut(w[1:], "=")
		for _, r := range short {
			if !hasShorthand(cmd, string(r)) {
				problems = append(problems, "unknown flag -"+string(r)+" for "+cmd.CommandPath())
			}
		}
	}
	return problems
}

// isShellOperator reports whether w ends the glab part of a command line.
// A placeholder like <iid> also starts with "<", so an input redirect is only
// "<", a heredoc "<<...", or "<file" with no closing ">".
func isShellOperator(w string) bool {
	switch {
	case w == "|", w == ";", w == "&&":
		return true
	case strings.HasPrefix(w, ">"), strings.HasPrefix(w, "#"), strings.HasPrefix(w, "<<"):
		return true
	case strings.HasPrefix(w, "<"):
		return !strings.Contains(w, ">")
	}
	return false
}

func hasShorthand(cmd *cobra.Command, s string) bool {
	return cmd.Flags().ShorthandLookup(s) != nil || cmd.InheritedFlags().ShorthandLookup(s) != nil
}

func TestGlabInvocations(t *testing.T) {
	t.Parallel()

	md := strings.Join([]string{
		"Run `glab mr view <iid>` first.",
		"Prose mentioning glab outside a span is ignored.",
		"```shell",
		"# a comment",
		"glab api projects/:id \\",
		"  -X POST",
		"cat file | glab ignored",
		"```",
		"Wrapped `glab issue",
		"note` span.",
	}, "\n")

	assert.Equal(t, []string{
		"glab api projects/:id -X POST",
		"glab mr view <iid>",
		"glab issue note",
	}, glabInvocations(md))
}

// newTestRoot builds a fresh tree per call: checkInvocation lazily fills
// cobra's flag sets, so parallel subtests can't share one.
func newTestRoot() *cobra.Command {
	root := &cobra.Command{Use: "glab"}
	root.PersistentFlags().StringP("repo", "R", "", "")
	mr := &cobra.Command{Use: "mr", Run: func(*cobra.Command, []string) {}}
	view := &cobra.Command{Use: "view", Run: func(*cobra.Command, []string) {}}
	view.Flags().StringP("message", "m", "", "")
	view.Flags().String("reply", "", "")
	view.Flags().BoolP("all", "a", false, "")
	mr.AddCommand(view)
	root.AddCommand(mr)
	return root
}

func TestCheckInvocation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		inv  string
		want []string
	}{
		{"valid", `glab mr view <iid> --reply <id> -m "hi"`, nil},
		{"inherited and --flag=value", "glab mr view -R g/p --reply=1", nil},
		{"combined shorthands", "glab mr view -am x", nil},
		{"stdin dash and negative number skipped", "glab mr view - -1", nil},
		{"help flag", "glab mr view --help", nil},
		{"unknown flag after placeholder", "glab mr view <iid> --bogus", []string{"unknown flag --bogus for glab mr view"}},
		{"unknown shorthand", "glab mr view -q", []string{"unknown flag -q for glab mr view"}},
		{"unknown subcommand under runnable parent", "glab mr veiw", []string{"unknown subcommand veiw for glab mr"}},
		{"stops at pipe", "glab mr view | jq --bogus", nil},
		{"stops at heredoc", "glab mr view <<'EOF' --bogus", nil},
		{"stops at input redirect", "glab mr view < /tmp/x --bogus", nil},
		{"stops at comment", "glab mr view # --bogus", nil},
		{"unclosed quote", `glab mr view -m "x`, []string{"cannot parse: EOF found when expecting closing quote"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, checkInvocation(newTestRoot(), tc.inv))
		})
	}
}
