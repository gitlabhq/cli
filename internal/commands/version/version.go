package version

import (
	"fmt"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

func NewCmdVersion(f cmdutils.Factory) *cobra.Command {
	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Show version information for glab.",
		Long: heredoc.Doc(`
			Prints the installed glab version and the commit it was built from.
			Include this information when reporting a bug.
		`),
		Example: heredoc.Doc(`
			glab version`),
		Aliases: []string{"v"},
		Args:    cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			buildInfo := f.BuildInfo()
			f.IO().LogInfof("%s", Scheme(buildInfo.Version, buildInfo.Commit))
			return nil
		},
	}
	return versionCmd
}

func Scheme(version, commit string) string {
	return fmt.Sprintf("glab %s (%s)\n", strings.TrimPrefix(version, "v"), commit)
}
