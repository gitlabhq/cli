package download

import (
	"context"
	"fmt"
	"io"
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
	io           *iostreams.IOStreams
	baseRepo     func() (glrepo.Interface, error)
	gitlabClient func() (*gitlab.Client, error)

	stateName string
	serial    *uint64
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		baseRepo:     f.BaseRepo,
		gitlabClient: f.GitLabClient,
	}

	cmd := &cobra.Command{
		Use:   "download <state> [<serial>]",
		Short: `Download the given state and output as JSON to stdout.`,
		Long: heredoc.Doc(`
			Downloads the latest serial version if no serial is specified.
		`),
		Example: heredoc.Doc(`
			# Download the latest serial of the state production
			glab opentofu state download production

			# Download the serial 42 of the state production
			glab opentofu state download production 42`),
		Args: cobra.RangeArgs(1, 2),
		Annotations: map[string]string{
			mcpannotations.Exclude: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(args); err != nil {
				return err
			}

			return opts.run(cmd.Context())
		},
	}

	return cmd
}

func (o *options) complete(args []string) error {
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

	var r io.Reader
	switch o.serial {
	case nil:
		r, _, err = client.TerraformStates.DownloadLatest(repo.FullName(), o.stateName, gitlab.WithContext(ctx))
	default:
		r, _, err = client.TerraformStates.Download(repo.FullName(), o.stateName, *o.serial, gitlab.WithContext(ctx))
	}

	if err != nil {
		return err
	}

	_, err = io.Copy(o.io.StdOut, r)
	return err
}
