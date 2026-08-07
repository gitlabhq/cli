package artifactregistry

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
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
			token.
		`) + text.ExperimentalString,
	}

	cmd.AddCommand(statusCmd.NewCmd(f))

	return cmd
}
