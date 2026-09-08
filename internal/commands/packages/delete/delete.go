package delete

import (
	"context"
	"fmt"
	"strconv"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	forceDelete bool
	packageID   int64

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
		Use:   "delete <id>",
		Short: `Delete a package from a project's package registry.`,
		Long: heredoc.Docf(`
		Packages are identified by their numeric ID. Use %[1]sglab packages list%[1]s
		to find the ID.

		The command asks for confirmation before deleting; use %[1]s-y%[1]s to skip
		the prompt in scripts.

		By default, the package is removed from the current project. Use %[1]s--repo%[1]s
		to target another project.
		`, "`"),
		Aliases: []string{"rm"},
		Args:    cobra.ExactArgs(1),
		Example: heredoc.Doc(`
			# Delete a package by ID
			glab packages delete 1

			# Skip the confirmation prompt
			glab packages delete 1 -y

			# Use the 'rm' alias
			glab packages rm 1
		`),
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
	packageID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("package ID must be an integer: %s", args[0])
	}
	o.packageID = packageID

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

	confirmed := o.forceDelete
	if !confirmed && o.io.PromptEnabled() {
		o.io.LogInfof("This action will permanently delete package %d immediately.\n\n", o.packageID)
		if err := o.io.Confirm(ctx, &confirmed, fmt.Sprintf("Are you ABSOLUTELY SURE you wish to delete this package %d?", o.packageID)); err != nil {
			return cmdutils.WrapError(err, "could not prompt")
		}
	}

	if !confirmed {
		return cmdutils.CancelError()
	}

	color := o.io.Color()
	o.io.LogInfof("%s Deleting package %s=%s %s=%d\n",
		color.ProgressIcon(),
		color.Blue("repo"), repo.FullName(),
		color.Blue("packageID"), o.packageID)

	_, err = client.Packages.DeleteProjectPackage(repo.FullName(), o.packageID, gitlab.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("failed to delete package: %w", err)
	}

	o.io.LogInfof(color.Bold("%s Package %d deleted.\n"), color.RedCheck(), o.packageID)

	return nil
}
