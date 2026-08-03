package mrutils

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sync/errgroup"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/tableprinter"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

type MRCheckErrOptions struct {
	// Draft: check and return err if merge request is a DRAFT
	Draft bool
	// Closed: check and return err if merge request is closed
	Closed bool
	// Merged: check and return err if merge request is already merged
	Merged bool
	// Opened: check and return err if merge request is already opened
	Opened bool
	// Conflict: check and return err if there are merge conflicts
	Conflict bool
	// PipelineStatus: check and return err pipeline did not succeed and it is required before merging
	PipelineStatus bool
	// MergePermitted: check and return err if user is not authorized to merge
	MergePermitted bool
	// Subscribed: check and return err if user is already subscribed to MR
	Subscribed bool
	// Unsubscribed: check and return err if user is already unsubscribed to MR
	Unsubscribed bool
	// MergePrivilege: check and return err if user is not authorized to merge
	MergePrivilege bool
}

type MrOptions struct {
	BaseRepo      glrepo.Interface
	Branch        string
	State         string
	PromptEnabled bool
}

var multipleMRSelectQuestion = "Multiple merge requests exist for this branch. Select one:"

// MRCheckErrors checks and return merge request errors specified in MRCheckErrOptions{}
func MRCheckErrors(mr *gitlab.MergeRequest, err MRCheckErrOptions) error {
	if mr.Draft && err.Draft {
		return fmt.Errorf("this merge request is still a draft; run `glab mr update %d --ready` to mark it as ready for review", mr.IID)
	}

	dbg.Debug("MergeWhenPipelineSucceeds:", strconv.FormatBool(mr.MergeWhenPipelineSucceeds))
	dbg.Debug("DetailedMergeStatus:", mr.DetailedMergeStatus)

	if mr.DetailedMergeStatus == "ci_must_pass" && err.PipelineStatus {
		return fmt.Errorf("this merge request requires a passing pipeline before merging")
	}

	if mr.MergeWhenPipelineSucceeds && err.PipelineStatus && mr.Pipeline != nil {
		if mr.Pipeline.Status != "success" {
			return fmt.Errorf("the pipeline for this merge request has failed; it must succeed before merging")
		}
	}

	if mr.State == "merged" && err.Merged {
		return fmt.Errorf("this merge request has already been merged")
	}

	if mr.State == "closed" && err.Closed {
		return fmt.Errorf("this merge request has been closed")
	}

	if mr.State == "opened" && err.Opened {
		return fmt.Errorf("this merge request is already open")
	}

	if mr.Subscribed && err.Subscribed {
		return fmt.Errorf("you are already subscribed to this merge request")
	}

	if !mr.Subscribed && err.Unsubscribed {
		return fmt.Errorf("you are not subscribed to this merge request")
	}

	if err.MergePrivilege && !mr.User.CanMerge {
		return fmt.Errorf("you do not have permission to merge this merge request")
	}

	if err.Conflict && mr.HasConflicts {
		return fmt.Errorf("merge conflicts exist; resolve the conflicts and try again, or merge locally")
	}

	return nil
}

func DisplayMR(c *iostreams.ColorPalette, mr *gitlab.BasicMergeRequest, isTTY bool) string {
	mrID := MRState(c, mr)
	if isTTY {
		return fmt.Sprintf("%s %s (%s)\n %s\n", mrID, mr.Title, mr.SourceBranch, mr.WebURL)
	} else {
		return mr.WebURL
	}
}

func MRState(c *iostreams.ColorPalette, m *gitlab.BasicMergeRequest) string {
	switch m.State {
	case "opened":
		return c.Green(fmt.Sprintf("!%d", m.IID))
	case "merged":
		return c.Magenta(fmt.Sprintf("!%d", m.IID))
	default:
		return c.Red(fmt.Sprintf("!%d", m.IID))
	}
}

func DisplayAllMRs(streams *iostreams.IOStreams, mrs []*gitlab.BasicMergeRequest) string {
	c := streams.Color()
	table := tableprinter.NewTablePrinter()
	table.SetIsTTY(streams.IsOutputTTY())
	for _, m := range mrs {
		table.AddCell(streams.Hyperlink(MRState(c, m), m.WebURL))
		table.AddCell(m.References.Full)
		table.AddCell(m.Title)
		table.AddCell(c.Cyan(fmt.Sprintf("(%s) ← (%s)", m.TargetBranch, m.SourceBranch)))
		table.EndRow()
	}

	return table.Render()
}

// MRFromArgs is wrapper around MRFromArgsWithOpts without any custom options
func MRFromArgs(ctx context.Context, f cmdutils.Factory, args []string, state string) (*gitlab.MergeRequest, glrepo.Interface, error) {
	return MRFromArgsWithOpts(ctx, f, args, &gitlab.GetMergeRequestsOptions{}, state)
}

// mrTarget is a merge request identified from command-line arguments, before
// the merge request itself has been fetched from the API. Exactly one of mrID
// and branch is set: mrID when the argument was an ID or a merge request URL,
// branch otherwise.
type mrTarget struct {
	client   *gitlab.Client
	baseRepo glrepo.Interface
	mrID     int
	branch   string
}

// resolveMRTarget interprets the command-line arguments without calling the API.
// It falls back to the current branch when no argument is given.
func resolveMRTarget(f cmdutils.Factory, args []string) (*mrTarget, error) {
	var mrID int

	client, err := f.GitLabClient()
	if err != nil {
		return nil, err
	}

	baseRepo, err := f.BaseRepo()
	if err != nil {
		return nil, err
	}

	var branch string

	if len(args) > 0 {
		// First, try to parse as a GitLab MR URL
		if urlMRID, urlRepo := cmdutils.ParseMergeRequestFromURL(args[0], f.DefaultHostname(), f.Config()); urlMRID != 0 {
			mrID = urlMRID
			// Update client to use the hostname from the URL if it's different
			if urlRepo.RepoHost() != baseRepo.RepoHost() {
				apiClient, err := f.ApiClient(urlRepo.RepoHost())
				if err != nil {
					return nil, fmt.Errorf("failed to connect to GitLab instance %s from URL (%s): %w", urlRepo.RepoHost(), args[0], err)
				}
				client = apiClient.Lab()
			}
			baseRepo = urlRepo
		} else {
			// If not a URL, try to parse as an integer (with optional ! prefix)
			mrID, err = strconv.Atoi(strings.TrimPrefix(args[0], "!"))
			if err != nil {
				branch = args[0]
			} else if mrID == 0 { // to check for cases where the user explicitly specified mrID to be zero
				return nil, fmt.Errorf("invalid merge request ID provided")
			}
		}
	}

	if branch == "" && mrID == 0 {
		branch, err = f.Branch()
		if err != nil {
			return nil, err
		}
	}

	return &mrTarget{client: client, baseRepo: baseRepo, mrID: mrID, branch: branch}, nil
}

// MRFromArgsWithOpts gets MR with custom request options passed down to it
func MRFromArgsWithOpts(
	ctx context.Context,
	f cmdutils.Factory,
	args []string,
	opts *gitlab.GetMergeRequestsOptions,
	state string,
) (*gitlab.MergeRequest, glrepo.Interface, error) {
	target, err := resolveMRTarget(f, args)
	if err != nil {
		return nil, nil, err
	}

	mrID := target.mrID
	if mrID == 0 {
		basicMR, err := target.mrForBranch(ctx, f.IO(), state)
		if err != nil {
			return nil, nil, err
		}
		mrID = int(basicMR.IID)
	}
	mr, err := api.GetMR(target.client, target.baseRepo.FullName(), int64(mrID), opts)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get merge request %d: %w", mrID, err)
	}

	return mr, target.baseRepo, nil
}

// MRWebURLFromArgs resolves the web URL of a merge request using as few API
// calls as possible. When the merge request is identified by branch, the URL is
// taken from the list response instead of fetching the merge request again.
func MRWebURLFromArgs(ctx context.Context, f cmdutils.Factory, args []string, state string) (string, glrepo.Interface, error) {
	target, err := resolveMRTarget(f, args)
	if err != nil {
		return "", nil, err
	}

	if target.mrID == 0 {
		basicMR, err := target.mrForBranch(ctx, f.IO(), state)
		if err != nil {
			return "", nil, err
		}
		return basicMR.WebURL, target.baseRepo, nil
	}

	mr, err := api.GetMR(target.client, target.baseRepo.FullName(), int64(target.mrID), &gitlab.GetMergeRequestsOptions{})
	if err != nil {
		return "", nil, fmt.Errorf("failed to get merge request %d: %w", target.mrID, err)
	}
	return mr.WebURL, target.baseRepo, nil
}

func (t *mrTarget) mrForBranch(ctx context.Context, io *iostreams.IOStreams, state string) (*gitlab.BasicMergeRequest, error) {
	return GetMRForBranch(ctx, io, t.client, MrOptions{t.baseRepo, t.branch, state, io.PromptEnabled()})
}

func MRsFromArgs(ctx context.Context, f cmdutils.Factory, args []string, state string) ([]*gitlab.MergeRequest, glrepo.Interface, error) {
	if len(args) <= 1 {
		var arrIDs []string
		if len(args) == 1 {
			arrIDs = strings.Split(args[0], ",")
		}
		if len(arrIDs) <= 1 {
			// If there are no args then try to auto-detect from the branch name
			mr, baseRepo, err := MRFromArgs(ctx, f, args, state)
			if err != nil {
				return nil, nil, err
			}
			return []*gitlab.MergeRequest{mr}, baseRepo, nil
		}
		args = arrIDs
	}

	baseRepo, err := f.BaseRepo()
	if err != nil {
		return nil, nil, err
	}

	errGroup, ctx := errgroup.WithContext(ctx)
	mrs := make([]*gitlab.MergeRequest, len(args))
	for i, arg := range args {
		errGroup.Go(func() error {
			// fetching multiple MRs does not return many major params in the payload
			// so we fetch again using the single mr endpoint
			mr, _, err := MRFromArgs(ctx, f, []string{arg}, state)
			if err != nil {
				return err
			}
			mrs[i] = mr
			return nil
		})
	}
	if err := errGroup.Wait(); err != nil {
		return nil, nil, err
	}
	return mrs, baseRepo, nil
}

func resolveOwnerAndBranch(potentialBranch string) (string, string) {
	split := strings.Split(potentialBranch, ":")
	userUsedOwnerColonBranchFormat := len(split) != 1
	if userUsedOwnerColonBranchFormat {
		owner, branch := split[0], split[1]
		return owner, branch
	}
	owner, branch := "", split[0]
	return owner, branch
}

var GetMRForBranch = func(ctx context.Context, ios *iostreams.IOStreams, apiClient *gitlab.Client, mrOpts MrOptions) (*gitlab.BasicMergeRequest, error) {
	owner, currentBranch := resolveOwnerAndBranch(mrOpts.Branch)

	opts := gitlab.ListProjectMergeRequestsOptions{
		SourceBranch: new(currentBranch),
	}

	userAskedForSpecificState := mrOpts.State != "" && mrOpts.State != "any"
	if userAskedForSpecificState {
		opts.State = new(mrOpts.State)
	}

	mrs, err := api.ListMRs(apiClient, mrOpts.BaseRepo.FullName(), &opts)
	if err != nil {
		return nil, fmt.Errorf("failed to get open merge request for %q: %w", currentBranch, err)
	}

	if len(mrs) == 0 {
		return nil, fmt.Errorf("no open merge request available for %q", currentBranch)
	}

	userAskedForSpecificOwner := owner != ""
	if userAskedForSpecificOwner {
		for i := range mrs {
			mr := mrs[i]
			matchFound := mr.Author.Username == owner
			if matchFound {
				return mr, nil
			}
		}
		return nil, fmt.Errorf("no open merge request available for %q owned by @%s", currentBranch, owner)
	}

	// This is done after the 'OWNER:' check because we don't want to give the wrong MR
	// to someone that **explicitly** asked for a OWNER.
	if len(mrs) == 1 {
		return mrs[0], nil
	}

	// No 'OWNER:' prompt the user to pick a merge request
	mrMap := map[string]*gitlab.BasicMergeRequest{}
	var mrNames []string
	for i := range mrs {
		t := fmt.Sprintf("!%d (%s) by @%s", mrs[i].IID, currentBranch, mrs[i].Author.Username)
		mrMap[t] = mrs[i]
		mrNames = append(mrNames, t)
	}
	pickedMR := mrNames[0]

	if !mrOpts.PromptEnabled {
		// NO_PROMPT environment variable is set. Skip prompt and return error when multiple merge requests exist for branch.
		err = fmt.Errorf("merge request ID number required. Possible matches:\n\n%s", strings.Join(mrNames, "\n"))
	} else {
		err = ios.Select(ctx, &pickedMR, multipleMRSelectQuestion, mrNames)
	}
	if err != nil {
		return nil, fmt.Errorf("you must select a merge request: %w", err)
	}

	return mrMap[pickedMR], nil
}

func RebaseMR(ios *iostreams.IOStreams, apiClient *gitlab.Client, repo glrepo.Interface, mr *gitlab.MergeRequest, rebaseOpts *gitlab.RebaseMergeRequestOptions) error {
	ios.StartSpinner("Sending rebase request...")
	_, err := apiClient.MergeRequests.RebaseMergeRequest(repo.FullName(), mr.IID, rebaseOpts)
	if err != nil {
		return err
	}
	ios.StopSpinner("")

	opts := &gitlab.GetMergeRequestsOptions{}
	opts.IncludeRebaseInProgress = new(true)
	ios.StartSpinner("Checking rebase status...")
	errorMSG := ""
	i := 0
	for {
		mr, err := api.GetMR(apiClient, repo.FullName(), mr.IID, opts)
		if err != nil {
			errorMSG = err.Error()
			break
		}
		if i == 0 {
			ios.StopSpinner("")
			ios.StartSpinner("Rebase in progress...")
		}
		if !mr.RebaseInProgress {
			if mr.MergeError != "" && mr.MergeError != "null" {
				errorMSG = mr.MergeError
			}
			break
		}
		i++
	}
	ios.StopSpinner("")
	if errorMSG != "" {
		return errors.New(errorMSG)
	}
	ios.LogInfof("%s %s\n", ios.Color().GreenCheck(), "Rebase successful!")
	return nil
}

// PrintMRApprovalState renders an output to summarize the approval state of a merge request
func PrintMRApprovalState(ios *iostreams.IOStreams, mrApprovals *gitlab.MergeRequestApprovalState) {
	const approvedIcon = "👍"

	c := ios.Color()

	if mrApprovals.ApprovalRulesOverwritten {
		ios.LogInfo(c.Yellow("Approval rules overwritten."))
	}
	for _, rule := range mrApprovals.Rules {
		table := tableprinter.NewTablePrinter()
		if rule.Approved {
			ios.LogInfo(c.Green(fmt.Sprintf("Rule %q sufficient approvals (%d/%d required):", rule.Name, len(rule.ApprovedBy), rule.ApprovalsRequired)))
		} else {
			ios.LogInfo(c.Yellow(fmt.Sprintf("Rule %q insufficient approvals (%d/%d required):", rule.Name, len(rule.ApprovedBy), rule.ApprovalsRequired)))
		}

		eligibleApprovers := rule.EligibleApprovers

		approvedBy := map[string]*gitlab.BasicUser{}
		for _, by := range rule.ApprovedBy {
			approvedBy[by.Username] = by
		}

		table.AddRow("Name", "Username", "Approved")
		for _, eligibleApprover := range eligibleApprovers {
			approved := "-"
			source := ""
			if _, exists := approvedBy[eligibleApprover.Username]; exists {
				approved = approvedIcon
			}
			if rule.SourceRule != nil {
				source = rule.SourceRule.RuleType
			}
			table.AddRow(eligibleApprover.Name, eligibleApprover.Username, approved, source)
			delete(approvedBy, eligibleApprover.Username)
		}

		// sort all usernames to ensure consistent output
		approverNames := make([]string, 0, len(approvedBy))
		for name := range approvedBy {
			approverNames = append(approverNames, name)
		}
		sort.Strings(approverNames)

		for _, name := range approverNames {
			approver := approvedBy[name]
			table.AddRow(approver.Name, approver.Username, approvedIcon, "")
		}
		ios.LogInfo(table)
	}
}

// AutofillMRFromCommits generates title and body from commit information between two branches
func AutofillMRFromCommits(targetBranch, sourceBranch string, fillCommitBody bool) (string, string, error) {
	commits, err := git.Commits(targetBranch, sourceBranch)
	if err != nil {
		// If git fails, create a simple title from branch name
		return utils.Humanize(sourceBranch), "", nil //nolint:nilerr // intentional fallback: branch-name title when git history is unavailable
	}

	return GenerateMRTitleAndBody(commits, sourceBranch, fillCommitBody)
}

// GenerateMRTitleAndBody creates title and body from commits
func GenerateMRTitleAndBody(commits []*git.Commit, sourceBranch string, fillCommitBody bool) (string /* title */, string /* body */, error) {
	switch len(commits) {
	case 0:
		// No commits found, use branch name as fallback title
		return utils.Humanize(sourceBranch), "", nil
	case 1:
		// Single commit: use commit title as MR title
		title := commits[0].Title

		// For single commit, always try to get the commit body (consistent with original behavior)
		commitBody, err := git.CommitBody(commits[0].Sha)
		if err != nil {
			// If we can't get the commit body, fall back to the commit title
			return title, commits[0].Title, nil //nolint:nilerr // intentional fallback: commit title as body when CommitBody fails
		}

		if strings.TrimSpace(commitBody) != "" {
			return title, commitBody, nil
		}

		return title, commits[0].Title, nil
	default:
		// Multiple commits: use humanized branch name as fallback title
		title := utils.Humanize(sourceBranch)

		// Generate body from commit list
		body, err := GenerateMRCommitListBody(commits, fillCommitBody)
		if err != nil {
			return "", "", err
		}

		return title, body, nil
	}
}

// GenerateMRCommitListBody creates a markdown list of commits with optional commit bodies
func GenerateMRCommitListBody(commits []*git.Commit, fillCommitBody bool) (string, error) {
	var body strings.Builder
	re := regexp.MustCompile(`\r?\n\n`)

	for _, commit := range slices.Backward(commits) {
		// adds 2 spaces for markdown line wrapping
		fmt.Fprintf(&body, "- %s  \n", commit.Title) //nolint:forbidigo // writing to a strings.Builder, not stdout/stderr

		if fillCommitBody {
			commitBody, err := git.CommitBody(commit.Sha)
			if err != nil {
				return "", fmt.Errorf("failed to get commit message for %s: %w", commit.Sha, err)
			}

			if strings.TrimSpace(commitBody) != "" {
				commitBody = re.ReplaceAllString(commitBody, "  \n")
				fmt.Fprintf(&body, "%s\n", commitBody) //nolint:forbidigo // writing to a strings.Builder, not stdout/stderr
			} else {
				// Add extra newline when commit has no body to match original behavior
				fmt.Fprintf(&body, "\n") //nolint:forbidigo // writing to a strings.Builder, not stdout/stderr
			}
		}
	}

	return body.String(), nil
}
