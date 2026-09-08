package prune

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	dryRun          bool
	yes             bool
	excludePatterns []string
	useMergedFlag   bool

	io           *iostreams.IOStreams
	gitLabClient func() (*gitlab.Client, error)
	baseRepo     func() (glrepo.Interface, error)
	branch       func() (string, error)
	gitRunner    git.GitRunner
}

type candidate struct {
	branch       string
	mrIID        int64
	targetBranch string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitLabClient: f.GitLabClient,
		baseRepo:     f.BaseRepo,
		branch:       f.Branch,
		gitRunner:    f.GitRunner(),
	}

	cmd := &cobra.Command{
		Use:   "prune [flags]",
		Short: "Delete local Git branches whose merge request has been merged.",
		Long: heredoc.Docf(`
			Delete local Git branches whose merge request has been merged on GitLab.

			By default, the command queries GitLab for each local branch and only
			deletes branches that have at least one merged merge request and no
			merge requests still open from the same source branch.

			Protected branches, the default branch, and the currently checked-out
			branch are never deleted.

			This command only affects your local Git repository. Remote branches
			on GitLab are not touched.

			Use --merged to skip the per-branch merge request lookup and instead
			rely on %[1]sgit branch --merged%[1]s to decide which branches to delete.
			The default branch and protected branches are still fetched from
			GitLab in this mode. Falling back to Git is faster, but only detects
			fast-forward merges — squash and rebase merges look like distinct
			commits to Git and will not be reported as merged.
		`, "`"),
		Example: heredoc.Doc(`
			# Preview branches that would be deleted
			glab repo prune --dry-run

			# Delete branches with merged MRs (after confirmation)
			glab repo prune

			# Delete without confirmation
			glab repo prune --yes

			# Exclude additional branches by name or glob pattern
			glab repo prune --exclude wip-*,demo-branch

			# Detect merged branches with Git instead of GitLab (faster, but misses squash and rebase merges)
			glab repo prune --merged
		`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := opts.validate(); err != nil {
				return err
			}
			return opts.run(cmd.Context())
		},
	}

	fl := cmd.Flags()
	fl.BoolVar(&opts.dryRun, "dry-run", false, "Preview branches that would be deleted without deleting them.")
	fl.BoolVarP(&opts.yes, "yes", "y", false, "Skip the confirmation prompt.")
	fl.StringSliceVarP(&opts.excludePatterns, "exclude", "e", nil, "Branch name or glob pattern to exclude. Comma-separated or repeated.")
	fl.BoolVar(&opts.useMergedFlag, "merged", false, "Use 'git branch --merged' instead of querying GitLab. Detects fast-forward merges only.")

	return cmd
}

func (o *options) validate() error {
	if !o.yes && !o.io.PromptEnabled() && !o.dryRun {
		return &cmdutils.FlagError{Err: errors.New("--yes or -y is required when not running interactively")}
	}
	return nil
}

func (o *options) run(ctx context.Context) error {
	repo, err := o.baseRepo()
	if err != nil {
		return err
	}

	currentBranch, err := o.branch()
	if err != nil && !errors.Is(err, git.ErrNotOnAnyBranch) {
		return err
	}

	apiClient, err := o.gitLabClient()
	if err != nil {
		return err
	}

	project, _, err := apiClient.Projects.GetProject(repo.FullName(), nil, gitlab.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("could not fetch project from GitLab: %w", err)
	}
	defaultBranch := project.DefaultBranch
	if defaultBranch == "" {
		defaultBranch = git.DefaultBranchName
	}

	protectedPatterns, err := listProtectedBranchNames(ctx, apiClient, repo.FullName())
	if err != nil {
		return fmt.Errorf("could not fetch protected branches from GitLab: %w", err)
	}

	allBranches, err := git.ListLocalBranches(o.gitRunner)
	if err != nil {
		return err
	}

	excluded := buildMatcher(defaultBranch, currentBranch, protectedPatterns, o.excludePatterns)

	candidateBranches := make([]string, 0, len(allBranches))
	for _, b := range allBranches {
		if !excluded(b) {
			candidateBranches = append(candidateBranches, b)
		}
	}

	var candidates []candidate
	if o.useMergedFlag {
		candidates, err = collectMergedLocally(o.gitRunner, defaultBranch, candidateBranches)
	} else {
		candidates, err = collectMergedViaAPI(ctx, o.io, apiClient, repo.FullName(), candidateBranches)
	}
	if err != nil {
		return err
	}

	c := o.io.Color()
	emptyMsg := "No local branches found with merged merge requests."
	header := fmt.Sprintf("Found %d branch(es) with merged merge requests:", len(candidates))
	if o.useMergedFlag {
		emptyMsg = fmt.Sprintf("No local branches found merged into %s.", defaultBranch)
		header = fmt.Sprintf("Found %d branch(es) merged into %s:", len(candidates), defaultBranch)
	}

	if len(candidates) == 0 {
		o.io.LogInfof("%s %s\n", c.GreenCheck(), emptyMsg)
		return nil
	}

	o.io.LogInfo(header)
	for _, cand := range candidates {
		switch {
		case cand.mrIID != 0:
			o.io.LogInfof("  %s %s (MR !%d → %s)\n", c.GreenCheck(), cand.branch, cand.mrIID, cand.targetBranch)
		default:
			o.io.LogInfof("  %s %s (merged into %s)\n", c.GreenCheck(), cand.branch, cand.targetBranch)
		}
	}

	if o.dryRun {
		o.io.LogInfo()
		o.io.LogInfo("Dry run: no branches were deleted.")
		return nil
	}

	if !o.yes {
		o.io.LogError()
		confirmed := false
		if err := o.io.Confirm(ctx, &confirmed, fmt.Sprintf("Delete these %d local branch(es)?", len(candidates))); err != nil {
			return err
		}
		if !confirmed {
			o.io.LogError("aborted by user")
			return nil
		}
	}

	o.io.LogInfo()
	deleted := 0
	for _, cand := range candidates {
		if err := git.DeleteLocalBranch(cand.branch, o.gitRunner); err != nil {
			o.io.LogInfof("  %s %s: %s\n", c.FailedIcon(), cand.branch, err)
			continue
		}
		o.io.LogInfof("  %s deleted %s\n", c.GreenCheck(), cand.branch)
		deleted++
	}
	o.io.LogInfof("\n%d branch(es) deleted.\n", deleted)
	return nil
}

func collectMergedViaAPI(ctx context.Context, io *iostreams.IOStreams, client *gitlab.Client, projectID string, branches []string) ([]candidate, error) {
	if len(branches) == 0 {
		return nil, nil
	}

	io.StartSpinner("Checking %d local branch(es) against GitLab...", len(branches))
	defer io.StopSpinner("")

	var candidates []candidate
	for _, b := range branches {
		merged, hasOpen, err := scanBranchMRs(ctx, client, projectID, b)
		if err != nil {
			return nil, fmt.Errorf("listing merge requests for branch %q: %w", b, err)
		}
		if merged != nil && !hasOpen {
			candidates = append(candidates, candidate{
				branch:       b,
				mrIID:        merged.IID,
				targetBranch: merged.TargetBranch,
			})
		}
	}
	return candidates, nil
}

// scanBranchMRs walks every page of merge requests with the given source
// branch and reports the first merged MR found, along with whether any
// open/locked MRs exist. Pagination matters here — a reused branch with
// many MRs could otherwise hide an open one on a later page and be
// incorrectly marked safe to delete.
func scanBranchMRs(ctx context.Context, client *gitlab.Client, projectID, branch string) (*gitlab.BasicMergeRequest, bool, error) {
	opts := &gitlab.ListProjectMergeRequestsOptions{
		SourceBranch: new(branch),
	}
	mrs, err := gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.BasicMergeRequest, *gitlab.Response, error) {
		return client.MergeRequests.ListProjectMergeRequests(projectID, opts, p, gitlab.WithContext(ctx))
	})
	if err != nil {
		return nil, false, err
	}

	var merged *gitlab.BasicMergeRequest
	hasOpen := false
	for _, mr := range mrs {
		switch mr.State {
		case "merged":
			if merged == nil {
				merged = mr
			}
		case "opened", "locked":
			hasOpen = true
		}
	}
	return merged, hasOpen, nil
}

func collectMergedLocally(gr git.GitRunner, target string, branches []string) ([]candidate, error) {
	if len(branches) == 0 {
		return nil, nil
	}

	merged, err := git.ListMergedBranches(target, gr)
	if err != nil {
		return nil, err
	}
	mergedSet := make(map[string]struct{}, len(merged))
	for _, b := range merged {
		mergedSet[b] = struct{}{}
	}

	var candidates []candidate
	for _, b := range branches {
		if _, ok := mergedSet[b]; ok {
			candidates = append(candidates, candidate{branch: b, targetBranch: target})
		}
	}
	return candidates, nil
}

func listProtectedBranchNames(ctx context.Context, client *gitlab.Client, projectID string) ([]string, error) {
	opts := &gitlab.ListProtectedBranchesOptions{}
	protected, err := gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.ProtectedBranch, *gitlab.Response, error) {
		return client.ProtectedBranches.ListProtectedBranches(projectID, opts, p, gitlab.WithContext(ctx))
	})
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(protected))
	for _, p := range protected {
		if p != nil && p.Name != "" {
			names = append(names, p.Name)
		}
	}
	return names, nil
}

// buildMatcher returns a predicate that reports whether a branch should be
// excluded from pruning. Pattern semantics match GitLab's protected-branch
// wildcards: `*` matches any character including `/`, `?` matches exactly
// one character. Plain strings match by full equality.
func buildMatcher(defaultBranch, currentBranch string, protected, userPatterns []string) func(string) bool {
	var patterns []string
	if defaultBranch != "" {
		patterns = append(patterns, defaultBranch)
	}
	if currentBranch != "" {
		patterns = append(patterns, currentBranch)
	}
	patterns = append(patterns, protected...)
	for _, raw := range userPatterns {
		for p := range strings.SplitSeq(raw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				patterns = append(patterns, p)
			}
		}
	}

	compiled := make([]*regexp.Regexp, 0, len(patterns))
	literals := make(map[string]struct{}, len(patterns))
	for _, p := range patterns {
		if !strings.ContainsAny(p, "*?") {
			literals[p] = struct{}{}
			continue
		}
		compiled = append(compiled, compileGlob(p))
	}

	return func(branch string) bool {
		if _, ok := literals[branch]; ok {
			return true
		}
		for _, re := range compiled {
			if re.MatchString(branch) {
				return true
			}
		}
		return false
	}
}

// compileGlob converts a GitLab-style wildcard pattern (`*` matches any
// sequence of any characters including `/`, `?` matches exactly one) into
// an anchored regular expression. Every literal character in the input
// goes through regexp.QuoteMeta, so the resulting expression is always
// well-formed — a compile failure here would be a programming error,
// hence MustCompile.
func compileGlob(pattern string) *regexp.Regexp {
	var sb strings.Builder
	sb.WriteString("^")
	for _, r := range pattern {
		switch r {
		case '*':
			sb.WriteString(".*")
		case '?':
			sb.WriteString(".")
		default:
			sb.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	sb.WriteString("$")
	return regexp.MustCompile(sb.String())
}
