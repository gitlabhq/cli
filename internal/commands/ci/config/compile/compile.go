package compile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

func NewCmdConfigCompile(f cmdutils.Factory) *cobra.Command {
	var jobName string

	configCompileCmd := &cobra.Command{
		Use:   "compile [<path>]",
		Short: "View the merged CI/CD configuration.",
		Long: heredoc.Docf(`
		Compiles your CI/CD configuration and prints the fully merged YAML
		to standard output.

		All %[1]sinclude%[1]s directives are resolved,
		and any extended jobs are flattened into their final form.

		When %[1]s--job%[1]s is specified, glab prints the resolved shell script for that job.

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

			# Extract script for a specific job
			glab ci config compile --job test-job
		`),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("job") && jobName == "" {
				return errors.New("--job cannot be empty")
			}
			path := ".gitlab-ci.yml"
			if len(args) == 1 {
				path = args[0]
			}
			return compileRun(cmd.Context(), f, path, jobName)
		},
	}

	configCompileCmd.Flags().StringVar(&jobName, "job", "", "Job name to extract script for.")

	configCompileCmd.SetHelpFunc(func(command *cobra.Command, strings []string) {
		// Hide "repo"-flag for this command, because it cannot be used on repositories but only on gitlab-ci files
		_ = configCompileCmd.Flags().MarkHidden("repo")

		configCompileCmd.Parent().HelpFunc()(command, strings)
	})

	return configCompileCmd
}

func compileRun(ctx context.Context, f cmdutils.Factory, path, jobName string) error {
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

	opts := &gitlab.ProjectNamespaceLintOptions{
		Content:     new(string(content)),
		IncludeJobs: new(jobName != ""),
	}

	result, _, err := client.Validate.ProjectNamespaceLint(
		project.ID,
		opts,
		api.WithSafeRetries(),
		gitlab.WithContext(ctx),
	)
	if err != nil {
		return err
	}

	if !result.Valid {
		errorsStr := strings.Join(result.Errors, ", ")
		return fmt.Errorf("could not compile %s: %s", path, errorsStr)
	}

	if jobName != "" {
		return renderJobScript(f.IO(), result.Jobs, jobName)
	}

	f.IO().LogInfof("%s", result.MergedYaml)

	return nil
}

func renderJobScript(io *iostreams.IOStreams, jobs []gitlab.ProjectLintJob, jobName string) error {
	available := make([]string, 0, len(jobs))
	var found *gitlab.ProjectLintJob
	for i := range jobs {
		if jobs[i].Name == jobName {
			found = &jobs[i]
			break
		}
		available = append(available, jobs[i].Name)
	}
	if found == nil {
		return fmt.Errorf("job %q not found in CI/CD configuration. Available jobs: %s", jobName, strings.Join(available, ", "))
	}

	hasMainScript := len(found.BeforeScript) > 0 || len(found.Script) > 0
	hasAfterScript := len(found.AfterScript) > 0

	if !hasMainScript && !hasAfterScript {
		return fmt.Errorf("job %q has no script to print (is it a trigger job?)", found.Name)
	}

	for _, line := range found.BeforeScript {
		io.LogInfo(line)
	}
	for _, line := range found.Script {
		io.LogInfo(line)
	}

	if hasAfterScript {
		if hasMainScript {
			io.LogInfo()
		}
		io.LogInfo("# after_script")
		for _, line := range found.AfterScript {
			io.LogInfo(line)
		}
	}

	return nil
}
