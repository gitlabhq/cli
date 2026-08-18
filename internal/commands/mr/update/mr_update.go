package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"charm.land/huh/v2"
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/mr/mrutils"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

func NewCmdUpdate(f cmdutils.Factory) *cobra.Command {
	mrUpdateCmd := &cobra.Command{
		Use:   "update [<id> | <branch>]",
		Short: `Update a merge request.`,
		Long: heredoc.Docf(`
			Defaults to the currently checked-out branch. Use %[1]s--fill%[1]s to
			automatically fill the title and description from the commit history.
		`, "`"),
		Example: heredoc.Doc(`
		# Mark a merge request as ready
		glab mr update 23 --ready

		# Mark a merge request as draft
		glab mr update 23 --draft

		# Updates the merge request for the current branch
		glab mr update --draft

		# Update merge request with commit information
		glab mr update 23 --fill --fill-commit-body --yes

		# Read the description from a file
		glab mr update 23 --description-file description.md

		# Read the description from standard input
		cat description.md | glab mr update 23 --description-file -`),
		Args: cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error
			var actions []string
			var ua *cmdutils.UserAssignments // assignees
			var ur *cmdutils.UserAssignments // reviewers
			c := f.IO().Color()

			// Check for autofill flags
			autofill, _ := cmd.Flags().GetBool("fill")
			fillCommitBody, _ := cmd.Flags().GetBool("fill-commit-body")

			if !autofill && fillCommitBody {
				return &cmdutils.FlagError{Err: errors.New("--fill-commit-body should be used with --fill")}
			}

			if cmd.Flags().Changed("unassign") && cmd.Flags().Changed("assignee") {
				return &cmdutils.FlagError{Err: errors.New("--assignee and --unassign are mutually exclusive")}
			}

			// Parse assignees Early so we can fail early in case of conflicts
			if cmd.Flags().Changed("assignee") {
				givenAssignees, err := cmd.Flags().GetStringSlice("assignee")
				if err != nil {
					return err
				}
				ua = cmdutils.ParseAssignees(givenAssignees)

				err = ua.VerifyAssignees()
				if err != nil {
					return &cmdutils.FlagError{Err: fmt.Errorf("--assignee: %w", err)}
				}
			}

			if cmd.Flags().Changed("reviewer") {
				givenReviewers, err := cmd.Flags().GetStringSlice("reviewer")
				if err != nil {
					return err
				}
				ur = cmdutils.ParseAssignees(givenReviewers)
				ur.AssignmentType = cmdutils.ReviewerAssignment

				err = ur.VerifyAssignees()
				if err != nil {
					return &cmdutils.FlagError{Err: fmt.Errorf("--reviewer: %w", err)}
				}
			}

			if cmd.Flags().Changed("lock-discussion") && cmd.Flags().Changed("unlock-discussion") {
				return &cmdutils.FlagError{
					Err: errors.New("--lock-discussion and --unlock-discussion can't be used together"),
				}
			}

			if err := cmdutils.ResolveDescriptionFile(f.IO(), cmd); err != nil {
				return err
			}

			client, err := f.GitLabClient()
			if err != nil {
				return err
			}

			mr, repo, err := mrutils.MRFromArgs(cmd.Context(), f, args, "any")
			if err != nil {
				return err
			}

			l := &gitlab.UpdateMergeRequestOptions{}
			var mergeTitle string
			var mergeBody string

			// Handle autofill functionality
			if autofill {
				title, body, err := mrutils.AutofillMRFromCommits(mr.TargetBranch, mr.SourceBranch, fillCommitBody)
				if err != nil {
					return fmt.Errorf("failed to autofill MR content: %w", err)
				}

				// Check if --yes flag is provided to skip confirmation
				skipConfirmation, _ := cmd.Flags().GetBool("yes")

				// Validate that we can prompt if confirmation is needed
				if !skipConfirmation && !f.IO().IsInteractive() {
					return &cmdutils.FlagError{
						Err: errors.New("--yes required with --fill for non-interactive mode"),
					}
				}

				// Show preview and ask for confirmation unless --yes is provided
				if !skipConfirmation {
					// Determine what title will be applied
					var proposedTitle string
					if cmd.Flags().Changed("title") {
						var err error
						proposedTitle, err = cmd.Flags().GetString("title")
						if err != nil {
							return err
						}
					} else {
						proposedTitle = title // from autofill
					}

					// Determine what description will be applied
					var proposedDescription string
					if cmd.Flags().Changed("description") {
						var err error
						proposedDescription, err = cmd.Flags().GetString("description")
						if err != nil {
							return err
						}
						// Special case for editor mode
						if proposedDescription == "-" {
							proposedDescription = "(from editor)"
						}
					} else {
						proposedDescription = body // from autofill
					}

					writeUpdatePreview(f.IO().StdOut, proposedTitle, proposedDescription)

					action, err := confirmUpdateSurvey(cmd.Context(), f)
					if err != nil {
						// iostreams.Run already prints "Cancelled." for user cancellation
						if errors.Is(err, iostreams.ErrUserCancelled) {
							return nil
						}
						return err
					}

					if action == cmdutils.CancelAction {
						f.IO().LogInfo("Cancelled.")
						return nil
					}
				}

				// Only set title and body if not explicitly provided by user
				if !cmd.Flags().Changed("title") {
					mergeTitle = title
					actions = append(actions, "updated title with commit info")
				}
				if !cmd.Flags().Changed("description") {
					mergeBody = body
					actions = append(actions, "updated body with commit info")
				}
			}

			isDraft, _ := cmd.Flags().GetBool("draft")
			isWIP, _ := cmd.Flags().GetBool("wip")
			if m, _ := cmd.Flags().GetString("title"); m != "" {
				actions = append(actions, fmt.Sprintf("updated title to %q", m))
				mergeTitle = m
			}
			if mergeTitle == "" {
				mergeTitle = mr.Title
			}
			if isDraft || isWIP {
				if isDraft {
					if strings.HasPrefix(strings.ToLower(mergeTitle), "draft:") {
						actions = append(actions, "already a Draft")
					} else {
						actions = append(actions, "marked as Draft")
						mergeTitle = "Draft: " + mergeTitle
					}
				} else {
					if strings.HasPrefix(strings.ToLower(mergeTitle), "wip:") {
						actions = append(actions, "already a WIP")
					} else {
						actions = append(actions, "marked as WIP")
						mergeTitle = "WIP: " + mergeTitle
					}
				}
			} else if isReady, _ := cmd.Flags().GetBool("ready"); isReady {
				actions = append(actions, "marked as ready")
				re := regexp.MustCompile(`(?i)^(\s*(?:draft:|wip:)\s*)*`)
				mergeTitle = re.ReplaceAllString(mergeTitle, "")
			}

			l.Title = new(mergeTitle)
			if m, _ := cmd.Flags().GetBool("lock-discussion"); m {
				actions = append(actions, "locked discussion")
				l.DiscussionLocked = new(m)
			}
			if m, _ := cmd.Flags().GetBool("unlock-discussion"); m {
				actions = append(actions, "unlocked discussion")
				l.DiscussionLocked = new(false)
			}

			if m, _ := cmd.Flags().GetString("description"); m != "" {
				actions = append(actions, "updated body")

				// Edit the body via editor
				if m == "-" {
					editor, err := cmdutils.GetEditor(f.Config)
					if err != nil {
						return err
					}

					l.Description = new("")
					err = cmdutils.EditorPrompt(cmd.Context(), f.IO(), l.Description, "Body", mr.Description, editor)
					if err != nil {
						return err
					}
				} else {
					l.Description = new(m)
				}
			} else if mergeBody != "" {
				// Use autofilled body if available and no explicit body provided
				l.Description = new(mergeBody)
			}

			if m, _ := cmd.Flags().GetStringSlice("label"); len(m) != 0 {
				actions = append(actions, fmt.Sprintf("added labels %s", strings.Join(m, " ")))
				l.AddLabels = (*gitlab.LabelOptions)(&m)
			}
			if m, _ := cmd.Flags().GetStringSlice("unlabel"); len(m) != 0 {
				actions = append(actions, fmt.Sprintf("removed labels %s", strings.Join(m, " ")))
				l.RemoveLabels = (*gitlab.LabelOptions)(&m)
			}
			if m, _ := cmd.Flags().GetString("target-branch"); m != "" {
				actions = append(actions, fmt.Sprintf("set target branch to %q", m))
				l.TargetBranch = new(m)
			}
			if ok := cmd.Flags().Changed("milestone"); ok {
				if m, _ := cmd.Flags().GetString("milestone"); m != "" || m == "0" {
					mID, err := cmdutils.ParseMilestone(client, repo, m)
					if err != nil {
						return err
					}
					actions = append(actions, fmt.Sprintf("added milestone %q", m))
					l.MilestoneID = new(mID)
				} else {
					// Unassign the Milestone
					actions = append(actions, "unassigned milestone")
					l.MilestoneID = new(int64(0))
				}
			}
			if cmd.Flags().Changed("unassign") {
				l.AssigneeIDs = &[]int64{0} // 0 or an empty int[] is the documented way to unassign
				actions = append(actions, "unassigned all users")
			}
			if ua != nil {
				if len(ua.ToReplace) != 0 {
					l.AssigneeIDs, actions, err = ua.UsersFromReplaces(client, actions)
					if err != nil {
						return err
					}
				} else if len(ua.ToAdd) != 0 || len(ua.ToRemove) != 0 {
					l.AssigneeIDs, actions, err = ua.UsersFromAddRemove(nil, mr.Assignees, client, actions)
					if err != nil {
						return err
					}
				}
			}

			if ur != nil {
				if len(ur.ToReplace) != 0 {
					l.ReviewerIDs, actions, err = ur.UsersFromReplaces(client, actions)
					if err != nil {
						return err
					}
				} else if len(ur.ToAdd) != 0 || len(ur.ToRemove) != 0 {
					l.ReviewerIDs, actions, err = ur.UsersFromAddRemove(nil, mr.Reviewers, client, actions)
					if err != nil {
						return err
					}
				}
			}

			if removeSource, _ := cmd.Flags().GetBool("remove-source-branch"); removeSource {

				if mr.ForceRemoveSourceBranch {
					actions = append(actions, "disabled removal of source branch on merge.")
				} else {
					actions = append(actions, "enabled removal of source branch on merge.")
				}

				l.RemoveSourceBranch = new(!mr.ForceRemoveSourceBranch)
			}

			if squashBeforeMerge, _ := cmd.Flags().GetBool("squash-before-merge"); squashBeforeMerge {

				if mr.Squash {
					actions = append(actions, "disabled squashing of commits before merge.")
				} else {
					actions = append(actions, "enabled squashing of commits before merge.")
				}

				l.Squash = new(!mr.Squash)
			}

			f.IO().LogInfof("- Updating merge request !%d\n", mr.IID)

			mr, err = api.UpdateMR(client, repo.FullName(), mr.IID, l)
			if err != nil {
				return err
			}

			for _, s := range actions {
				f.IO().LogInfof("%s %s\n", c.GreenCheck(), s)
			}

			f.IO().LogInfo(mrutils.DisplayMR(c, &mr.BasicMergeRequest, f.IO().IsaTTY))
			return nil
		},
	}

	mrUpdateCmd.Flags().BoolP("draft", "", false, "Mark merge request as a draft.")
	mrUpdateCmd.Flags().BoolP("ready", "r", false, "Mark merge request as ready to be reviewed and merged.")
	mrUpdateCmd.Flags().BoolP("wip", "", false, "Mark merge request as a work in progress. Alternative to --draft.")
	mrUpdateCmd.Flags().StringP("title", "t", "", "Title of merge request.")
	mrUpdateCmd.Flags().BoolP("lock-discussion", "", false, "Lock discussion on merge request.")
	mrUpdateCmd.Flags().BoolP("unlock-discussion", "", false, "Unlock discussion on merge request.")
	mrUpdateCmd.Flags().StringP("description", "d", "", "Merge request description. Set to \"-\" to open an editor.")
	mrUpdateCmd.Flags().StringSliceP("label", "l", []string{}, "Add labels.")
	mrUpdateCmd.Flags().StringSliceP("unlabel", "u", []string{}, "Remove labels.")
	mrUpdateCmd.Flags().
		StringSliceP("assignee", "a", []string{}, "Assign users via username. Prefix with '!' or '-' to remove from existing assignees, '+' to add. Otherwise, replace existing assignees with given users. Multiple usernames can be comma-separated or specified by repeating the flag.")
	mrUpdateCmd.Flags().
		StringSliceP("reviewer", "", []string{}, "Request review from users by their usernames. Prefix with '!' or '-' to remove from existing reviewers, '+' to add. Otherwise, replace existing reviewers with given users. Multiple usernames can be comma-separated or specified by repeating the flag.")
	mrUpdateCmd.Flags().Bool("unassign", false, "Unassign all users.")
	mrUpdateCmd.Flags().
		BoolP("squash-before-merge", "", false, "Toggles the option to squash commits into a single commit when merging.")
	mrUpdateCmd.Flags().BoolP("remove-source-branch", "", false, "Toggles the removal of the source branch on merge.")
	mrUpdateCmd.Flags().StringP("milestone", "m", "", "Title of the milestone to assign. Set to \"\" or 0 to unassign.")
	mrUpdateCmd.Flags().String("target-branch", "", "Set target branch.")

	// Add new autofill flags
	mrUpdateCmd.Flags().BoolP("fill", "f", false, "Do not prompt for title or body, and just use commit info.")
	mrUpdateCmd.Flags().Bool("fill-commit-body", false, "Fill body with each commit body when multiple commits. Can only be used with --fill.")
	mrUpdateCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt.")
	cmdutils.AddDescriptionFileFlag(mrUpdateCmd, "merge request")

	return mrUpdateCmd
}

// writeUpdatePreview prints the proposed title and description to w before asking for confirmation.
// Multi-line values are indented so continuation lines align with the first line of content.
func writeUpdatePreview(w io.Writer, title, description string) {
	fmt.Fprintf(w, "\nProposed changes:\n") //nolint:forbidigo // w is a generic io.Writer, exercised directly with a strings.Builder in tests
	if title != "" {
		lines := strings.Split(title, "\n")
		fmt.Fprintf(w, "  Title: %s\n", lines[0]) //nolint:forbidigo // w is a generic io.Writer, exercised directly with a strings.Builder in tests
		for _, line := range lines[1:] {
			fmt.Fprintf(w, "         %s\n", line) //nolint:forbidigo // w is a generic io.Writer, exercised directly with a strings.Builder in tests
		}
	}
	if description != "" {
		lines := strings.Split(description, "\n")
		fmt.Fprintf(w, "  Description: %s\n", lines[0]) //nolint:forbidigo // w is a generic io.Writer, exercised directly with a strings.Builder in tests
		for _, line := range lines[1:] {
			fmt.Fprintf(w, "              %s\n", line) //nolint:forbidigo // w is a generic io.Writer, exercised directly with a strings.Builder in tests
		}
	}
	fmt.Fprintf(w, "\n") //nolint:forbidigo // w is a generic io.Writer, exercised directly with a strings.Builder in tests
}

func confirmUpdateSurvey(ctx context.Context, f cmdutils.Factory) (cmdutils.Action, error) {
	shouldProceed := false // default value

	confirm := huh.NewConfirm().
		Title("What would you like to do?").
		Affirmative("Proceed with changes").
		Negative("Cancel").
		Value(&shouldProceed)

	err := f.IO().Run(ctx, confirm)
	if err != nil {
		return cmdutils.CancelAction, fmt.Errorf("could not prompt: %w", err)
	}

	if shouldProceed {
		return cmdutils.SubmitAction, nil
	}
	return cmdutils.CancelAction, nil
}
