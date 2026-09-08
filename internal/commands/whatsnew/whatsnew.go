package whatsnew

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/hashicorp/go-version"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/update"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

const (
	projectPath = "gitlab-org/cli"
	maxReleases = 10
	// Separate from update.LastSeenVersionKey on purpose: the banner needs
	// `marker >= current` (to stop repeating), the default whatsnew view
	// needs `marker < current` (to still include the just-installed version).
	// Sharing one marker produces TestWhatsnew_bannerNudgeThenWhatsnew.
	LastWhatsnewVersionKey = "last_whatsnew_version"
)

var clientCreator = update.CreateUnauthenticatedClient

type options struct {
	tagName      string
	sinceVersion string
	showLatest   bool
	isDefault    bool

	io        *iostreams.IOStreams
	cfg       func() config.Config
	buildInfo api.BuildInfo
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		cfg:       f.Config,
		buildInfo: f.BuildInfo(),
	}

	cmd := &cobra.Command{
		Use:   "whatsnew [version]",
		Short: "Show release notes for new versions of glab.",
		Long: heredoc.Doc(`
			With no arguments, shows release notes for every glab release
			published since the last time you ran 'whatsnew' or saw the
			post-upgrade banner — capped at the most recent 10 releases.

			Pass a version argument to view notes for a specific release,
			or use --since to set an explicit baseline.
		`),
		Example: heredoc.Doc(`
			# Show release notes for every release since you last looked
			glab whatsnew

			# Show notes for the latest published release
			glab whatsnew --latest

			# Show notes for a specific version
			glab whatsnew v1.85.0

			# Show notes for every release published after a given version
			glab whatsnew --since v1.80.0
		`),
		Args: cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(cmd, args)
			if err := opts.validate(); err != nil {
				return err
			}
			return opts.run(cmd.Context())
		},
	}

	fl := cmd.Flags()
	fl.StringVar(&opts.sinceVersion, "since", "", "Show release notes for every release newer than this version.")
	fl.BoolVar(&opts.showLatest, "latest", false, "Show release notes for the latest published release only.")
	cmd.MarkFlagsMutuallyExclusive("since", "latest")

	return cmd
}

func (o *options) complete(cmd *cobra.Command, args []string) {
	if len(args) == 1 {
		o.tagName = args[0]
	}
	fl := cmd.Flags()
	o.isDefault = o.tagName == "" && !fl.Changed("since") && !fl.Changed("latest")
}

func (o *options) validate() error {
	// cobra's MarkFlagsMutuallyExclusive can't express conflicts between
	// flags and a positional argument, so we cover those here.
	if o.tagName != "" && (o.sinceVersion != "" || o.showLatest) {
		return cmdutils.WrapError(errors.New("flag conflict"), "cannot combine a version argument with --since or --latest.")
	}
	return nil
}

func (o *options) run(ctx context.Context) error {
	client, err := clientCreator(o.buildInfo.UserAgent())
	if err != nil {
		return err
	}
	releases, err := o.fetchReleases(ctx, client.Lab())
	if err != nil {
		return err
	}
	if len(releases) == 0 {
		o.io.LogError("No new releases since you last checked.")
		return nil
	}

	if err := o.renderReleases(releases); err != nil {
		return err
	}

	// Explicit views (--latest, version arg) shouldn't dismiss releases
	// the user hasn't actually read, so only the default advances.
	if o.isDefault {
		_ = setLastWhatsnewVersion(o.cfg(), strings.TrimSpace(o.buildInfo.Version))
	}
	return nil
}

func (o *options) fetchReleases(ctx context.Context, client *gitlab.Client) ([]*gitlab.Release, error) {
	switch {
	case o.tagName != "":
		r, resp, err := client.Releases.GetRelease(projectPath, normalizeTag(o.tagName), gitlab.WithContext(ctx))
		if err != nil {
			if resp != nil && resp.StatusCode == http.StatusNotFound {
				return nil, cmdutils.WrapError(err, fmt.Sprintf("release %q not found.", o.tagName))
			}
			return nil, cmdutils.WrapError(err, "failed to fetch release.")
		}
		return []*gitlab.Release{r}, nil

	case o.showLatest:
		releases, _, err := client.Releases.ListReleases(projectPath, &gitlab.ListReleasesOptions{
			ListOptions: gitlab.ListOptions{Page: 1, PerPage: 1},
		}, gitlab.WithContext(ctx))
		if err != nil {
			return nil, cmdutils.WrapError(err, "failed to fetch latest release.")
		}
		return releases, nil

	default:
		sinceStr := strings.TrimSpace(o.sinceVersion)
		if sinceStr == "" {
			seen, _ := o.cfg().Get("", LastWhatsnewVersionKey)
			sinceStr = strings.TrimSpace(seen)
		}
		releases, _, err := client.Releases.ListReleases(projectPath, &gitlab.ListReleasesOptions{
			ListOptions: gitlab.ListOptions{Page: 1, PerPage: int64(maxReleases)},
		}, gitlab.WithContext(ctx))
		if err != nil {
			return nil, cmdutils.WrapError(err, "failed to fetch releases.")
		}
		return filterNewer(releases, sinceStr), nil
	}
}

func (o *options) renderReleases(releases []*gitlab.Release) error {
	cfg := o.cfg()
	glamourStyle, _ := cfg.Get("", "glamour_style")
	o.io.ResolveBackgroundColor(glamourStyle)

	if err := o.io.StartPager(); err != nil {
		return err
	}
	defer o.io.StopPager()

	c := o.io.Color()
	for i, r := range releases {
		if i > 0 {
			o.io.LogInfo()
		}
		o.io.LogInfo(c.Bold(fmt.Sprintf("## %s", r.TagName)))
		body := strings.TrimSpace(r.Description)
		if body == "" {
			o.io.LogInfo("(no release notes)")
			continue
		}
		rendered, err := utils.RenderMarkdown(body, o.io.BackgroundColor())
		if err != nil {
			o.io.LogInfo(body)
			continue
		}
		o.io.LogInfof("%s", rendered)
	}
	return nil
}

func filterNewer(releases []*gitlab.Release, sinceStr string) []*gitlab.Release {
	since, err := version.NewVersion(sinceStr)
	if err != nil {
		return releases
	}
	out := make([]*gitlab.Release, 0, len(releases))
	for _, r := range releases {
		v, err := version.NewVersion(r.TagName)
		if err != nil {
			continue
		}
		if v.GreaterThan(since) {
			out = append(out, r)
		}
	}
	return out
}

func normalizeTag(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return v
	}
	if !strings.HasPrefix(v, "v") {
		return "v" + v
	}
	return v
}

func setLastWhatsnewVersion(cfg config.Config, v string) error {
	if err := cfg.Set("", LastWhatsnewVersionKey, v); err != nil {
		return err
	}
	return cfg.Write()
}
