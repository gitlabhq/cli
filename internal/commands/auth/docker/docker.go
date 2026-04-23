// Package docker provides commands that help configure a docker
// credential helper.
package docker

import (
	"fmt"
	"runtime"

	"github.com/docker/docker-credential-helpers/credentials"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

const (
	// helperFullName is the full name of the credential helper,
	// and is what Docker will look for in the user's PATH.
	helperFullName = "docker-credential-glab"
	// helperShortName is the short name of the credential,
	// and is what the Docker config will list in the credHelpers
	// configuration object.
	helperShortName = "glab"
)

// NewCmdConfigureDocker returns a command that configures the CLI
// as a Docker credential helper.
func NewCmdConfigureDocker(f cmdutils.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "configure-docker",
		Args:  cobra.ExactArgs(0),
		Short: "Register glab as a Docker credential helper.",
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO: The shell wrapper approach is only implemented for POSIX
			// compliant operating systems at the moment.
			// See https://gitlab.com/gitlab-org/cli/-/issues/7906 to track
			// the work on support for additional operating systems.
			if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
				return fmt.Errorf("operating system %q is not supported; "+
					"only Linux and MacOS (Darwin) are supported", runtime.GOOS)
			}

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
		Short: "A Docker credential helper for GitLab container registries.",
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			apiClient, err := f.ApiClient("")
			if err != nil {
				return err
			}

			httpClient := apiClient.HTTPClient()

			credHelper := Helper{httpClient, f.Config()}

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
