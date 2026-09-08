package approvers

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/mr/mrutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	outputFormat string

	io           *iostreams.IOStreams
	gitlabClient func() (*gitlab.Client, error)
	baseRepo     func() (glrepo.Interface, error)
}

func NewCmdApprovers(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
		baseRepo:     f.BaseRepo,
	}

	mrApproversCmd := &cobra.Command{
		Use:   "approvers [<id> | <branch>] [flags]",
		Short: `List eligible approvers for merge requests in any state.`,
		Long: heredoc.Doc(`
			Lists users and groups eligible to approve, based on the approval
			rules configured for the project.
		`),
		Example: heredoc.Doc(`
			glab mr approvers 123
			glab mr approvers branch-name`),
		Aliases: []string{},
		Args:    cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := opts.gitlabClient()
			if err != nil {
				return err
			}

			// Obtain the MR from the positional arguments, but allow users to find approvers for
			// merge requests in any valid state
			mr, repo, err := mrutils.MRFromArgs(cmd.Context(), f, args, "any")
			if err != nil {
				return err
			}

			mrApprovals, _, err := client.MergeRequestApprovals.GetApprovalState(repo.FullName(), mr.IID)
			if err != nil {
				return err
			}

			if opts.outputFormat == "json" {
				return opts.io.PrintJSON(mrApprovals)
			}

			opts.io.LogInfof("\nListing merge request !%d eligible approvers:\n", mr.IID)
			mrutils.PrintMRApprovalState(opts.io, mrApprovals)

			return nil
		},
	}

	cmdutils.EnableJSONOutput(mrApproversCmd, opts.io, &opts.outputFormat)

	return mrApproversCmd
}
