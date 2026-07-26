package archive

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

func NewCmdArchive(f cmdutils.Factory) *cobra.Command {
	repoArchiveCmd := &cobra.Command{
		Use:   "archive [<repo>] [<dir>] [flags]",
		Short: `Get an archive of the repository.`,
		Example: heredoc.Doc(`
			glab repo archive profclems/glab

			# Downloads zip file of current repository
			glab repo archive

			# Downloads repo zip file into mydirectory
			glab repo archive profclems/glab mydirectory

			# Finds repo for current user and download in ZIP format
			glab repo archive profclems/glab --format=zip`),
		Long: heredoc.Doc(`
			Without a repository argument, archives the current repository.
			The repository accepts these shorthand references:

			- repo
			- namespace/repo
			- namespace/group/repo

			The second argument sets the directory to download the archive into.
		`),
		Args: cobra.MaximumNArgs(2),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var name string
			var err error

			var client *gitlab.Client
			var repo glrepo.Interface
			if len(args) != 0 {
				repo, err = glrepo.FromFullName(args[0], f.DefaultHostname(), f.Config())
				if err != nil {
					return err
				}

				// repository is coming from command args, not -R
				apiClient, err := f.ApiClient(repo.RepoHost())
				if err != nil {
					return err
				}

				if len(args) > 1 {
					name = args[1]
				}

				client = apiClient.Lab()
			} else {
				client, err = f.GitLabClient()
				if err != nil {
					return err
				}
				repo, err = f.BaseRepo()
				if err != nil {
					return err
				}
			}

			format, _ := cmd.Flags().GetString("format")

			// tar.gz, tar.bz2, tbz, tbz2, tb2, bz2, tar, and zip
			extensions := []string{"tar.gz", "tar.bz2", "tbz", "tbz2", "tb2", "bz2", "tar", "zip"}
			if b := contains(extensions, format); !b {
				return errors.New("format must be one of " + strings.Join(extensions, ","))
			}

			l := &gitlab.ArchiveOptions{}
			l.Format = new(format)
			if sha, _ := cmd.Flags().GetString("sha"); sha != "" {
				l.SHA = new(sha)
			}
			ext := *l.Format
			archiveName := strings.ReplaceAll(repo.FullName(), "/", "-") + "." + ext
			if strings.TrimSpace(name) != "" {
				archiveName = name + "." + ext
			}

			bt, _, err := client.Repositories.Archive(repo.FullName(), l)
			if err != nil {
				return fmt.Errorf("failed to get archive: %w", err)
			}

			r := bytes.NewReader(bt)
			out, err := os.Create(archiveName + ".tmp")
			if err != nil {
				return fmt.Errorf("failed to create repository archive: %w", err)
			}

			counter := &CloneWriter{}
			if _, err = io.Copy(out, io.TeeReader(r, counter)); err != nil {
				_ = out.Close()
				return fmt.Errorf("failed to write repositories: %w", err)
			}

			f.IO().LogInfo()
			_ = out.Close()
			if err = os.Rename(archiveName+".tmp", archiveName); err != nil {
				return fmt.Errorf("failed to rename tmp repos: %w", err)
			}
			f.IO().LogInfo("Complete...", archiveName)
			return nil
		},
	}

	repoArchiveCmd.Flags().StringP("format", "f", "zip", "Optional. Specify format if you want a downloaded archive: tar.gz, tar.bz2, tbz, tbz2, tb2, bz2, tar, zip.")
	repoArchiveCmd.Flags().StringP("sha", "s", "", "The commit SHA to download. A tag, branch reference, or SHA can be used. Defaults to the tip of the default branch if not specified.")

	return repoArchiveCmd
}

// CloneWriter w
type CloneWriter struct {
	Total uint64
}

func (wc *CloneWriter) Write(p []byte) (int, error) {
	n := len(p)
	wc.Total += uint64(n)
	wc.progress()
	return n, nil
}

func (wc CloneWriter) progress() {
	fmt.Printf("\r%s", strings.Repeat(" ", 35))
	fmt.Printf("\rCloning... %s complete", humanize.Bytes(wc.Total))
}

func contains(arr []string, str string) bool {
	return slices.Contains(arr, str)
}
