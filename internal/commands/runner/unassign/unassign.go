package unassign

import (
	"context"
	"errors"
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
	io           *iostreams.IOStreams
	baseRepo     func() (glrepo.Interface, error)
	gitlabClient func() (*gitlab.Client, error)
	runnerID     int64
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		baseRepo:     f.BaseRepo,
		gitlabClient: f.GitLabClient,
	}

	cmd := &cobra.Command{
		Use:   "unassign <runner-id> [flags]",
		Short: "Unassign a runner from a project.",
		Long: heredoc.Docf(`
			Unassign a runner from a project.
			You cannot unassign a runner from the owner project.
			Use %[1]sglab runner delete%[1]s instead.

			Requires the Maintainer or Owner role for the project.
		`, "`"),
		Example: heredoc.Doc(`
			# Unassign runner 9 from the current repository
			glab runner unassign 9

			# Unassign runner 9 from a specific repository
			glab runner unassign 9 -R owner/repo`),
		Args: cobra.ExactArgs(1),
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

	cmdutils.EnableRepoOverride(cmd, f)
	return cmd
}

func (o *options) complete(args []string) error {
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return cmdutils.WrapError(err, "invalid runner ID")
	}
	o.runnerID = id
	return nil
}

func (o *options) run(ctx context.Context) error {
	repo, err := o.baseRepo()
	if err != nil {
		return cmdutils.FlagError{Err: errors.New("-R is required to specify the project (e.g. owner/repo or full URL)")}
	}
	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	_, err = client.Runners.DisableProjectRunner(
		repo.FullName(),
		o.runnerID,
		gitlab.WithContext(ctx),
	)
	if err != nil {
		return cmdutils.WrapError(err, "failed to unassign runner from project")
	}
	o.io.LogInfof("Runner %d has been unassigned from project %s.\n", o.runnerID, repo.FullName())
	return nil
}
