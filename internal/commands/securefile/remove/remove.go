package remove

import (
	"context"
	"fmt"
	"strconv"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	forceDelete bool
	fileID      int64

	io           *iostreams.IOStreams
	gitlabClient func() (*gitlab.Client, error)
	baseRepo     func() (glrepo.Interface, error)
}

func NewCmdRemove(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
		baseRepo:     f.BaseRepo,
	}
	securefileRemoveCmd := &cobra.Command{
		Use:     "remove <fileID>",
		Short:   `Remove a secure file.`,
		Long:    ``,
		Aliases: []string{"rm", "delete"},
		Args:    cobra.ExactArgs(1),
		Example: heredoc.Doc(`
			# Remove a project's secure file using the file's ID.
			glab securefile remove 1

			# Skip the confirmation prompt and force delete.
			glab securefile remove 1 -y

			# Remove a project's secure file with 'rm' alias.
			glab securefile rm 1

			# Remove a project's secure file with 'delete' alias.
			glab securefile delete 1`),
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

	securefileRemoveCmd.Flags().BoolVarP(&opts.forceDelete, "yes", "y", false, "Skip the confirmation prompt. (default false)")

	return securefileRemoveCmd
}

func (o *options) complete(args []string) error {
	fileID, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Errorf("Secure file ID must be an integer: %s", args[0])
	}
	o.fileID = int64(fileID)

	return nil
}

func (o *options) validate() error {
	if !o.forceDelete && !o.io.PromptEnabled() {
		return &cmdutils.FlagError{Err: fmt.Errorf("--yes or -y flag is required when not running interactively.")}
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
		o.io.LogInfof("This action will permanently delete secure file %d immediately.\n\n", o.fileID)
		err = o.io.Confirm(ctx, &o.forceDelete, fmt.Sprintf("Are you ABSOLUTELY SURE you wish to delete this secure file %d?", o.fileID))
		if err != nil {
			return cmdutils.WrapError(err, "could not prompt")
		}
	}

	if !o.forceDelete {
		return cmdutils.CancelError()
	}

	color := o.io.Color()
	o.io.LogInfof("%s Deleting secure file %s=%s %s=%d\n",
		color.ProgressIcon(),
		color.Blue("repo"), repo.FullName(),
		color.Blue("fileID"), o.fileID)

	_, err = client.SecureFiles.RemoveSecureFile(repo.FullName(), o.fileID)
	if err != nil {
		return fmt.Errorf("Error removing secure file: %v", err)
	}

	o.io.LogInfof(color.Bold("%s Secure file %d deleted.\n"), color.RedCheck(), o.fileID)

	return nil
}
