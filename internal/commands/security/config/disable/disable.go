package disable

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

	disableCmd := &cobra.Command{
		Use:   "disable <profile> [flags]",
		Short: "Disable a security scan profile for a project. (EXPERIMENTAL)",
		Long: heredoc.Doc(`
			Detach a security scan profile from a project.

			Prerequisites:

			- At least the Maintainer role or the Security Manager role for the project.
		`) + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Disable dependency scanning on the current project
			$ glab security config disable dependency_scanning

			# Disable SAST on a specific project
			$ glab security config disable sast -R gitlab-org/cli

			# Disable auto-remediation for vulnerable dependencies
			$ glab security config disable dependency_scanning_post_processing
		`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(args)
			return opts.run(cmd.Context())
		},
	}

	return disableCmd
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

	_, err = client.SecurityScanProfiles.DetachSecurityScanProfile(&gitlab.DetachSecurityScanProfileOptions{
		SecurityScanProfileID: gitlab.SecurityScanProfileGID(o.profile),
		ProjectIDs:            []int64{project.ID},
	}, gitlab.WithContext(ctx))
	if err != nil {
		return cmdutils.WrapError(err, fmt.Sprintf("failed to disable the profile %q", o.profile))
	}

	color := o.io.Color()
	o.io.LogInfof("%s Disabled the %q security scan profile for %s.\n", color.GreenCheck(), o.profile, repo.FullName())
	return nil
}
