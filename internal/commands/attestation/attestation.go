package attestation

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	attestationVerifyCmd "gitlab.com/gitlab-org/cli/internal/commands/attestation/verify"
	"gitlab.com/gitlab-org/cli/internal/text"
)

func NewCmdAttestation(f cmdutils.Factory) *cobra.Command {
	attestationCmd := &cobra.Command{
		Use:   "attestation <command> [flags]",
		Short: `Manage software attestations. (EXPERIMENTAL)`,
		Long: heredoc.Doc(`
		Manage software attestations for artifacts built in GitLab CI/CD pipelines.
		An attestation is a signed statement about an artifact, such as a provenance
		statement that records how the artifact was built.

		Use this command to verify that an artifact was built by the expected
		GitLab project and pipeline.
		`) + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Verify attestation for filename.txt in the gitlab-org/gitlab project
			glab attestation verify gitlab-org/gitlab filename.txt

			# Verify attestation for filename.txt in the project with ID 123
			glab attestation verify 123 filename.txt`),
		Annotations: map[string]string{
			"help:arguments": heredoc.Doc(`
			Specify a project in one of the following formats:
			- By number, for example "123"
			- By path, for example "gitlab-org/cli"
			`),
		},
	}

	attestationCmd.AddCommand(attestationVerifyCmd.NewCmd(f))

	return attestationCmd
}
