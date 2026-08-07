package list

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/release/releaseutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

var getRelease = func(client *gitlab.Client, projectID any, tag string) (*gitlab.Release, error) {
	release, _, err := client.Releases.GetRelease(projectID, tag)
	if err != nil {
		return nil, err
	}

	return release, nil
}

var listReleases = func(client *gitlab.Client, projectID any, opts *gitlab.ListReleasesOptions) ([]*gitlab.Release, error) {
	releases, _, err := client.Releases.ListReleases(projectID, opts)
	return releases, err
}

type options struct {
	outputFormat string

	io           *iostreams.IOStreams
	gitlabClient func() (*gitlab.Client, error)
	baseRepo     func() (glrepo.Interface, error)
	config       func() config.Config
}

func NewCmdReleaseList(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
		baseRepo:     f.BaseRepo,
		config:       f.Config,
	}

	releaseListCmd := &cobra.Command{
		Use:   "list [flags]",
		Short: `List releases in a repository.`,
		Long: heredoc.Docf(`
			By default, lists the releases for the current project, most recent
			first. Use %[1]s--repo%[1]s to target a different project, or %[1]s-F json%[1]s for
			machine-readable output.
		`, "`"),
		Aliases: []string{"ls"},
		Args:    cobra.NoArgs,
		Example: heredoc.Doc(`
			glab release list
			glab release list --per-page 50
			glab release list -R owner/repository
			glab release list -F json`),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.run(cmd)
		},
	}

	releaseListCmd.Flags().IntP("page", "p", 1, "Page number.")
	releaseListCmd.Flags().IntP("per-page", "P", 30, "Number of items to list per page.")
	cmdutils.EnableJSONOutput(releaseListCmd, opts.io, &opts.outputFormat)

	releaseListCmd.Flags().StringP("tag", "t", "", "Filter releases by tag <name>.")
	// deprecate in favour of the `release view` command
	_ = releaseListCmd.Flags().MarkDeprecated("tag", "Use `glab release view <tag>` instead.")

	// make it hidden but still accessible
	// TODO: completely remove before a major release (v2.0.0+)
	_ = releaseListCmd.Flags().MarkHidden("tag")

	return releaseListCmd
}

func (o *options) run(cmd *cobra.Command) error {
	l := &gitlab.ListReleasesOptions{}

	page, _ := cmd.Flags().GetInt("page")
	l.Page = int64(page)
	perPage, _ := cmd.Flags().GetInt("per-page")
	l.PerPage = int64(perPage)

	tag, err := cmd.Flags().GetString("tag")
	if err != nil {
		return err
	}

	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	repo, err := o.baseRepo()
	if err != nil {
		return err
	}

	if tag != "" {
		release, err := getRelease(client, repo.FullName(), tag)
		if err != nil {
			return err
		}

		if o.outputFormat == "json" {
			return o.io.PrintJSON(release)
		}

		cfg := o.config()
		glamourStyle, _ := cfg.Get(repo.RepoHost(), "glamour_style")
		o.io.ResolveBackgroundColor(glamourStyle)

		err = o.io.StartPager()
		if err != nil {
			return err
		}
		defer o.io.StopPager()

		o.io.LogInfo(releaseutils.DisplayRelease(o.io, release, repo))
	} else {

		releases, err := listReleases(client, repo.FullName(), l)
		if err != nil {
			return err
		}

		if o.outputFormat == "json" {
			return o.io.PrintJSON(releases)
		}

		title := utils.NewListTitle("release")
		title.RepoName = repo.FullName()
		title.Page = 0
		title.CurrentPageTotal = len(releases)
		err = o.io.StartPager()
		if err != nil {
			return err
		}
		defer o.io.StopPager()

		o.io.LogInfof("%s\n%s\n", title.Describe(), releaseutils.DisplayAllReleases(o.io, releases, repo.FullName()))
	}
	return nil
}
