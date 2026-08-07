package create

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

func NewCmdCreate(f cmdutils.Factory) *cobra.Command {
	labelCreateCmd := &cobra.Command{
		Use:   "create [flags]",
		Short: `Create a label in a project.`,
		Long: heredoc.Docf(`
			Use the flags to set the label name, color, description, and priority.
			The %[1]s--name%[1]s flag is required; %[1]s--color%[1]s defaults to
			%[1]s#428BCA%[1]s if not specified.

			By default, the label is created in the current repository. Use
			%[1]s--repo%[1]s to target another project.
		`, "`"),
		Aliases: []string{"new"},
		Example: heredoc.Doc(`
			# Create a label in the current repository
			glab label create --name bug --color "#FF0000" --description "Something is broken"

			# Create a label in another project
			glab label create --name bug -R owner/repo
		`),
		Args: cobra.NoArgs,
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

			l := &gitlab.CreateLabelOptions{}

			if s, _ := cmd.Flags().GetString("name"); s != "" {
				l.Name = new(s)
			}

			if s, _ := cmd.Flags().GetString("color"); s != "" {
				l.Color = new(s)
			}
			if s, _ := cmd.Flags().GetString("description"); s != "" {
				l.Description = new(s)
			}
			if cmd.Flags().Changed("priority") {
				if s, err := cmd.Flags().GetInt("priority"); err == nil {
					l.Priority = gitlab.NewNullableWithValue(int64(s))
				} else {
					return err
				}
			}
			label, _, err := client.Labels.CreateLabel(repo.FullName(), l)
			if err != nil {
				return err
			}

			f.IO().LogInfof("Created label: %s\nWith color: %s\n", label.Name, label.Color)

			return nil
		},
	}
	labelCreateCmd.Flags().StringP("name", "n", "", "Name of the label.")
	_ = labelCreateCmd.MarkFlagRequired("name")
	labelCreateCmd.Flags().StringP("color", "c", "#428BCA", "Color of the label, in plain or HEX code.")
	labelCreateCmd.Flags().StringP("description", "d", "", "Label description.")
	labelCreateCmd.Flags().IntP("priority", "p", 0, "Label priority.")

	return labelCreateCmd
}
