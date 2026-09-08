package note

import (
	"context"
	"fmt"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/mr/mrutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

func NewCmdNote(f cmdutils.Factory) *cobra.Command {
	mrCreateNoteCmd := &cobra.Command{
		Use:     "note [<id> | <branch>]",
		Aliases: []string{"comment"},
		Short:   "Manage comments and discussions on a merge request.",
		Long: heredoc.Docf(`
			Creates a comment by default. Use %[1]s--resolve%[1]s or
			%[1]s--unresolve%[1]s to manage existing discussion threads.
		`, "`"),
		Args: cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GitLabClient()
			if err != nil {
				return err
			}

			mr, repo, err := mrutils.MRFromArgs(cmd.Context(), f, args, "any")
			if err != nil {
				return err
			}

			// Check if we're resolving or unresolving
			resolveNoteID, _ := cmd.Flags().GetInt64("resolve")
			unresolveNoteID, _ := cmd.Flags().GetInt64("unresolve")

			if resolveNoteID != 0 {
				return resolveDiscussion(cmd.Context(), client, f, mr, repo, resolveNoteID, true)
			}

			if unresolveNoteID != 0 {
				return resolveDiscussion(cmd.Context(), client, f, mr, repo, unresolveNoteID, false)
			}

			// Create note (existing behavior)
			body, _ := cmd.Flags().GetString("message")
			if strings.TrimSpace(body) == "" {
				body, err = getBodyFromStdinOrEditor(f, cmd)
				if err != nil {
					return err
				}
			}
			if strings.TrimSpace(body) == "" {
				return fmt.Errorf("aborted: note has an empty message")
			}

			uniqueNoteEnabled, _ := cmd.Flags().GetBool("unique")
			if uniqueNoteEnabled {
				found, err := deduplicateNote(f.IO(), client, repo.FullName(), mr.IID, body, mr.WebURL)
				if err != nil {
					return err
				}
				if found {
					return nil
				}
			}

			noteInfo, _, err := client.Notes.CreateMergeRequestNote(repo.FullName(), mr.IID, &gitlab.CreateMergeRequestNoteOptions{Body: &body})
			if err != nil {
				return err
			}

			f.IO().LogInfof("%s#note_%d\n", mr.WebURL, noteInfo.ID)
			return nil
		},
	}

	mrCreateNoteCmd.Flags().StringP("message", "m", "", "Comment or note message.")
	mrCreateNoteCmd.Flags().Bool("unique", false, "Do not create a comment if a comment with the same body already exists.")
	mrCreateNoteCmd.Flags().Int64("resolve", 0, "Resolve the discussion containing the specified note ID.")
	mrCreateNoteCmd.Flags().Int64("unresolve", 0, "Unresolve the discussion containing the specified note ID.")

	mrCreateNoteCmd.MarkFlagsMutuallyExclusive("message", "resolve")
	mrCreateNoteCmd.MarkFlagsMutuallyExclusive("message", "unresolve")
	mrCreateNoteCmd.MarkFlagsMutuallyExclusive("resolve", "unresolve")

	cobra.CheckErr(mrCreateNoteCmd.Flags().MarkDeprecated("resolve", "use `glab mr note resolve` instead."))
	cobra.CheckErr(mrCreateNoteCmd.Flags().MarkDeprecated("unresolve", "use `glab mr note reopen` instead."))
	cobra.CheckErr(mrCreateNoteCmd.Flags().MarkDeprecated("message", "use `glab mr note create` instead."))
	cobra.CheckErr(mrCreateNoteCmd.Flags().MarkDeprecated("unique", "use `glab mr note create` instead."))

	mrCreateNoteCmd.AddCommand(NewCmdCreate(f))
	mrCreateNoteCmd.AddCommand(NewCmdList(f))
	mrCreateNoteCmd.AddCommand(NewCmdResolve(f))
	mrCreateNoteCmd.AddCommand(NewCmdReopen(f))
	mrCreateNoteCmd.AddCommand(NewCmdUpdate(f))
	mrCreateNoteCmd.AddCommand(NewCmdDelete(f))

	return mrCreateNoteCmd
}

func resolveDiscussion(ctx context.Context, client *gitlab.Client, f cmdutils.Factory, mr *gitlab.MergeRequest, repo glrepo.Interface, noteID int64, resolve bool) error {
	discussions, err := mrutils.ListAllDiscussions(ctx, client, repo.FullName(), mr.IID, &gitlab.ListMergeRequestDiscussionsOptions{})
	if err != nil {
		return fmt.Errorf("failed to list discussions: %w", err)
	}

	targetDiscussionID, _, err := mrutils.FindNoteInDiscussions(discussions, noteID)
	if err != nil {
		return fmt.Errorf("note %d not found in merge request !%d", noteID, mr.IID)
	}

	action := "resolve"
	if !resolve {
		action = "unresolve"
	}

	_, _, err = client.Discussions.ResolveMergeRequestDiscussion(
		repo.FullName(),
		mr.IID,
		targetDiscussionID,
		&gitlab.ResolveMergeRequestDiscussionOptions{
			Resolved: &resolve,
		},
		gitlab.WithContext(ctx),
	)
	if err != nil {
		return fmt.Errorf("failed to %s discussion: %w", action, err)
	}

	f.IO().LogInfof("✓ Discussion %sd (note #%d in !%d)\n", action, noteID, mr.IID)
	return nil
}
