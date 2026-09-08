package list

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
	"gitlab.com/gitlab-org/cli/internal/utils"
)

type options struct {
	repositoryID int64
	details      bool
	page         int
	perPage      int
	outputFormat string

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
		Use:   "list <repository-id> [flags]",
		Short: "List container registry repository tags.",
		Long: heredoc.Doc(`
			The repository ID must belong to the selected project. Use -R/--repo
			to specify the owning project when running this command outside that
			project's Git checkout.
		`),
		Aliases: []string{"ls"},
		Args:    cobra.ExactArgs(1),
		Example: heredoc.Doc(`
			# List tags for a container registry repository
			glab container-registry tag list 123

			# List tags for a container registry repository in another project
			glab container-registry tag list 123 -R gitlab-org/cli`),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(args); err != nil {
				return err
			}

			return opts.run(cmd.Context())
		},
	}

	cmd.Flags().IntVarP(&opts.page, "page", "p", 1, "Page number.")
	cmd.Flags().IntVarP(&opts.perPage, "per-page", "P", 30, "Number of items to list per page.")
	cmd.Flags().BoolVar(&opts.details, "details", false, "Fetch digest, size, and creation time for each tag. Makes one API call per tag.")
	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)

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

func (o *options) run(ctx context.Context) error {
	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	repo, err := o.baseRepo()
	if err != nil {
		return err
	}

	tags, resp, err := client.ContainerRegistry.ListRegistryRepositoryTags(
		repo.FullName(),
		o.repositoryID,
		&gitlab.ListRegistryRepositoryTagsOptions{
			ListOptions: gitlab.ListOptions{
				Page:    int64(o.page),
				PerPage: int64(o.perPage),
			},
		},
	)
	if err != nil {
		return cmdutils.WrapError(err, registryutils.ProjectScopedRepositoryError("failed to fetch container registry tags from", o.repositoryID, repo.FullName())+".")
	}

	if o.details {
		tags, err = o.fetchTagDetails(ctx, client, repo.FullName(), tags)
		if err != nil {
			return err
		}
	}

	if o.outputFormat == "json" {
		return o.io.PrintJSON(registryutils.NewTagJSONList(tags, o.details))
	}

	title := utils.NewListTitle("container registry tag")
	title.RepoName = repo.FullName()
	title.Page = o.page
	title.CurrentPageTotal = len(tags)
	title.EmptyMessage = fmt.Sprintf("No container registry tags available on %s.", repo.FullName())
	if resp != nil {
		title.Total = int(resp.TotalItems)
	}

	o.io.LogInfof("%s\n", title.Describe())
	if len(tags) > 0 {
		if o.details {
			o.io.LogInfof("%s\n", registryutils.DisplayTagsWithDetails(o.io, tags))
		} else {
			o.io.LogInfof("%s\n", registryutils.DisplayTags(tags))
		}
	}

	return nil
}

func (o *options) fetchTagDetails(ctx context.Context, client *gitlab.Client, repoName string, tags []*gitlab.RegistryRepositoryTag) ([]*gitlab.RegistryRepositoryTag, error) {
	detailedTags := make([]*gitlab.RegistryRepositoryTag, 0, len(tags))
	for _, tag := range tags {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		detailedTag, _, err := client.ContainerRegistry.GetRegistryRepositoryTagDetail(
			repoName,
			o.repositoryID,
			tag.Name,
		)
		if err != nil {
			return nil, cmdutils.WrapError(err, registryutils.ProjectScopedTagError("failed to fetch container registry tag details for", tag.Name, o.repositoryID, repoName)+".")
		}
		detailedTags = append(detailedTags, detailedTag)
	}

	return detailedTags, nil
}
