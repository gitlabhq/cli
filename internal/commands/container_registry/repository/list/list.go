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
	group             string
	includeTags       bool
	includeTagDetails bool
	includeTagsCount  bool
	page              int
	perPage           int
	outputFormat      string

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
		Use:   "list [flags]",
		Short: "List container registry repositories.",
		Long: heredoc.Docf(`
			By default, repositories are listed for the current project. Use %[1]s--repo%[1]s
			to target another project, or %[1]s--group%[1]s to list repositories for a group.
		`, "`"),
		Aliases: []string{"ls"},
		Args:    cobra.NoArgs,
		Example: heredoc.Doc(`
			# List container registry repositories for the current project
			glab container-registry repository list

			# List container registry repositories for another project
			glab container-registry repository list -R gitlab-org/cli

			# List container registry repositories for a group
			glab container-registry repository list --group gitlab-org`),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd); err != nil {
				return err
			}

			if err := opts.validate(); err != nil {
				return err
			}

			return opts.run(cmd.Context())
		},
	}

	cmdutils.EnableRepoOverride(cmd, f)
	cmd.PersistentFlags().StringP("group", "g", "", "List container registry repositories for a group.")

	fl := cmd.Flags()
	fl.BoolVar(&opts.includeTags, "include-tags", false, "Include tags in the response. Project repositories only.")
	fl.BoolVar(&opts.includeTagDetails, "include-tag-details", false, "Fetch digest, size, and creation time for included tags. Makes one API call per tag. Project JSON output only. Implies --include-tags.")
	fl.BoolVar(&opts.includeTagsCount, "include-tags-count", true, "Include the number of tags in the response. Project repositories only.")
	fl.IntVarP(&opts.page, "page", "p", 1, "Page number.")
	fl.IntVarP(&opts.perPage, "per-page", "P", 30, "Number of items to list per page.")
	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)

	return cmd
}

func (o *options) complete(cmd *cobra.Command) error {
	group, err := cmdutils.GroupOverride(cmd)
	if err != nil {
		return err
	}
	o.group = group

	return nil
}

func (o *options) validate() error {
	if o.group != "" && o.includeTagDetails {
		return &cmdutils.FlagError{Err: fmt.Errorf("--include-tag-details is only available for project repositories")}
	}
	if o.includeTagDetails && o.outputFormat != "json" {
		return &cmdutils.FlagError{Err: fmt.Errorf("--include-tag-details requires --output json")}
	}

	return nil
}

func (o *options) run(ctx context.Context) error {
	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	listOpts := gitlab.ListOptions{
		Page:    int64(o.page),
		PerPage: int64(o.perPage),
	}

	var repoName string
	var repositories []*gitlab.RegistryRepository
	var resp *gitlab.Response

	if o.group != "" {
		repoName = o.group
		repositories, resp, err = client.ContainerRegistry.ListGroupRegistryRepositories(
			o.group,
			&gitlab.ListGroupRegistryRepositoriesOptions{ListOptions: listOpts},
		)
	} else {
		repo, repoErr := o.baseRepo()
		if repoErr != nil {
			return repoErr
		}
		repoName = repo.FullName()
		opts := &gitlab.ListProjectRegistryRepositoriesOptions{
			ListOptions: listOpts,
		}
		if o.includeTags || o.includeTagDetails {
			opts.Tags = new(true)
		}
		if o.includeTagsCount {
			opts.TagsCount = new(true)
		}
		repositories, resp, err = client.ContainerRegistry.ListProjectRegistryRepositories(
			repo.FullName(),
			opts,
		)
	}
	if err != nil {
		if o.group == "" {
			return cmdutils.WrapError(err, fmt.Sprintf("failed to list container registry repositories from %s; ensure %s exists and has container registry enabled, or specify the owning project with -R <project>.", repoName, repoName))
		}
		return err
	}

	if o.outputFormat == "json" {
		showTagsCount := o.group == "" && o.includeTagsCount
		if o.includeTagDetails {
			if err := o.fetchRepositoryTagDetails(ctx, client, repoName, repositories); err != nil {
				return err
			}
		}

		return o.io.PrintJSON(registryutils.NewRepositoryJSONList(repositories, o.includeTagDetails, showTagsCount))
	}

	title := utils.NewListTitle("container registry repository")
	title.RepoName = repoName
	title.Page = o.page
	title.CurrentPageTotal = len(repositories)
	title.EmptyMessage = fmt.Sprintf("No container registry repositories available on %s.", repoName)
	if resp != nil {
		title.Total = int(resp.TotalItems)
	}

	o.io.LogInfof("%s\n", title.Describe())
	if len(repositories) > 0 {
		o.io.LogInfof("%s\n", registryutils.DisplayRepositories(o.io, repositories, o.group == "" && o.includeTagsCount))
	}

	return nil
}

func (o *options) fetchRepositoryTagDetails(ctx context.Context, client *gitlab.Client, repoName string, repositories []*gitlab.RegistryRepository) error {
	for _, repository := range repositories {
		detailedTags := make([]*gitlab.RegistryRepositoryTag, 0, len(repository.Tags))
		for _, tag := range repository.Tags {
			if err := ctx.Err(); err != nil {
				return err
			}
			detailedTag, _, err := client.ContainerRegistry.GetRegistryRepositoryTagDetail(
				repoName,
				repository.ID,
				tag.Name,
			)
			if err != nil {
				return cmdutils.WrapError(err, fmt.Sprintf("failed to fetch container registry tag %q from repository %d.", tag.Name, repository.ID))
			}
			detailedTags = append(detailedTags, detailedTag)
		}
		repository.Tags = detailedTags
	}

	return nil
}
