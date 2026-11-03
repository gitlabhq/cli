package packages

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	packagesDeleteCmd "gitlab.com/gitlab-org/cli/internal/commands/packages/delete"
	packagesDownloadCmd "gitlab.com/gitlab-org/cli/internal/commands/packages/download"
	packagesListCmd "gitlab.com/gitlab-org/cli/internal/commands/packages/list"
	packagesUploadCmd "gitlab.com/gitlab-org/cli/internal/commands/packages/upload"
)

func NewCmd(f cmdutils.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "packages <command> [flags]",
		Short: `Manage packages in the GitLab package registry.`,
		Long: heredoc.Docf(`
		Upload, download, list, and delete packages in a project's package
		registry, using your existing %[1]sglab%[1]s authentication.

		%[1]slist%[1]s and %[1]sdelete%[1]s operate on packages of any type. %[1]supload%[1]s and %[1]sdownload%[1]s
		are limited to generic packages, which let you store and retrieve
		arbitrary files identified by a package name, version, and file name.
		`, "`"),
	}

	cmdutils.EnableRepoOverride(cmd, f)

	cmd.AddCommand(packagesDeleteCmd.NewCmd(f))
	cmd.AddCommand(packagesDeleteCmd.NewRmCmd(f))
	cmd.AddCommand(packagesDownloadCmd.NewCmd(f))
	cmd.AddCommand(packagesListCmd.NewCmd(f))
	cmd.AddCommand(packagesUploadCmd.NewCmd(f))
	return cmd
}
