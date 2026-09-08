package delete

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

type options struct {
	gitlabClient func() (*gitlab.Client, error)
	io           *iostreams.IOStreams

	keyID int64
}

func NewCmdDelete(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
	}
	cmd := &cobra.Command{
		Use:   "delete <key-id>",
		Short: "Deletes a single GPG key specified by the ID.",
		Long: heredoc.Docf(`Pass the ID of the key to delete as an argument. Find key IDs by
		running %[1]sglab gpg-key list --show-id%[1]s.

		This action is permanent and cannot be undone.
		`, "`"),
		Example: heredoc.Doc(`
			# Delete GPG key with ID as argument
			glab gpg-key delete 7750633`),
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(args); err != nil {
				return err
			}
			return opts.run()
		},
	}

	return cmd
}

func (o *options) complete(args []string) error {
	if len(args) == 1 {
		o.keyID = int64(utils.StringToInt(args[0]))
	}

	return nil
}

func (o *options) run() error {
	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	_, err = client.Users.DeleteGPGKey(o.keyID)
	if err != nil {
		return cmdutils.WrapError(err, "failed to delete GPG key.")
	}

	cs := o.io.Color()
	o.io.LogInfof("%s GPG key deleted.", cs.GreenCheck())

	return nil
}
