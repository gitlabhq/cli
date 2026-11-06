package autoflowutils

import (
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
)

// NewWorkflowTokenFlag registers --workflow-token-file on cmd.
func NewWorkflowTokenFlag(cmd *cobra.Command) *cmdutils.SecretFile {
	return cmdutils.AddSecretFileFlag(cmd, "workflow-token-file", "workflow token", "GLAB_AUTOFLOW_WORKFLOW_TOKEN", "that 'glab autoflow workflow start' printed")
}

// NewChannelTokenFlag registers --channel-token-file on cmd.
func NewChannelTokenFlag(cmd *cobra.Command) *cmdutils.SecretFile {
	return cmdutils.AddSecretFileFlag(cmd, "channel-token-file", "channel token", "GLAB_AUTOFLOW_CHANNEL_TOKEN", "from 'glab autoflow workflow start --channel' or from a module")
}

// NewTokenBindingFlag registers --token-binding-file on cmd.
func NewTokenBindingFlag(cmd *cobra.Command) *cmdutils.SecretFile {
	return cmdutils.AddSecretFileFlag(cmd, "token-binding-file", "token binding", "GLAB_AUTOFLOW_TOKEN_BINDING", "of an earlier submission, base64-encoded as 'glab autoflow workflow start' printed it")
}
