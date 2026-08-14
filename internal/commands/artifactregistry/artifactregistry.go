package artifactregistry

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	getTokenCmd "gitlab.com/gitlab-org/cli/internal/commands/artifactregistry/gettoken"
	loginCmd "gitlab.com/gitlab-org/cli/internal/commands/artifactregistry/login"
	statusCmd "gitlab.com/gitlab-org/cli/internal/commands/artifactregistry/status"
	"gitlab.com/gitlab-org/cli/internal/text"
)

// NewCmd returns the `glab artifact-registry` command.
func NewCmd(f cmdutils.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "artifact-registry <command>",
		Short:   "Authenticate with GitLab Artifact Registry. (EXPERIMENTAL)",
		Aliases: []string{"ar"},
		Long: heredoc.Doc(`
			Exchange a GitLab credential for a short-lived Artifact Registry access
			token, either to check your access, to hand the token to a caller, or to
			configure a package manager to authenticate against the registry.
		`) + text.ExperimentalString,
	}

	cmd.AddCommand(statusCmd.NewCmd(f))
	cmd.AddCommand(getTokenCmd.NewCmd(f))
	cmd.AddCommand(loginCmd.NewCmd(f))

	return cmd
}
