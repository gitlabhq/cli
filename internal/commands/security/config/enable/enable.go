package enable

import (
	"context"
	"fmt"

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

	enableCmd := &cobra.Command{
		Use:   "enable <profile> [flags]",
		Short: "Enable a security scan profile for a project. (EXPERIMENTAL)",
		Long: heredoc.Doc(`
			Attach a security scan profile to a project.

			Prerequisites:

			- At least the Maintainer role or the Security Manager role for the project.
		`) + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Enable dependency scanning on the current project
			$ glab security config enable dependency_scanning

			# Enable SAST on a specific project
			$ glab security config enable sast -R gitlab-org/cli

			# Enable auto-remediation for vulnerable dependencies
			$ glab security config enable dependency_scanning_post_processing
		`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(args)
			return opts.run(cmd.Context())
		},
	}

	return enableCmd
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

	project, err := repo.Project(client)
	if err != nil {
		return cmdutils.WrapError(err, "failed to resolve the project")
	}

	_, err = client.SecurityScanProfiles.AttachSecurityScanProfile(&gitlab.AttachSecurityScanProfileOptions{
		SecurityScanProfileID: gitlab.SecurityScanProfileGID(o.profile),
		ProjectIDs:            []int64{project.ID},
	}, gitlab.WithContext(ctx))
	if err != nil {
		return cmdutils.WrapError(err, fmt.Sprintf("failed to enable the profile %q", o.profile))
	}

	color := o.io.Color()
	o.io.LogInfof("%s Enabled the %q security scan profile for %s.\n", color.GreenCheck(), o.profile, repo.FullName())
	return nil
}
