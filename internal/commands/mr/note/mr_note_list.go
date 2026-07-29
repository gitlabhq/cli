package note

import (
	"context"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/mr/mrutils"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type listOptions struct {
	factory cmdutils.Factory

	args         []string
	noteType     string
	state        string
	filePath     string
	outputFormat string
}

func NewCmdList(f cmdutils.Factory) *cobra.Command {
	opts := &listOptions{
		factory: f,
	}

	mrNoteListCmd := &cobra.Command{
		Use:   "list [<id> | <branch>]",
		Short: "List merge request discussions. (EXPERIMENTAL)",
		Long: heredoc.Docf(`Fetches and displays merge request discussions.

		Human-readable output shows an eight-character prefix for each non-system
		discussion. Use the characters before the ellipsis with
		%[1]sglab mr note create --reply%[1]s. JSON output preserves the full discussion ID in the %[1]sid%[1]s field of each discussion object. Extract it with:
		%[1]sglab mr note list -F json | jq -r '.[].id'%[1]s.

		Supports filtering by note type, resolution state, and file path.
		Supports JSON output for scripting.
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			# List all discussions on the current branch's MR
			glab mr note list

			# List diff comments only
			glab mr note list --type diff

			# List unresolved discussions
			glab mr note list --state unresolved

			# List discussions on a specific file
			glab mr note list --file src/main.go

			# JSON output for scripting
			glab mr note list -F json | jq '.[].notes[].body'

			# List discussions on MR 123
			glab mr note list 123`),
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(args)
			return opts.run(cmd.Context())
		},
	}

	mrNoteListCmd.Flags().VarP(
		cmdutils.NewEnumValue([]string{"all", "general", "diff", "system"}, "all", &opts.noteType),
		"type", "t", "Note type: all, general, diff, system.",
	)
	mrNoteListCmd.Flags().Var(
		cmdutils.NewEnumValue([]string{"all", "resolved", "unresolved"}, "all", &opts.state),
		"state", "Resolution state: all, resolved, unresolved.",
	)
	mrNoteListCmd.Flags().StringVar(&opts.filePath, "file", "", "Show only diff notes on this file path.")
	cmdutils.EnableJSONOutput(mrNoteListCmd, f.IO(), &opts.outputFormat)

	return mrNoteListCmd
}

func (o *listOptions) complete(args []string) {
	o.args = args
}

func (o *listOptions) run(ctx context.Context) error {
	client, err := o.factory.GitLabClient()
	if err != nil {
		return err
	}

	mr, repo, err := mrutils.MRFromArgs(ctx, o.factory, o.args, "any")
	if err != nil {
		return err
	}

	discussions, err := mrutils.ListAllDiscussions(ctx, client, repo.FullName(), mr.IID, &gitlab.ListMergeRequestDiscussionsOptions{})
	if err != nil {
		return err
	}

	filterOpts := mrutils.FilterOpts{}
	if o.noteType != "all" {
		filterOpts.Type = o.noteType
	}
	if o.state != "all" {
		filterOpts.State = o.state
	}
	filterOpts.FilePath = o.filePath

	filtered := mrutils.FilterDiscussions(discussions, filterOpts)

	if o.outputFormat == "json" {
		return o.factory.IO().PrintJSON(filtered)
	}

	out := o.factory.IO().StdOut
	if len(filtered) == 0 {
		o.factory.IO().LogInfo("No discussions found.")
		return nil
	}

	showSystemLogs := o.noteType == "system"

	mrutils.PrintDiscussions(out, o.factory.IO(), filtered, mrutils.PrintDiscussionsOptions{
		ShowSystemLogs:                 showSystemLogs,
		ShowSingleNoteDiscussionPrefix: true,
	})

	return nil
}
