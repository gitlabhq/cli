package remove

import (
	"context"
	"fmt"
	"strconv"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/securefile/helpers"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	forceDelete bool
	fileID      int64
	fileName    string

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
		Use:   "remove [<id> | --id <id> | --name <name>] [flags]",
		Short: `Remove a secure file from a project.`,
		Long: heredoc.Docf(`
		Remove a secure file from a project, identified by its numeric ID.
		The command asks for confirmation before deleting; use %[1]s-y%[1]s to skip
		the prompt in scripts.

		By default, the file is removed from the current project. Use %[1]s--repo%[1]s
		to target another project.
		`, "`"),
		Aliases: []string{"rm", "delete"},
		Args:    cobra.MaximumNArgs(1),
		Example: heredoc.Doc(`
			# Remove a secure file by ID
			glab securefile remove 1
			glab securefile remove --id 1

			# Remove a secure file by name
			glab securefile remove --name example.txt

			# Skip the confirmation prompt
			glab securefile remove 1 -y
			glab securefile remove --name example.txt -y

			# Use the 'rm' alias
			glab securefile rm 1

			# Use the 'delete' alias
			glab securefile delete --name example.txt
		`),
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

	securefileRemoveCmd.Flags().BoolVarP(&opts.forceDelete, "yes", "y", false, "Skip the confirmation prompt.")
	securefileRemoveCmd.Flags().Int64("id", 0, "ID of the secure file to remove.")
	securefileRemoveCmd.Flags().String("name", "", "Name of the secure file to remove.")

	securefileRemoveCmd.MarkFlagsMutuallyExclusive("id", "name")

	return securefileRemoveCmd
}

func (o *options) complete(cmd *cobra.Command, args []string) error {
	// A positional ID and the --id/--name flags select the file in conflicting
	// ways. Rejecting the combination avoids silently ignoring the argument and
	// removing a different file than the one named on the command line.
	if len(args) > 0 && (cmd.Flags().Changed("id") || cmd.Flags().Changed("name")) {
		return &cmdutils.FlagError{
			Err: fmt.Errorf("the secure file ID argument %q cannot be combined with --id or --name", args[0]),
		}
	}

	name, err := cmd.Flags().GetString("name")
	if err != nil {
		return fmt.Errorf("unable to get name flag: %w", err)
	}
	if name != "" {
		o.fileName = name
		return nil
	}

	if cmd.Flags().Changed("id") {
		o.fileID, err = cmd.Flags().GetInt64("id")
		if err != nil {
			return fmt.Errorf("unable to get id flag: %w", err)
		}
	} else {
		if len(args) == 0 {
			return &cmdutils.FlagError{Err: fmt.Errorf("provide a secure file ID argument, or the --id or --name flag")}
		}
		o.fileID, err = strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("secure file ID must be an integer: %s", args[0])
		}
	}

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

	if o.fileName != "" {
		secureFile, err := helpers.GetSecureFileByName(client, o.fileName, repo.FullName())
		if err != nil {
			return err
		}

		o.fileID = secureFile.ID
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
		return fmt.Errorf("error removing secure file: %w", err)
	}

	o.io.LogInfof(color.Bold("%s Secure file %d deleted.\n"), color.RedCheck(), o.fileID)

	return nil
}
