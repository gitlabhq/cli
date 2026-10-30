package confighelp

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/config"
)

// Settings renders the documented config keys as a Markdown bullet list for
// the `glab config` synopsis.
func Settings() string {
	var lines []string
	for _, kd := range config.KeySchema {
		if !kd.UserSettable || kd.HelpHidden {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %#q: %s", kd.Name, describe(kd)))
	}
	slices.Sort(lines)
	return strings.Join(lines, "\n")
}

// KeyCompletions provides shell completions for config keys.
func KeyCompletions(setting bool) []cobra.Completion {
	completions := make([]cobra.Completion, 0, len(config.KeySchema)*2)
	for _, kd := range config.KeySchema {
		if kd.UserSettable && !kd.HelpHidden {
			completions = append(completions, fmt.Sprintf("%s\t%s", kd.Name, flatten(kd.Description)))
		}
		if !setting {
			for _, alias := range kd.Aliases {
				completions = append(completions, fmt.Sprintf("%s\tAlias for '%s'.", alias, kd.Name))
			}
		}
	}
	return completions
}

// ValueCompletions provides shell completions for config values.
func ValueCompletions(key string) ([]cobra.Completion, cobra.ShellCompDirective) {
	key = config.ConfigKeyEquivalence(key) // aliases like prompt_disabled complete too
	for _, kd := range config.KeySchema {
		if kd.Name != key || !kd.UserSettable {
			continue
		}
		if kd.Type == config.TypeBool {
			return []cobra.Completion{"true", "false"}, cobra.ShellCompDirectiveNoFileComp
		}
		switch kd.Name {
		case "ca_cert", "client_cert":
			return []string{"pem", "crt", "cer", "cert"}, cobra.ShellCompDirectiveFilterFileExt
		case "client_key":
			return []cobra.Completion{"pem", "key"}, cobra.ShellCompDirectiveFilterFileExt
		case "glamour_style":
			return kd.Values, cobra.ShellCompDirectiveDefault
		}
		return kd.Values, cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// quotedLiteral matches a 'single-quoted' span, the convention KeyDef
// descriptions use because they are also rendered as YAML comments. The
// leading and trailing delimiters keep possessive apostrophes out.
var quotedLiteral = regexp.MustCompile(`(^|[\s(])'([^']+?)'([\s.,;:)]|$)`)

func backtickLiterals(s string) string {
	return quotedLiteral.ReplaceAllString(s, "${1}`${2}`${3}")
}

func describe(kd config.KeyDef) string {
	desc := tidy(kd.Description)
	if len(kd.Aliases) > 0 {
		quoted := make([]string, 0, len(kd.Aliases))
		for _, a := range kd.Aliases {
			quoted = append(quoted, "`"+a+"`")
		}
		desc += " Also accepted as: " + strings.Join(quoted, ", ") + "."
	}
	if kd.Scope == config.ScopePerHost {
		desc += " Scoped per host; set it with `--host`."
	}
	if envVars := config.EnvVarsForKey(kd); len(envVars) > 0 {
		desc += " " + envVarSentence(envVars)
	}
	return desc
}

// tidy renders a schema description as a single Markdown sentence.
func tidy(desc string) string {
	out := backtickLiterals(flatten(desc))
	if !strings.HasSuffix(out, ".") {
		out += "."
	}
	return out
}

// EnvVarsByGroup returns the environment variable reference, grouped, with
// each description flattened onto one line and its quoted literals marked up
// as code.
func EnvVarsByGroup() []config.EnvVarSection {
	out := slices.Clone(config.EnvVarsByGroup())
	for i := range out {
		out[i].Vars = slices.Clone(out[i].Vars)
		for j := range out[i].Vars {
			out[i].Vars[j].Description = tidy(out[i].Vars[j].Description)
		}
	}
	return out
}

// EnvironmentHelp renders the environment variable reference as the plain
// "NAME: description" blocks the help:environment annotation uses.
func EnvironmentHelp() string {
	var b strings.Builder
	for i, ev := range config.EnvVars() {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "%s: %s", ev.Name, flatten(ev.Description))
	}
	return b.String()
}

func envVarSentence(envVars []string) string {
	quoted := make([]string, 0, len(envVars))
	for _, v := range envVars {
		quoted = append(quoted, "`"+v+"`")
	}
	if len(quoted) == 1 {
		return "Environment variable: " + quoted[0] + "."
	}
	return "Environment variables, first one set wins: " + strings.Join(quoted, ", ") + "."
}

// flatten collapses a KeyDef description onto one line, dropping any trailing
// indented example block so it renders as a single bullet.
func flatten(desc string) string {
	var out []string
	for line := range strings.SplitSeq(desc, "\n") {
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "  ") {
			break
		}
		if t := strings.TrimSpace(line); t != "" {
			out = append(out, t)
		}
	}
	return strings.Join(out, " ")
}
