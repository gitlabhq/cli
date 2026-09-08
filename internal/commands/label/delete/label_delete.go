package delete

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

func NewCmdDelete(f cmdutils.Factory) *cobra.Command {
	labelDeleteCmd := &cobra.Command{
		Use:   "delete <name> [flags]",
		Short: `Delete a label from a project.`,
		Long: heredoc.Docf(`
			Delete a label from a project by name. The label is removed from
			the project; it is not removed from issues, merge requests, or
			epics that already use it.

			By default, the label is deleted from the current repository. Use
			%[1]s--repo%[1]s to target another project.
		`, "`"),
		Example: heredoc.Doc(`
			# Delete a label from the current repository
			glab label delete bug

			# Delete a label from another project
			glab label delete bug -R owner/repo
		`),
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error

			client, err := f.GitLabClient()
			if err != nil {
				return err
			}

			repo, err := f.BaseRepo()
			if err != nil {
				return err
			}

			o := &gitlab.DeleteLabelOptions{}

			_, err = client.Labels.DeleteLabel(repo.FullName(), args[0], o)
			if err != nil {
				return err
			}
			f.IO().LogInfof("Label deleted")

			return nil
		},
	}

	return labelDeleteCmd
}
