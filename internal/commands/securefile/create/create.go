package create

import (
	"fmt"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/securefile/helpers"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	fileName      string
	inputFilePath string

	io           *iostreams.IOStreams
	gitlabClient func() (*gitlab.Client, error)
	baseRepo     func() (glrepo.Interface, error)
}

func NewCmdCreate(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
		baseRepo:     f.BaseRepo,
	}
	securefileCreateCmd := &cobra.Command{
		Use:   "create <name> <path>",
		Short: `Upload a new secure file to a project.`,
		Long: heredoc.Docf(`
		Provide the name to store the file under, followed by the local path
		to the file to upload.

		Secure files are stored outside the project's repository and not in
		version control. Both plain text and binary files are supported, up
		to a maximum size of 5 MB.

		By default, the file is uploaded to the current project. Use %[1]s--repo%[1]s
		to target another project.
		`, "`"),
		Example: heredoc.Doc(`
			# Upload a secure file from a local path
			glab securefile create "newfile.txt" "securefiles/localfile.txt"

			# Upload using the 'upload' alias
			glab securefile upload "newfile.txt" "securefiles/localfile.txt"

			# Upload to another project
			glab securefile create "newfile.txt" "securefiles/localfile.txt" -R owner/repo
		`),
		Aliases: []string{"upload"},
		Args:    cobra.ExactArgs(2),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(args)

			return opts.run()
		},
	}
	return securefileCreateCmd
}

func (o *options) complete(args []string) {
	o.fileName = args[0]
	o.inputFilePath = args[1]
}

func (o *options) run() error {
	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	repo, err := o.baseRepo()
	if err != nil {
		return err
	}

	color := o.io.Color()
	o.io.LogInfof("%s Creating secure file %s=%s %s=%s\n",
		color.ProgressIcon(),
		color.Blue("repo"), repo.FullName(),
		color.Blue("fileName"), o.fileName)

	reader, err := helpers.GetReaderFromFilePath(o.inputFilePath)
	if err != nil {
		return fmt.Errorf("unable to read file at %s: %w", o.inputFilePath, err)
	}

	_, _, err = client.SecureFiles.CreateSecureFile(repo.FullName(), reader, &gitlab.CreateSecureFileOptions{Name: new(o.fileName)})
	if err != nil {
		return fmt.Errorf("error creating secure file: %w", err)
	}

	o.io.LogInfof(color.Bold("%s Secure file %s created.\n"), color.GreenCheck(), o.fileName)
	return nil
}
