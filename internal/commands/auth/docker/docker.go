// Package docker provides commands that help configure a docker
// credential helper.
package docker

import (
	"fmt"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/docker/docker-credential-helpers/credentials"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

// NewCmdConfigureDocker returns a command that configures the CLI
// as a Docker credential helper.
func NewCmdConfigureDocker(f cmdutils.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "configure-docker",
		Args:  cobra.NoArgs,
		Short: "Register glab as a Docker credential helper.",
		Long: heredoc.Docf(`
		Configures Docker to use glab for authentication with GitLab
		container registries. This command runs only on Linux and macOS.

		After you run this command, Docker uses glab to obtain credentials
		when it pulls from or pushes to a GitLab container registry.

		Docker looks for the credential helper script by name on your %[1]sPATH%[1]s. By default, glab installs the script next to the glab binary. If that directory is not writable, glab falls back to %[1]s~/.local/bin%[1]s, then to any other writable directory on your %[1]sPATH%[1]s. If the script lands in a directory that is not on your %[1]sPATH%[1]s, glab tells you which directory to add.
		`, "`"),
		Example: heredoc.Doc(`
			# Configure Docker to use glab for GitLab container registry authentication
			glab auth configure-docker
		`),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			io := f.IO()
			cfg := f.Config()
			return configureDocker(io, cfg)
		},
	}
	return cmd
}

// NewCmdCredentialHelper returns a command that handles Docker credential
// requests.
func NewCmdCredentialHelper(f cmdutils.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:       "docker-helper",
		ValidArgs: []cobra.Completion{"store", "get", "erase"},
		Args: cobra.MatchAll(
			helperArgCheck,
			cobra.OnlyValidArgs,
		),
		Short: "A Docker credential helper for GitLab container and artifact registries.",
		Long: heredoc.Doc(`
		Responds to Docker credential helper requests for GitLab container
		registries and Artifact Registries. Docker invokes this command
		automatically.
		`),
		Example: heredoc.Doc(`
		# Docker invokes the helper automatically; supported actions are 'store', 'get', and 'erase'.
		# Retrieve the stored credentials for a registry:
		echo registry.gitlab.com | glab auth docker-helper get`),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			apiClient, err := f.ApiClient("")
			if err != nil {
				return err
			}

			httpClient := apiClient.HTTPClient()

			credHelper := Helper{
				client:    httpClient,
				cfg:       f.Config(),
				io:        f.IO(),
				userAgent: f.BuildInfo().UserAgent(),
				ctx:       cmd.Context(),
			}

			action := args[0]
			return credentials.HandleCommand(&credHelper, action, f.IO().In, f.IO().StdOut)
		},
	}
	return cmd
}

func helperArgCheck(cmd *cobra.Command, args []string) error {
	validation := cobra.ExactArgs(1)
	if err := validation(cmd, args); err != nil {
		return fmt.Errorf("arg is missing - valid args: %s", cmd.ValidArgs)
	}
	return nil
}
