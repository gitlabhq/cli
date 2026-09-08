package compile

import (
	"fmt"
	"os"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

func NewCmdConfigCompile(f cmdutils.Factory) *cobra.Command {
	configCompileCmd := &cobra.Command{
		Use:   "compile [<path>]",
		Short: "View the merged CI/CD configuration.",
		Long: heredoc.Docf(`
		Compiles your CI/CD configuration and prints the fully merged YAML
		to standard output.

		All %[1]sinclude%[1]s directives are resolved,
		and any extended jobs are flattened into their final form.

		By default, glab compiles the %[1]s.gitlab-ci.yml%[1]s file in the
		current directory. To compile a different file, pass its path as an
		argument.

		You must run this command from a GitLab project repository.
		`, "`"),
		Args: cobra.MaximumNArgs(1),
		Example: heredoc.Doc(`
			# Compile .gitlab-ci.yml in the current directory
			glab ci config compile

			# Compile a specific file in the current directory
			glab ci config compile .gitlab-ci.yml

			# Compile a file at a relative path
			glab ci config compile path/to/.gitlab-ci.yml
		`),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			path := ".gitlab-ci.yml"
			if len(args) == 1 {
				path = args[0]
			}
			return compileRun(f, path)
		},
	}

	configCompileCmd.SetHelpFunc(func(command *cobra.Command, strings []string) {
		// Hide "repo"-flag for this command, because it cannot be used on repositories but only on gitlab-ci files
		_ = configCompileCmd.Flags().MarkHidden("repo")

		configCompileCmd.Parent().HelpFunc()(command, strings)
	})

	return configCompileCmd
}

func compileRun(f cmdutils.Factory, path string) error {
	var err error

	client, err := f.GitLabClient()
	if err != nil {
		return err
	}

	repo, err := f.BaseRepo()
	if err != nil {
		return fmt.Errorf("you must be in a GitLab project repository for this action: %w", err)
	}

	project, err := repo.Project(client)
	if err != nil {
		return fmt.Errorf("you must be in a GitLab project repository for this action: %w", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading CI/CD configuration at %s: %w", path, err)
	}

	compiledResult, _, err := client.Validate.ProjectNamespaceLint(
		project.ID,
		&gitlab.ProjectNamespaceLintOptions{
			Content:     new(string(content)),
			DryRun:      new(bool),
			Ref:         new(string),
			IncludeJobs: new(bool),
		},
	)
	if err != nil {
		return err
	}

	if !compiledResult.Valid {
		errorsStr := strings.Join(compiledResult.Errors, ", ")
		return fmt.Errorf("could not compile %s: %s", path, errorsStr)
	}

	fmt.Print(compiledResult.MergedYaml)

	return nil
}
