package status

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type options struct {
	io           *iostreams.IOStreams
	gitlabClient func() (*gitlab.Client, error)
	baseRepo     func() (glrepo.Interface, error)

	profile string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
		baseRepo:     f.BaseRepo,
	}

	statusCmd := &cobra.Command{
		Use:   "status <profile> [flags]",
		Short: "Show the status of a security scan profile for a project. (EXPERIMENTAL)",
		Long: heredoc.Doc(`
			Show whether a security scan profile is attached to a project and its
			current scan status.
		`) + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Show dependency scanning status for the current project
			$ glab security config status dependency_scanning

			# Show SAST status for a specific project
			$ glab security config status sast -R gitlab-org/cli

			# Show auto-remediation status for vulnerable dependencies
			$ glab security config status dependency_scanning_post_processing
		`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(args)
			return opts.run(cmd.Context())
		},
	}

	return statusCmd
}

func (o *options) complete(args []string) {
	o.profile = args[0]
}

func (o *options) run(ctx context.Context) error {
	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	repo, err := o.baseRepo()
	if err != nil {
		return err
	}

	fullPath := repo.FullName()
	statuses, _, err := client.SecurityScanProfiles.ListProjectScanProfileStatuses(fullPath, gitlab.WithContext(ctx))
	if err != nil {
		return cmdutils.WrapError(err, fmt.Sprintf("failed to read the status of the profile %q", o.profile))
	}

	status := "NOT_CONFIGURED"
	if i := slices.IndexFunc(statuses, func(s gitlab.ScanProfileStatus) bool {
		return strings.EqualFold(s.ScanProfile.ScanType, o.profile)
	}); i >= 0 {
		status = statuses[i].Status
	}

	o.io.LogInfof("%s profile for %s: %s\n", o.profile, fullPath, status)
	return nil
}
