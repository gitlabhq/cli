package list

import (
	"fmt"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

func NewCmdList(f cmdutils.Factory) *cobra.Command {
	securefileListCmd := &cobra.Command{
		Use:   "list [flags]",
		Short: `List secure files in a project.`,
		Long: heredoc.Docf(`
		List the secure files configured for a project. Use %[1]s--page%[1]s and
		%[1]s--per-page%[1]s to paginate the result.

		By default, files are listed for the current project. Use %[1]s--repo%[1]s
		to target another project.
		`, "`"),
		Aliases: []string{"ls"},
		Example: heredoc.Doc(`
			# List all secure files in the current project
			glab securefile list

			# Use the 'ls' alias
			glab securefile ls

			# List a specific page
			glab securefile list --page 2

			# List a specific page with a custom page size
			glab securefile list --page 2 --per-page 10

			# List files from another project
			glab securefile list -R owner/repo
		`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GitLabClient()
			if err != nil {
				return err
			}

			repo, err := f.BaseRepo()
			if err != nil {
				return err
			}

			l := &gitlab.ListProjectSecureFilesOptions{
				ListOptions: gitlab.ListOptions{
					Page:    1,
					PerPage: api.DefaultListLimit,
				},
			}

			if p, _ := cmd.Flags().GetInt("page"); p != 0 {
				l.Page = int64(p)
			}

			if p, _ := cmd.Flags().GetInt("per-page"); p != 0 {
				l.PerPage = int64(p)
			}

			files, _, err := client.SecureFiles.ListProjectSecureFiles(repo.FullName(), l)
			if err != nil {
				return fmt.Errorf("error listing secure files: %w", err)
			}

			return f.IO().PrintJSON(files)
		},
	}

	securefileListCmd.Flags().IntP("page", "p", 1, "Page number.")
	securefileListCmd.Flags().IntP("per-page", "P", 30, "Number of items to list per page.")

	cmdutils.AddJQFlag(securefileListCmd, f.IO())
	return securefileListCmd
}
