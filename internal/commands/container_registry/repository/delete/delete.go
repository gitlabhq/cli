package delete

import (
	"context"
	"fmt"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/container_registry/registryutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	repositoryID int64
	forceDelete  bool

	io           *iostreams.IOStreams
	gitlabClient func() (*gitlab.Client, error)
	baseRepo     func() (glrepo.Interface, error)
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
		baseRepo:     f.BaseRepo,
	}

	cmd := &cobra.Command{
		Use:     "delete <repository-id> [flags]",
		Short:   "Delete a container registry repository.",
		Aliases: []string{"del"},
		Args:    cobra.ExactArgs(1),
		Long: heredoc.Doc(`
			This action permanently deletes the repository and all images and tags
			published to it.
		`),
		Example: heredoc.Doc(`
			# Delete a container registry repository with a confirmation prompt
			glab container-registry repository delete 123

			# Skip the confirmation prompt
			glab container-registry repository delete 123 --yes`),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(args); err != nil {
				return err
			}
			if err := opts.validate(); err != nil {
				return err
			}

			return opts.run(cmd.Context())
		},
	}

	cmd.Flags().BoolVarP(&opts.forceDelete, "yes", "y", false, "Skip the confirmation prompt.")

	return cmd
}

func (o *options) complete(args []string) error {
	repositoryID, err := registryutils.ParseID(args[0], "repository ID")
	if err != nil {
		return &cmdutils.FlagError{Err: err}
	}
	o.repositoryID = repositoryID

	return nil
}

func (o *options) validate() error {
	if !o.forceDelete && !o.io.PromptEnabled() {
		return &cmdutils.FlagError{Err: fmt.Errorf("--yes or -y flag is required when not running interactively")}
	}

	return nil
}

func (o *options) run(ctx context.Context) error {
	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	repo, err := o.baseRepo()
	if err != nil {
		return err
	}

	if !o.forceDelete && o.io.PromptEnabled() {
		o.io.LogErrorf("This action will permanently delete container registry repository %d on %s, including all images and tags.\n\n", o.repositoryID, repo.FullName())
		err = o.io.Confirm(ctx, &o.forceDelete, fmt.Sprintf("Are you ABSOLUTELY SURE you wish to delete container registry repository %d?", o.repositoryID))
		if err != nil {
			return cmdutils.WrapError(err, "could not prompt")
		}
	}

	if !o.forceDelete {
		return cmdutils.CancelError()
	}

	c := o.io.Color()
	o.io.LogInfof("%s Deleting container registry repository %s=%s %s=%d\n",
		c.ProgressIcon(),
		c.Blue("repo"), repo.FullName(),
		c.Blue("repository"), o.repositoryID)

	_, err = client.ContainerRegistry.DeleteRegistryRepository(repo.FullName(), o.repositoryID)
	if err != nil {
		return cmdutils.WrapError(err, registryutils.ProjectScopedRepositoryError("failed to delete container registry", o.repositoryID, repo.FullName())+".")
	}

	o.io.LogInfof(c.Bold("%s Container registry repository %d deleted.\n"), c.RedCheck(), o.repositoryID)
	return nil
}
