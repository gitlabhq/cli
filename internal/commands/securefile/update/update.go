package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"

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
	forceUpdate   bool
	fileName      string
	inputFilePath string

	io           *iostreams.IOStreams
	gitlabClient func() (*gitlab.Client, error)
	baseRepo     func() (glrepo.Interface, error)
}

func NewCmdUpdate(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
		baseRepo:     f.BaseRepo,
	}
	securefileUpdateCmd := &cobra.Command{
		Use:   "update <name> <path>",
		Short: `Update a secure file in a project.`,
		Long: heredoc.Docf(`
		Update a secure file in a project, identified by its name.
		The command asks for confirmation before updating; use %[1]s-y%[1]s to skip
		the prompt in scripts.

		By default, the file is updated in the current project. Use %[1]s--repo%[1]s
		to target another project.

		If the file content is unchanged, no update is performed.

		Updating a secure file changes its ID. When you download the file afterward, reference it by %[1]s--name%[1]s instead of %[1]s--id%[1]s.
		`, "`"),
		Aliases: []string{"overwrite"},
		Args:    cobra.ExactArgs(2),
		Example: heredoc.Doc(`
			# Update a secure file
			glab securefile update "file.txt" securefiles/localfile.txt

			# Skip the confirmation prompt
			glab securefile update "file.txt" securefiles/localfile.txt -y

			# Use the 'overwrite' alias
			glab securefile overwrite "file.txt" securefiles/localfile.txt
		`),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(args)

			if err := opts.validate(); err != nil {
				return err
			}

			return opts.run(cmd.Context())
		},
	}

	securefileUpdateCmd.Flags().BoolVarP(&opts.forceUpdate, "yes", "y", false, "Skip the confirmation prompt.")

	return securefileUpdateCmd
}

func (o *options) complete(args []string) {
	o.fileName = args[0]
	o.inputFilePath = args[1]
}

func (o *options) validate() error {
	if !o.forceUpdate && !o.io.PromptEnabled() {
		return &cmdutils.FlagError{Err: fmt.Errorf("--yes or -y flag is required when not running interactively")}
	}

	return nil
}

func (o *options) run(ctx context.Context) error {
	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	repo, err := o.baseRepo()
	if err != nil {
		return err
	}

	content, err := os.ReadFile(o.inputFilePath)
	if err != nil {
		return fmt.Errorf("unable to read file at %s: %w", o.inputFilePath, err)
	}

	secureFile, err := helpers.GetSecureFileByName(client, o.fileName, repo.FullName())
	if err != nil {
		return err
	}

	color := o.io.Color()

	if secureFile.ChecksumAlgorithm == "sha256" {
		if checksum := fmt.Sprintf("%x", sha256.Sum256(content)); checksum == secureFile.Checksum {
			o.io.LogInfof(color.Bold("%s Secure file %s is already up to date.\n"), color.GreenCheck(), o.fileName)
			return nil
		}
	}

	if !o.forceUpdate && o.io.PromptEnabled() {
		o.io.LogInfof("This action will update the existing secure file %s immediately, and its ID will change. Any scripts using `securefile download --id` will stop working; use `--name` instead.\n\n", o.fileName)
		err = o.io.Confirm(ctx, &o.forceUpdate, fmt.Sprintf("Are you ABSOLUTELY SURE you wish to update this secure file %s?", o.fileName))
		if err != nil {
			return cmdutils.WrapError(err, "could not prompt")
		}
	}

	if !o.forceUpdate {
		return cmdutils.CancelError()
	}

	o.io.LogInfof("%s Updating secure file %s=%s %s=%s\n",
		color.ProgressIcon(),
		color.Blue("repo"), repo.FullName(),
		color.Blue("fileName"), o.fileName)

	_, err = client.SecureFiles.RemoveSecureFile(repo.FullName(), secureFile.ID)
	if err != nil {
		return fmt.Errorf("error removing secure file: %w", err)
	}

	newFile, _, err := client.SecureFiles.CreateSecureFile(repo.FullName(), bytes.NewReader(content), &gitlab.CreateSecureFileOptions{Name: new(o.fileName)})
	if err != nil {
		return fmt.Errorf("the existing secure file %q was removed but the new version could not be uploaded, so it must be re-created manually: %w", o.fileName, err)
	}

	o.io.LogInfof(color.Bold("%s Secure file %s updated.\n"), color.GreenCheck(), o.fileName)
	o.io.LogInfof("%s The secure file ID changed to %d. Any scripts using `securefile download --id` must be updated, or switch to `securefile download --name %s`.\n",
		color.WarnIcon(), newFile.ID, o.fileName)

	return nil
}
