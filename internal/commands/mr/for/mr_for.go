package _for

import (
	"fmt"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/mr/mrutils"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

func NewCmdFor(f cmdutils.Factory) *cobra.Command {
	mrForCmd := &cobra.Command{
		Use:   "for",
		Short: `Create a new merge request for an issue.`,
		Long: heredoc.Docf(`
			Creates a branch and opens a merge request linked to the issue.
			Use %[1]s--draft%[1]s to mark the merge request as a draft.
		`, "`"),
		Aliases: []string{"new-for", "create-for", "for-issue"},
		Example: heredoc.Doc(`
			# Create merge request for issue 34
			$ glab mr for 34

			# Create merge request for issue 34 and mark as work in progress
			$ glab mr for 34 --wip

			$ glab mr new-for 34
			$ glab mr create-for 34
		`),
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error

			client, err := f.GitLabClient()
			if err != nil {
				return err
			}

			repo, err := f.BaseRepo()
			if err != nil {
				return err
			}

			issueID := utils.StringToInt(args[0])
			issue, err := api.GetIssue(client, repo.FullName(), int64(issueID))
			if err != nil {
				return err
			}

			var targetBranch string
			if t, _ := cmd.Flags().GetString("target-branch"); t != "" {
				targetBranch = strings.TrimSpace(t)
			} else {
				project, err := api.GetProject(client, repo.FullName())
				if err != nil {
					return fmt.Errorf("error getting project details: %w", err)
				}
				if project.DefaultBranch != "" {
					targetBranch = project.DefaultBranch
				} else {
					targetBranch = git.DefaultBranchName
				}
			}

			sourceBranch := fmt.Sprintf("%d-%s", issue.IID, utils.ReplaceNonAlphaNumericChars(strings.ToLower(issue.Title), "-"))

			lb := &gitlab.CreateBranchOptions{
				Branch: &sourceBranch,
				Ref:    &targetBranch,
			}

			_, _, err = client.Branches.CreateBranch(repo.FullName(), lb)
			if err != nil {
				for branchErr, branchCount := err, 1; branchErr != nil; branchCount++ {

					numberedBranch := fmt.Sprintf("%d-%s-%d", issue.IID, strings.ReplaceAll(strings.ToLower(issue.Title), " ", "-"), branchCount)
					lb = &gitlab.CreateBranchOptions{
						Branch: &numberedBranch,
						Ref:    &targetBranch,
					}
					sourceBranch = numberedBranch
					_, _, branchErr = client.Branches.CreateBranch(repo.FullName(), lb)
					fmt.Println(branchErr)
				}
			}

			var mergeTitle string
			mergeTitle = fmt.Sprintf("Resolve \"%s\"", issue.Title)

			isDraft, _ := cmd.Flags().GetBool("draft")
			isWIP, _ := cmd.Flags().GetBool("wip")
			if isDraft || isWIP {
				if isWIP {
					mergeTitle = "WIP: " + mergeTitle
				} else {
					mergeTitle = "Draft: " + mergeTitle
				}
			}

			mergeLabel, _ := cmd.Flags().GetString("label")

			l := &gitlab.CreateMergeRequestOptions{}
			l.Title = new(mergeTitle)
			l.Description = new(fmt.Sprintf("Closes #%d", issue.IID))
			l.Labels = &gitlab.LabelOptions{mergeLabel}
			l.SourceBranch = new(sourceBranch)
			l.TargetBranch = new(targetBranch)
			if milestone, _ := cmd.Flags().GetInt("milestone"); milestone != -1 {
				l.MilestoneID = new(int64(milestone))
			}
			if allowCol, _ := cmd.Flags().GetBool("allow-collaboration"); allowCol {
				l.AllowCollaboration = new(true)
			}
			if removeSource, _ := cmd.Flags().GetBool("remove-source-branch"); removeSource {
				l.RemoveSourceBranch = new(true)
			}
			if withLabels, _ := cmd.Flags().GetBool("with-labels"); withLabels {
				l.Labels = (*gitlab.LabelOptions)(&issue.Labels)
			}

			if a, _ := cmd.Flags().GetString("assignee"); a != "" {
				arrIds := strings.Split(strings.Trim(a, "[] "), ",")
				var t2 []int64

				for _, i := range arrIds {
					j := utils.StringToInt(i)
					t2 = append(t2, int64(j))
				}
				l.AssigneeIDs = &t2
			}

			mr, _, err := client.MergeRequests.CreateMergeRequest(repo.FullName(), l)
			if err != nil {
				return err
			}

			f.IO().LogInfo(mrutils.DisplayMR(f.IO().Color(), &mr.BasicMergeRequest, f.IO().IsaTTY))

			return nil
		},
	}

	mrForCmd.Flags().BoolP("draft", "", true, "Mark merge request as a draft.")
	mrForCmd.Flags().BoolP("wip", "", false, "Mark merge request as a work in progress. Overrides --draft.")
	mrForCmd.Flags().StringP("label", "l", "", "Add label by name. Multiple labels should be comma-separated.")
	mrForCmd.Flags().StringP("assignee", "a", "", "Assign merge request to people by their IDs. Multiple values should be comma-separated.")
	mrForCmd.Flags().BoolP("allow-collaboration", "", false, "Allow commits from other members.")
	mrForCmd.Flags().BoolP("remove-source-branch", "", false, "Remove source branch on merge.")
	mrForCmd.Flags().IntP("milestone", "m", -1, "Add milestone by <id> for this merge request.")
	mrForCmd.Flags().StringP("target-branch", "b", "", "The target or base branch into which you want your code merged.")
	mrForCmd.Flags().BoolP("with-labels", "", false, "Copy labels from issue to the merge request.")

	mrForCmd.Deprecated = "use `glab mr create --related-issue <issueID>`."

	return mrForCmd
}
