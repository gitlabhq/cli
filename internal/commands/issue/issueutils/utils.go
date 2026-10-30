package issueutils

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/sync/errgroup"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/tableprinter"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

func DisplayIssueList(streams *iostreams.IOStreams, issues []*gitlab.Issue, projectID string) string {
	c := streams.Color()
	table := tableprinter.NewTablePrinter()
	table.SetIsTTY(streams.IsOutputTTY())

	if len(issues) > 0 {
		table.AddRow("ID", "Title", "Labels", "Created at")
	}

	for _, issue := range issues {
		table.AddCell(streams.Hyperlink(IssueState(c, issue), issue.WebURL))
		table.AddCell(issue.Title)

		if len(issue.Labels) > 0 {
			table.AddCellf("(%s)", c.Cyan(strings.Trim(strings.Join(issue.Labels, ", "), ",")))
		} else {
			table.AddCell("")
		}

		if issue.CreatedAt != nil {
			table.AddCell(c.Gray(utils.TimeToPrettyTimeAgo(*issue.CreatedAt)))
		} else {
			table.AddCell("")
		}
		table.EndRow()
	}

	return table.Render()
}

func DisplayIssue(c *iostreams.ColorPalette, i *gitlab.Issue, isTTY bool) string {
	var duration string
	if i.CreatedAt != nil {
		duration = utils.TimeToPrettyTimeAgo(*i.CreatedAt)
	}
	issueID := IssueState(c, i)

	if isTTY {
		if duration != "" {
			return fmt.Sprintf("%s %s (%s)\n %s\n", issueID, i.Title, duration, i.WebURL)
		}
		return fmt.Sprintf("%s %s\n %s\n", issueID, i.Title, i.WebURL)
	} else {
		return i.WebURL
	}
}

func IssueState(c *iostreams.ColorPalette, i *gitlab.Issue) string {
	switch {
	case i.State == "opened":
		return c.Green(fmt.Sprintf("#%d", i.IID))
	default:
		return c.Red(fmt.Sprintf("#%d", i.IID))
	}
}

func IssuesFromArgs(ctx context.Context, apiClientFunc func(repoHost string) (*api.Client, error), gitlabClient *gitlab.Client, baseRepoFn func() (glrepo.Interface, error), defaultHostname string, cfg config.Config, args []string) ([]*gitlab.Issue, glrepo.Interface, error) {
	var baseRepo glrepo.Interface

	if len(args) <= 1 {
		if len(args) == 1 {
			args = strings.Split(args[0], ",")
		}
		if len(args) <= 1 {
			issue, repo, err := IssueFromArg(apiClientFunc, gitlabClient, baseRepoFn, defaultHostname, cfg, args[0])
			if err != nil {
				return nil, nil, err
			}
			baseRepo = repo
			return []*gitlab.Issue{issue}, baseRepo, err
		}
	}

	errGroup, _ := errgroup.WithContext(ctx)
	errGroup.SetLimit(api.MaxConcurrentRequests)
	issues := make([]*gitlab.Issue, len(args))
	repos := make([]glrepo.Interface, len(args))
	for i, arg := range args {
		errGroup.Go(func() error {
			issue, repo, err := IssueFromArg(apiClientFunc, gitlabClient, baseRepoFn, defaultHostname, cfg, arg)
			if err != nil {
				return err
			}
			repos[i] = repo
			issues[i] = issue
			return nil
		})
	}
	if err := errGroup.Wait(); err != nil {
		return nil, nil, err
	}
	return issues, repos[len(repos)-1], nil
}

func IssueFromArg(apiClientFunc func(repoHost string) (*api.Client, error), client *gitlab.Client, baseRepoFn func() (glrepo.Interface, error), defaultHostname string, cfg config.Config, arg string) (*gitlab.Issue, glrepo.Interface, error) {
	issueIID, baseRepo := issueMetadataFromURL(arg, defaultHostname, cfg)
	if issueIID == 0 {
		var err error
		issueIIDInt, err := strconv.Atoi(strings.TrimPrefix(arg, "#"))
		if err != nil {
			return nil, nil, fmt.Errorf("invalid issue format: %q", arg)
		}
		issueIID = int64(issueIIDInt)
	}

	if baseRepo == nil {
		var err error
		baseRepo, err = baseRepoFn()
		if err != nil {
			return nil, nil, fmt.Errorf("could not determine base repository: %w", err)
		}
	} else {
		a, err := apiClientFunc(baseRepo.RepoHost())
		if err != nil {
			return nil, nil, err
		}
		client = a.Lab()
	}

	issue, err := issueFromIID(client, baseRepo, issueIID)
	return issue, baseRepo, err
}

func issueMetadataFromURL(s, defaultHostname string, cfg config.Config) (int64, glrepo.Interface) {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return 0, nil
	}

	issueIID, repo := cmdutils.ParseIssueFromURL(s, defaultHostname, cfg)
	return int64(issueIID), repo
}

func issueFromIID(apiClient *gitlab.Client, repo glrepo.Interface, issueIID int64) (*gitlab.Issue, error) {
	return api.GetIssue(apiClient, repo.FullName(), issueIID)
}
