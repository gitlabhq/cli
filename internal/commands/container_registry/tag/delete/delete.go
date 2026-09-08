package delete

import (
	"context"
	"fmt"
	"regexp"

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
	repositoryID    int64
	tagName         string
	nameRegexDelete string
	nameRegexKeep   string
	keepN           int
	olderThan       string
	keepNSet        bool
	bulkDelete      bool
	forceDelete     bool

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
		Use:   "delete <repository-id> [<tag-name>] [flags]",
		Short: "Delete container registry tags.",
		Long: heredoc.Doc(`
			Delete one container registry tag by name, or delete matching tags in
			bulk by filter.

			Deleting a single tag is synchronous. Bulk deletion is scheduled
			asynchronously by GitLab; matching tags may remain visible until the
			background deletion job has completed.

			To bulk delete tags, omit <tag-name> and provide at least one bulk
			deletion flag: --name-regex-delete, --name-regex-keep, --keep-n, or
			--older-than.

			The repository ID must belong to the selected project. Use -R/--repo
			to specify the owning project when running this command outside that
			project's Git checkout.
		`),
		Aliases: []string{"del"},
		Args:    cobra.RangeArgs(1, 2),
		Example: heredoc.Doc(`
			# Delete a container registry tag with a confirmation prompt
			glab container-registry tag delete 123 latest

			# Skip the confirmation prompt
			glab container-registry tag delete 123 latest --yes

			# Schedule tags matching a regular expression for deletion
			glab container-registry tag delete 123 --name-regex-delete '^release-.*' --yes

			# Schedule old tags for deletion, but keep the 10 most recent matching tags
			glab container-registry tag delete 123 --name-regex-delete '.*' --keep-n 10 --older-than 30d --yes

			# Delete a container registry tag in another project
			glab container-registry tag delete 123 latest -R gitlab-org/cli`),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd, args); err != nil {
				return err
			}
			if err := opts.validate(); err != nil {
				return err
			}

			return opts.run(cmd.Context())
		},
	}

	fl := cmd.Flags()
	fl.StringVar(&opts.nameRegexDelete, "name-regex-delete", "", "Regular expression for tag names to delete. Bulk deletion only; scheduled asynchronously.")
	fl.StringVar(&opts.nameRegexKeep, "name-regex-keep", "", "Regular expression for tag names to keep. Bulk deletion only; scheduled asynchronously.")
	fl.IntVar(&opts.keepN, "keep-n", 0, "Keep the latest N matching tags. Bulk deletion only; scheduled asynchronously.")
	fl.StringVar(&opts.olderThan, "older-than", "", "Delete tags older than the given duration, such as 7d or 1month. Bulk deletion only; scheduled asynchronously.")
	fl.BoolVarP(&opts.forceDelete, "yes", "y", false, "Skip the confirmation prompt.")

	return cmd
}

func (o *options) complete(cmd *cobra.Command, args []string) error {
	repositoryID, err := registryutils.ParseID(args[0], "repository ID")
	if err != nil {
		return &cmdutils.FlagError{Err: err}
	}
	o.repositoryID = repositoryID
	if len(args) == 2 {
		o.tagName = args[1]
	}
	o.bulkDelete = cmd.Flags().Changed("name-regex-delete") ||
		cmd.Flags().Changed("name-regex-keep") ||
		cmd.Flags().Changed("keep-n") ||
		cmd.Flags().Changed("older-than")
	o.keepNSet = cmd.Flags().Changed("keep-n")

	return nil
}

func (o *options) validate() error {
	if o.tagName == "" && !o.bulkDelete {
		return &cmdutils.FlagError{Err: fmt.Errorf("either a tag name or at least one bulk deletion flag is required")}
	}
	if o.tagName != "" && o.bulkDelete {
		return &cmdutils.FlagError{Err: fmt.Errorf("either a tag name or bulk deletion flags must be passed, but not both")}
	}
	if o.bulkDelete && o.nameRegexDelete == "" && o.nameRegexKeep == "" && !o.keepNSet && o.olderThan == "" {
		return &cmdutils.FlagError{Err: fmt.Errorf("at least one bulk deletion flag must be non-empty")}
	}
	if o.nameRegexDelete != "" {
		if _, err := regexp.Compile(o.nameRegexDelete); err != nil {
			return &cmdutils.FlagError{Err: fmt.Errorf("--name-regex-delete is not a valid regular expression: %w", err)}
		}
	}
	if o.nameRegexKeep != "" {
		if _, err := regexp.Compile(o.nameRegexKeep); err != nil {
			return &cmdutils.FlagError{Err: fmt.Errorf("--name-regex-keep is not a valid regular expression: %w", err)}
		}
	}
	if o.keepN < 0 {
		return &cmdutils.FlagError{Err: fmt.Errorf("--keep-n must be zero or a positive integer")}
	}
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

	if o.bulkDelete {
		return o.runBulkDelete(ctx, client, repo)
	}

	return o.runSingleDelete(ctx, client, repo)
}

func (o *options) runSingleDelete(ctx context.Context, client *gitlab.Client, repo glrepo.Interface) error {
	tagPath := fmt.Sprintf("%s:%s", repo.FullName(), o.tagName)

	if !o.forceDelete && o.io.PromptEnabled() {
		o.io.LogErrorf("This action will permanently delete container registry tag %q.\n\n", tagPath)
		err := o.io.Confirm(ctx, &o.forceDelete, fmt.Sprintf("Are you ABSOLUTELY SURE you wish to delete container registry tag %q?", tagPath))
		if err != nil {
			return cmdutils.WrapError(err, "could not prompt")
		}
	}

	if !o.forceDelete {
		return cmdutils.CancelError()
	}

	c := o.io.Color()
	o.io.LogInfof("%s Deleting container registry tag %s\n",
		c.ProgressIcon(),
		tagPath)

	_, err := client.ContainerRegistry.DeleteRegistryRepositoryTag(repo.FullName(), o.repositoryID, o.tagName)
	if err != nil {
		return cmdutils.WrapError(err, registryutils.ProjectScopedTagError("failed to delete container registry", o.tagName, o.repositoryID, repo.FullName())+".")
	}

	o.io.LogInfof(c.Bold("%s Container registry tag %q deleted.\n"), c.RedCheck(), o.tagName)
	return nil
}

func (o *options) runBulkDelete(ctx context.Context, client *gitlab.Client, repo glrepo.Interface) error {
	if !o.forceDelete && o.io.PromptEnabled() {
		o.io.LogErrorf("%s", bulkDeleteConfirmationMessage(o.repositoryID, repo.FullName(), o.nameRegexDelete, o.nameRegexKeep, o.keepN, o.olderThan))
		err := o.io.Confirm(ctx, &o.forceDelete, fmt.Sprintf("Are you ABSOLUTELY SURE you wish to schedule matching container registry tags for deletion from repository %d?", o.repositoryID))
		if err != nil {
			return cmdutils.WrapError(err, "could not prompt")
		}
	}

	if !o.forceDelete {
		return cmdutils.CancelError()
	}

	deleteOpts := &gitlab.DeleteRegistryRepositoryTagsOptions{}
	if o.nameRegexDelete != "" {
		deleteOpts.NameRegexpDelete = new(o.nameRegexDelete)
	}
	if o.nameRegexKeep != "" {
		deleteOpts.NameRegexpKeep = new(o.nameRegexKeep)
	}
	if o.keepNSet {
		keepN := int64(o.keepN)
		deleteOpts.KeepN = new(keepN)
	}
	if o.olderThan != "" {
		deleteOpts.OlderThan = new(o.olderThan)
	}

	c := o.io.Color()
	o.io.LogInfof("%s Scheduling container registry tags for deletion %s=%s %s=%d\n",
		c.ProgressIcon(),
		c.Blue("repo"), repo.FullName(),
		c.Blue("repository"), o.repositoryID)

	_, err := client.ContainerRegistry.DeleteRegistryRepositoryTags(repo.FullName(), o.repositoryID, deleteOpts)
	if err != nil {
		return cmdutils.WrapError(err, registryutils.ProjectScopedRepositoryError("failed to delete container registry tags from", o.repositoryID, repo.FullName())+".")
	}

	o.io.LogInfof(c.Bold("%s Container registry tags scheduled for deletion. They may remain visible until GitLab finishes the background deletion job.\n"), c.RedCheck())
	return nil
}

func bulkDeleteConfirmationMessage(repositoryID int64, repoName string, nameRegexDelete string, nameRegexKeep string, keepN int, olderThan string) string {
	return fmt.Sprintf(heredoc.Doc(`
		This action schedules container registry tags for deletion from repository %d on %s.

		Filters:
		  name regex delete: %s
		  name regex keep: %s
		  keep latest: %d
		  older than: %s

		The matching tags may remain visible until the background deletion job has completed.

	`), repositoryID, repoName, emptyValue(nameRegexDelete), emptyValue(nameRegexKeep), keepN, emptyValue(olderThan))
}

func emptyValue(value string) string {
	if value == "" {
		return "<unset>"
	}

	return value
}
