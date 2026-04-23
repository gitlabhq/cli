package view

import (
	"errors"
	"fmt"
	"net/http"

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

type options struct {
	tagName       string
	openInBrowser bool
	outputFormat  string

	io           *iostreams.IOStreams
	gitlabClient func() (*gitlab.Client, error)
	baseRepo     func() (glrepo.Interface, error)
	config       func() config.Config
}

func NewCmdView(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
		baseRepo:     f.BaseRepo,
		config:       f.Config,
	}

	cmd := &cobra.Command{
		Use:   "view <tag>",
		Short: "View information about a GitLab release.",
		Long: heredoc.Doc(`View information about a GitLab release.

			Without an explicit tag name argument, shows the latest release in the project.
		`),
		Example: heredoc.Doc(`
			# View the latest release of a GitLab repository
			glab release view

			# View a release with specified tag name
			glab release view v1.0.1`),
		Args: cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(args)

			return opts.run()
		},
	}

	cmd.Flags().BoolVarP(&opts.openInBrowser, "web", "w", false, "Open the release in the browser. (default false)")
	cmdutils.EnableJSONOutput(cmd, &opts.outputFormat)

	return cmd
}

func (o *options) complete(args []string) {
	if len(args) == 1 {
		o.tagName = args[0]
	}
}

func (o *options) run() error {
	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	repo, err := o.baseRepo()
	if err != nil {
		return err
	}

	cfg := o.config()

	var resp *gitlab.Response
	var release *gitlab.Release

	if o.tagName == "" {
		releases, _, err := client.Releases.ListReleases(repo.FullName(), &gitlab.ListReleasesOptions{})
		if err != nil {
			return cmdutils.WrapError(err, "could not fetch latest release.")
		}
		if len(releases) < 1 {
			return cmdutils.WrapError(errors.New("not found"), fmt.Sprintf("no release found for %q", repo.FullName()))
		}

		release = releases[0]
		o.tagName = release.TagName
	} else {
		release, resp, err = client.Releases.GetRelease(repo.FullName(), o.tagName)
		if err != nil {
			if resp != nil && (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden) {
				return cmdutils.WrapError(err, "release does not exist.")
			}
			return cmdutils.WrapError(err, "failed to fetch release.")
		}
	}

	if o.openInBrowser { // open in browser if --web flag is specified
		url := release.Links.Self

		if o.io.IsOutputTTY() {
			o.io.LogInfof("Opening %s in your browser.\n", url)
		}

		browser, _ := cfg.Get(repo.RepoHost(), "browser")
		return utils.OpenInBrowser(url, browser)
	}

	if o.outputFormat == "json" {
		return o.io.PrintJSON(release)
	}

	glamourStyle, _ := cfg.Get(repo.RepoHost(), "glamour_style")
	o.io.ResolveBackgroundColor(glamourStyle)

	err = o.io.StartPager()
	if err != nil {
		return err
	}
	defer o.io.StopPager()

	o.io.LogInfo(releaseutils.DisplayRelease(o.io, release, repo))
	return nil
}
