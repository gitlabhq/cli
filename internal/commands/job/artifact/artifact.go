package artifact

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

func NewCmdArtifact(f cmdutils.Factory) *cobra.Command {
	jobArtifactCmd := &cobra.Command{
		Use:     "artifact <refName> <jobName> [flags]",
		Short:   `Download all artifacts from the most recent pipeline.`,
		Aliases: []string{"push"},
		Example: heredoc.Doc(`
		glab job artifact main build
		glab job artifact main deploy --path="artifacts/"
		glab job artifact main deploy --list-paths
		glab job artifact refs/merge-requests/123/head build`),
		Long: heredoc.Docf(`
		Downloads all artifacts from the most recent successful pipeline.

		%[1]s<refName>%[1]s is a branch name, tag, or merge request reference. For a branch
		or tag, use the name directly. For a merge request pipeline, use the ref
		%[1]srefs/merge-requests/<iid>/head%[1]s, where %[1]s<iid>%[1]s is the merge request IID.
		`, "`"),
		Args: cobra.ExactArgs(2),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := f.BaseRepo()
			if err != nil {
				return err
			}
			client, err := f.GitLabClient()
			if err != nil {
				return err
			}
			path, err := cmd.Flags().GetString("path")
			if err != nil {
				return err
			}
			listPaths, err := cmd.Flags().GetBool("list-paths")
			if err != nil {
				return err
			}
			return DownloadArtifacts(client, repo, path, listPaths, args[0], args[1])
		},
	}
	jobArtifactCmd.Flags().StringP("path", "p", "./", "Path to download the artifact files.")
	jobArtifactCmd.Flags().BoolP("list-paths", "l", false, "Print the paths of downloaded artifacts. (default false)")
	return jobArtifactCmd
}
