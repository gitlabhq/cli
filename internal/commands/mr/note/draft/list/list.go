package list

import (
	"context"
	"slices"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/mr/mrutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type options struct {
	io           *iostreams.IOStreams
	factory      cmdutils.Factory
	gitlabClient func() (*gitlab.Client, error)

	// Flags.
	filePath     string
	outputFormat string

	// Populated in complete.
	client *gitlab.Client
	mr     *gitlab.MergeRequest
	repo   glrepo.Interface
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		factory:      f,
		gitlabClient: f.GitLabClient,
	}

	cmd := &cobra.Command{
		Use:   "list [<id> | <branch>]",
		Short: "List your pending review comments on a merge request. (EXPERIMENTAL)",
		Long: heredoc.Docf(`
			Human-readable output shows the ID of each pending comment, the file and line it targets, and the discussion it replies to. Pass the ID to %[1]sglab mr note draft update%[1]s or %[1]sglab mr note draft delete%[1]s.

			JSON output returns the pending comment objects as the API reports them.
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			# List your pending review comments on the current branch's merge request
			glab mr note draft list

			# List your pending review comments on merge request 123
			glab mr note draft list 123

			# List only pending comments on a specific file
			glab mr note draft list 123 --file src/main.go

			# Print the IDs of your pending comments
			glab mr note draft list 123 -F json | jq '.[].id'
		`),
		Args: cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd, args); err != nil {
				return err
			}
			return opts.run(cmd.Context())
		},
	}

	cmd.Flags().StringVar(&opts.filePath, "file", "", "Show only pending diff comments on this file path.")
	cmdutils.EnableJSONOutput(cmd, f.IO(), &opts.outputFormat)

	return cmd
}

func (o *options) complete(cmd *cobra.Command, args []string) error {
	client, err := o.gitlabClient()
	if err != nil {
		return err
	}
	o.client = client

	mr, repo, err := mrutils.MRFromArgs(cmd.Context(), o.factory, args, "any")
	if err != nil {
		return err
	}
	o.mr = mr
	o.repo = repo
	return nil
}

func (o *options) run(ctx context.Context) error {
	drafts, err := mrutils.ListAllDraftNotes(ctx, o.client, o.repo.FullName(), o.mr.IID)
	if err != nil {
		return err
	}

	if o.filePath != "" {
		drafts = slices.DeleteFunc(drafts, func(d *gitlab.DraftNote) bool {
			return d.Position == nil || (d.Position.NewPath != o.filePath && d.Position.OldPath != o.filePath)
		})
	}

	if o.outputFormat == "json" {
		return o.io.PrintJSON(drafts)
	}
	if len(drafts) == 0 {
		o.io.LogInfo("No pending review comments found.")
		return nil
	}
	mrutils.PrintDraftNotes(o.io, drafts)
	return nil
}
