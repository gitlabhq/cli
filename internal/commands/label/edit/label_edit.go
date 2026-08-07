package edit

import (
	"fmt"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

func NewCmdEdit(f cmdutils.Factory) *cobra.Command {
	var labelID int

	LabelUpdateCmd := &cobra.Command{
		Use:   "edit [flags]",
		Short: `Edit a label in a project.`,
		Long: heredoc.Docf(`
			Edit an existing label in a project. The %[1]s--label-id%[1]s flag is required
			to identify the label to update. At least one of %[1]s--new-name%[1]s or
			%[1]s--color%[1]s must be provided; %[1]s--description%[1]s and %[1]s--priority%[1]s are optional.

			By default, the label is edited in the current repository. Use
			%[1]s--repo%[1]s to target another project.
		`, "`"),
		Example: heredoc.Doc(`
			# Rename a label in the current repository
			glab label edit --label-id 1234 --new-name critical

			# Change a label's color and description in another project
			glab label edit --label-id 1234 --color "#FF0000" --description "Top priority" -R owner/repo
		`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error
			var change string

			client, err := f.GitLabClient()
			if err != nil {
				return err
			}

			repo, err := f.BaseRepo()
			if err != nil {
				return err
			}

			l := &gitlab.UpdateLabelOptions{}

			if s, _ := cmd.Flags().GetString("new-name"); s != "" {
				l.Name = new(s)
				change += fmt.Sprintf("Updated name: %s\n", s)
			}
			if s, _ := cmd.Flags().GetString("color"); s != "" {
				l.Color = new(s)
				change += fmt.Sprintf("Updated color: %s\n", s)
			}
			if s, _ := cmd.Flags().GetString("description"); s != "" {
				l.Description = new(s)
				change += fmt.Sprintf("Updated description: %s\n", s)
			}
			if cmd.Flags().Changed("priority") {
				if s, err := cmd.Flags().GetInt("priority"); err == nil {
					l.Priority = gitlab.NewNullableWithValue(int64(s))
					change += fmt.Sprintf("Updated priority: %d\n", s)
				} else {
					return err
				}
			}

			label, _, err := client.Labels.UpdateLabel(repo.FullName(), labelID, l)
			if err != nil {
				return err
			}

			f.IO().LogInfof("Updating \"%s\" label\n%s", label.Name, change)

			return nil
		},
	}

	LabelUpdateCmd.Flags().IntVarP(&labelID, "label-id", "l", 0, "The label ID we are updating.")
	_ = LabelUpdateCmd.MarkFlagRequired("label-id")

	LabelUpdateCmd.Flags().StringP("new-name", "n", "", "The new name of the label.")
	LabelUpdateCmd.Flags().StringP("color", "c", "", "The color of the label given in 6-digit hex notation with leading ‘#’ sign.")
	LabelUpdateCmd.MarkFlagsOneRequired("new-name", "color")
	LabelUpdateCmd.Flags().StringP("description", "d", "", "Label description.")
	LabelUpdateCmd.Flags().IntP("priority", "p", 0, "Label priority.")

	return LabelUpdateCmd
}
