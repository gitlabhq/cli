//go:build !integration

package confighelp

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

func completionNames(completions []cobra.Completion) []string {
	names := make([]string, 0, len(completions))
	for _, c := range completions {
		name, _, _ := strings.Cut(c, "\t")
		names = append(names, name)
	}
	return names
}

func TestKeyCompletions(t *testing.T) {
	t.Parallel()

	for _, setting := range []bool{true, false} {
		names := completionNames(KeyCompletions(setting))
		assert.Contains(t, names, "git_protocol")
		assert.NotContains(t, names, "last_seen_version", "HelpHidden keys are CLI-managed")
		assert.NotContains(t, names, "oauth2_refresh_token", "non-settable keys are CLI-managed")
	}

	assert.Contains(t, completionNames(KeyCompletions(false)), "gitlab_token")
	assert.NotContains(t, completionNames(KeyCompletions(true)), "gitlab_token")
}

func TestValueCompletions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		key       string
		want      []cobra.Completion
		directive cobra.ShellCompDirective
	}{
		{"debug", []cobra.Completion{"true", "false"}, cobra.ShellCompDirectiveNoFileComp},
		{"prompt_disabled", []cobra.Completion{"true", "false"}, cobra.ShellCompDirectiveNoFileComp},
		{"skip_tls_verify", []cobra.Completion{"true", "false"}, cobra.ShellCompDirectiveNoFileComp},
		{"git_protocol", []cobra.Completion{"ssh", "https"}, cobra.ShellCompDirectiveNoFileComp},
		{"color", []cobra.Completion{"auto", "never", "always"}, cobra.ShellCompDirectiveNoFileComp},
		{"duo_cli_auto_run", []cobra.Completion{"true", "false"}, cobra.ShellCompDirectiveNoFileComp},
		{"glamour_style", []cobra.Completion{"auto", "dark", "light", "notty"}, cobra.ShellCompDirectiveDefault},
		{"ca_cert", []cobra.Completion{"pem", "crt", "cer", "cert"}, cobra.ShellCompDirectiveFilterFileExt},
		{"editor", nil, cobra.ShellCompDirectiveNoFileComp},
		{"oauth2_refresh_token", nil, cobra.ShellCompDirectiveNoFileComp},
		{"not_a_key", nil, cobra.ShellCompDirectiveNoFileComp},
	}
	for _, tc := range tests {
		t.Run(tc.key, func(t *testing.T) {
			t.Parallel()

			got, directive := ValueCompletions(tc.key)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.directive, directive)
		})
	}
}
