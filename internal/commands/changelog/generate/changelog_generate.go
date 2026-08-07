package generate

import (
	"errors"
	"fmt"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

func NewCmdGenerate(f cmdutils.Factory) *cobra.Command {
	changelogGenerateCmd := &cobra.Command{
		Use:   "generate [flags]",
		Short: `Generate a changelog for the current project.`,
		Long: heredoc.Docf(`
		Generates a changelog from the commits in your project's Git
		repository. If you do not pass %[1]s--version%[1]s, glab determines
		the version by running %[1]sgit describe%[1]s against your local tags.
		
		By default, GitLab reads the changelog configuration from
		%[1]s.gitlab/changelog_config.yml%[1]s in the project. To use a
		different file, pass %[1]s--config-file%[1]s.
		
		To limit the range of commits, use %[1]s--from%[1]s and %[1]s--to%[1]s.
		glab excludes the %[1]s--from%[1]s commit from the range and includes
		the %[1]s--to%[1]s commit. The %[1]s--to%[1]s commit defaults to
		%[1]sHEAD%[1]s of the project's default branch.
		`, "`"),
		Example: heredoc.Doc(`
		# Generate a changelog for the version detected by 'git describe'
		glab changelog generate

		# Generate a changelog for a specific version
		glab changelog generate --version 1.2.0

		# Generate a changelog for commits between two SHAs
		glab changelog generate --from abc123 --to def456
		`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
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

			opts := gitlab.GenerateChangelogDataOptions{}

			// Set the version
			if s, _ := cmd.Flags().GetString("version"); s != "" {
				opts.Version = new(s)
			} else {
				tags, err := git.ListTags()
				if err != nil {
					return err
				}

				if len(tags) == 0 {
					return errors.New("no tags found; either fetch tags, or pass a version with --version instead")
				}

				version, err := git.DescribeByTags()
				if err != nil {
					return fmt.Errorf("failed to determine version from `git describe`: %w", err)
				}
				opts.Version = new(version)
			}

			// Set the config file
			if s, _ := cmd.Flags().GetString("config-file"); s != "" {
				opts.ConfigFile = new(s)
			}

			// Set the date
			if s, _ := cmd.Flags().GetString("date"); s != "" {
				parsedDate, err := time.Parse(time.RFC3339, s)
				if err != nil {
					return err
				}

				t := gitlab.ISOTime(parsedDate)
				opts.Date = new(t)
			}

			// Set the "from" attribute
			if s, _ := cmd.Flags().GetString("from"); s != "" {
				opts.From = new(s)
			}

			// Set the "to" attribute
			if s, _ := cmd.Flags().GetString("to"); s != "" {
				opts.To = new(s)
			}

			// Set the trailer
			if s, _ := cmd.Flags().GetString("trailer"); s != "" {
				opts.Trailer = new(s)
			}

			changelog, _, err := client.Repositories.GenerateChangelogData(repo.FullName(), opts)
			if err != nil {
				return err
			}

			f.IO().LogInfof("%s", changelog.Notes)

			return nil
		},
	}

	// The options mimic the ones from the REST API.
	// https://docs.gitlab.com/api/repositories/#generate-changelog-data
	changelogGenerateCmd.Flags().StringP("version", "v", "", "Version to generate the changelog for. Must follow semantic versioning. Defaults to the version detected by 'git describe'.")
	changelogGenerateCmd.Flags().StringP("config-file", "", "", "Path to the changelog configuration file in the project's Git repository. Defaults to '.gitlab/changelog_config.yml'.")
	changelogGenerateCmd.Flags().StringP("date", "", "", "Date and time of the release, in ISO 8601 format (2016-03-11T03:45:40Z). Defaults to the current time.")
	changelogGenerateCmd.Flags().StringP("from", "", "", "Start of the range of commits to use when generating the changelog, as a SHA. This commit is not included in the range.")
	changelogGenerateCmd.Flags().StringP("to", "", "", "End of the range of commits to use when generating the changelog, as a SHA. This commit is included in the range. Defaults to the HEAD of the project's default branch.")
	changelogGenerateCmd.Flags().StringP("trailer", "", "", "The Git trailer to use to include commits. Defaults to 'Changelog'.")

	return changelogGenerateCmd
}
