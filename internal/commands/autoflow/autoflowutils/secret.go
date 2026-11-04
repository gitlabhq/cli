package autoflowutils

import (
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
)

// NewTokenBindingFlag registers --token-binding-file on cmd.
func NewTokenBindingFlag(cmd *cobra.Command) *cmdutils.SecretFile {
	return cmdutils.AddSecretFileFlag(cmd, "token-binding-file", "token binding", "GLAB_AUTOFLOW_TOKEN_BINDING", "of an earlier submission, base64-encoded as 'glab autoflow workflow start' printed it")
}
