package unlock

import (
	"context"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

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
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		baseRepo:     f.BaseRepo,
		gitlabClient: f.GitLabClient,
	}

	cmd := &cobra.Command{
		Use:   "unlock <state>",
		Short: `Unlock the given state.`,
		Long: heredoc.Doc(`
			Unlocking a state releases a lock held on it, so others can modify
			the state again.
		`),
		Example: heredoc.Doc(`
			glab opentofu state unlock production`),
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(args)
			return opts.run(cmd.Context())
		},
	}

	return cmd
}

func (o *options) complete(args []string) {
	o.stateName = args[0]
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

	if _, err := client.TerraformStates.Unlock(repo.FullName(), o.stateName, gitlab.WithContext(ctx)); err != nil {
		return err
	}

	o.io.LogInfof("Unlocked state %s\n", o.stateName)
	return nil
}
