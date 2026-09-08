package lint

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	io           *iostreams.IOStreams
	gitlabClient func() (*gitlab.Client, error)
	baseRepo     func() (glrepo.Interface, error)

	path        string
	ref         string
	dryRun      bool
	includeJobs bool
}

func NewCmdLint(f cmdutils.Factory) *cobra.Command {
	opts := options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
		baseRepo:     f.BaseRepo,
	}
	pipelineCILintCmd := &cobra.Command{
		Use:   "lint",
		Short: "Check if your `.gitlab-ci.yml` file is valid.",
		Long: heredoc.Docf(`
			Defaults to the %[1]s.gitlab-ci.yml%[1]s file in the current directory.
			You can also pass a URL to validate a remote file. Use %[1]s--dry-run%[1]s
			to simulate pipeline creation, and %[1]s--ref%[1]s to set the branch or
			tag context for the simulation.
		`, "`"),
		Args: cobra.MaximumNArgs(1),
		Example: heredoc.Doc(`
			# Uses .gitlab-ci.yml in the current directory
			glab ci lint

			# Lint a specific file
			glab ci lint .gitlab-ci.yml
			glab ci lint path/to/.gitlab-ci.yml`),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(args)
			return opts.run(cmd.Context())
		},
	}

	pipelineCILintCmd.Flags().BoolVarP(&opts.dryRun, "dry-run", "", false, "Run pipeline creation simulation.")
	pipelineCILintCmd.Flags().BoolVarP(&opts.includeJobs, "include-jobs", "", false, "Include the list of jobs that would exist in a static check or pipeline simulation.")
	pipelineCILintCmd.Flags().StringVar(&opts.ref, "ref", "", "When '--dry-run' is true, sets the branch or tag context for validating the CI/CD YAML configuration.")

	return pipelineCILintCmd
}

func (o *options) complete(args []string) {
	if len(args) == 1 {
		o.path = args[0]
	} else {
		o.path = ".gitlab-ci.yml"
	}
}

func (o *options) run(ctx context.Context) error {
	var err error
	c := o.io.Color()

	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	repo, err := o.baseRepo()
	if err != nil {
		return fmt.Errorf("you must be in a GitLab project repository for this action: %w", err)
	}

	project, err := repo.Project(client)
	if err != nil {
		return fmt.Errorf("you must be in a GitLab project repository for this action: %w", err)
	}

	projectID := project.ID

	var content []byte
	var stdout bytes.Buffer

	if git.IsValidURL(o.path) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.path, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return fmt.Errorf("fetching %s: %s", o.path, resp.Status)
		}
		_, err = io.Copy(&stdout, resp.Body)
		if err != nil {
			return err
		}
		content = stdout.Bytes()
	} else {
		content, err = os.ReadFile(o.path)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("%s: no such file or directory", o.path)
			}
			return err
		}
	}

	o.io.LogInfo("Validating...")

	lintOpts := &gitlab.ProjectNamespaceLintOptions{
		Content:     new(string(content)),
		DryRun:      new(o.dryRun),
		IncludeJobs: new(o.includeJobs),
	}
	// Only include Ref if it was explicitly set by the user
	if o.ref != "" {
		lintOpts.Ref = new(o.ref)
	}

	lint, _, err := client.Validate.ProjectNamespaceLint(
		projectID,
		lintOpts,
	)
	if err != nil {
		return err
	}

	if !lint.Valid {
		o.io.LogInfo(c.Red(o.path + " is invalid."))
		for i, err := range lint.Errors {
			i++
			o.io.LogInfo(i, err)
		}
		return cmdutils.SilentError
	}
	o.io.LogInfo(c.GreenCheck(), "CI/CD YAML is valid!")
	return nil
}
