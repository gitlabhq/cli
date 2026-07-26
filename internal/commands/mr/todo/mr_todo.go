package todo

import (
	"errors"
	"net/http"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/mr/mrutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

var errTodoExists = errors.New("to-do already exists")

func NewCmdTodo(f cmdutils.Factory) *cobra.Command {
	mrToDoCmd := &cobra.Command{
		Use:     "todo [<id> | <branch>]",
		Aliases: []string{"add-todo"},
		Short:   "Add a to-do item to a merge request.",
		Long: heredoc.Doc(`
			Adding a to-do item flags the merge request for follow-up in your To-Do List.
		`),
		Example: heredoc.Doc(`
			glab mr todo 123
			glab mr todo branch-name`),
		Args: cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error
			c := f.IO().Color()

			client, err := f.GitLabClient()
			if err != nil {
				return err
			}

			mr, repo, err := mrutils.MRFromArgs(cmd.Context(), f, args, "any")
			if err != nil {
				return err
			}

			_, resp, err := client.MergeRequests.CreateTodo(repo.FullName(), mr.IID)
			if err != nil {
				return err
			}
			if resp != nil && resp.StatusCode == http.StatusNotModified {
				return errTodoExists
			}

			f.IO().LogInfo(c.GreenCheck(), "Done!!")

			return nil
		},
	}

	return mrToDoCmd
}
