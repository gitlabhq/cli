package delete

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	io           *iostreams.IOStreams
	baseRepo     func() (glrepo.Interface, error)
	gitlabClient func() (*gitlab.Client, error)

	stateName string
	serial    *uint64
	force     bool
}

var errAbort = errors.New("aborting the delete")

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		baseRepo:     f.BaseRepo,
		gitlabClient: f.GitLabClient,
	}

	cmd := &cobra.Command{
		Use:   "delete <state> [<serial>] [flags]",
		Short: `Delete the given state or if the serial is provided only that version of the given state.`,
		Args:  cobra.MinimumNArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(args); err != nil {
				return err
			}
			return opts.run(cmd.Context())
		},
	}
	cmd.Flags().BoolVarP(&opts.force, "force", "f", false, "Force delete the state without prompting. (default false)")

	return cmd
}

func (o *options) complete(args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New("must provide 1 or 2 positional arguments")
	}

	o.stateName = args[0]

	if len(args) == 2 {
		serial, err := strconv.ParseUint(args[1], 10, 64)
		if err != nil {
			return fmt.Errorf("serial must be an integer, got %q", args[1])
		}
		o.serial = &serial
	}
	return nil
}

func (o *options) run(ctx context.Context) error {
	repo, err := o.baseRepo()
	if err != nil {
		return err
	}

	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	// Prompt user to proceed with deletion
	if !o.force {
		var shouldDelete bool
		if err := o.io.Confirm(ctx, &shouldDelete, "Are you sure you want to delete? This action is destructive"); err != nil {
			return err
		}
		if !shouldDelete {
			return errAbort
		}
	}

	switch o.serial {
	case nil:
		_, err := client.TerraformStates.Delete(repo.FullName(), o.stateName, gitlab.WithContext(ctx))
		if err != nil {
			return err
		}

		fmt.Fprintf(o.io.StdOut, "Deleted state %s\n", o.stateName)
	default:
		_, err := client.TerraformStates.DeleteVersion(repo.FullName(), o.stateName, *o.serial, gitlab.WithContext(ctx))
		if err != nil {
			return err
		}

		fmt.Fprintf(o.io.StdOut, "Deleted version with serial %d of state %s\n", *o.serial, o.stateName)
	}

	return nil
}
