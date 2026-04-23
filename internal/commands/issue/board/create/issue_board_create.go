package create

import (
	"fmt"

	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

var createIssueBoard = func(client *gitlab.Client, projectID any, opts *gitlab.CreateIssueBoardOptions) (*gitlab.IssueBoard, error) {
	board, _, err := client.Boards.CreateIssueBoard(projectID, opts)
	if err != nil {
		return nil, err
	}

	return board, nil
}

func NewCmdCreate(f cmdutils.Factory) *cobra.Command {
	var boardName string
	issueCmd := &cobra.Command{
		Use:     "create [flags]",
		Short:   `Create a project issue board.`,
		Long:    ``,
		Aliases: []string{"new"},
		Args:    cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				boardName = args[0]
			}
			var err error
			out := f.IO().StdOut
			c := f.IO().Color()

			client, err := f.GitLabClient()
			if err != nil {
				return err
			}

			repo, err := f.BaseRepo()
			if err != nil {
				return err
			}

			if boardName == "" {
				err = f.IO().Input(cmd.Context(), &boardName, "Board Name:", "", func(s string) error {
					if s == "" {
						return fmt.Errorf("board name is required")
					}
					return nil
				})
				if err != nil {
					return err
				}
			}

			opts := &gitlab.CreateIssueBoardOptions{
				Name: new(boardName),
			}

			fmt.Fprintln(out, "- Creating board")

			issueBoard, err := createIssueBoard(client, repo.FullName(), opts)
			if err != nil {
				return err
			}

			fmt.Fprintf(out, "%s Board created: %q", c.GreenCheck(), issueBoard.Name)

			return nil
		},
	}

	issueCmd.Flags().StringVarP(&boardName, "name", "n", "", "The name of the new board.")

	return issueCmd
}
