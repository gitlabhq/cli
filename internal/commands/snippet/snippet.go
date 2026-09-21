package snippet

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/snippet/create"
)

func NewCmdSnippet(f cmdutils.Factory) *cobra.Command {
	snippetCmd := &cobra.Command{
		Use:   "snippet <command> [flags]",
		Short: `Create snippets.`,
		Long: heredoc.Docf(`
			Snippets store and share small pieces of code or text. A snippet can
			belong to a project, or to your personal account when you pass
			%[1]s--personal%[1]s.

			To view and edit existing snippets, use the GitLab UI or %[1]sglab api%[1]s with the [Project snippets API](https://docs.gitlab.com/api/project_snippets/) or personal [Snippets API](https://docs.gitlab.com/api/snippets/).
		`, "`"),
		Example: heredoc.Doc(`
			glab snippet create --title "Title of the snippet" --filename "main.go"`),
		Annotations: map[string]string{
			"help:arguments": heredoc.Doc(`
			A snippet can be supplied as argument in the following format:
			- by number, e.g. "123"
			`),
		},
	}

	cmdutils.EnableRepoOverride(snippetCmd, f)

	snippetCmd.AddCommand(create.NewCmdCreate(f))
	return snippetCmd
}
