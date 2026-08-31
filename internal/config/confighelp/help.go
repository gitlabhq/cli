package confighelp

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

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
		lines = append(lines, fmt.Sprintf("- `%s`: %s", kd.Name, describe(kd)))
	}
	slices.Sort(lines)
	return strings.Join(lines, "\n")
}

// quotedLiteral matches a 'single-quoted' span, the convention KeyDef
// descriptions use because they are also rendered as YAML comments. The
// leading and trailing delimiters keep possessive apostrophes out.
var quotedLiteral = regexp.MustCompile(`(^|[\s(])'([^']+?)'([\s.,;:)]|$)`)

func backtickLiterals(s string) string {
	return quotedLiteral.ReplaceAllString(s, "${1}`${2}`${3}")
}

func describe(kd config.KeyDef) string {
	desc := backtickLiterals(flatten(kd.Description))
	if !strings.HasSuffix(desc, ".") {
		desc += "."
	}
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
	return desc
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
