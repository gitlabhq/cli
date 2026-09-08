package get

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/tableprinter"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

type options struct {
	gitlabClient func() (*gitlab.Client, error)
	io           *iostreams.IOStreams

	keyID        int64
	outputFormat string
}

func NewCmdGet(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
	}
	cmd := &cobra.Command{
		Use:   "get <key-id>",
		Short: "Returns a single GPG key specified by the ID.",
		Long: heredoc.Docf(`Pass the ID of the key to return as an argument. Find key IDs by
		running %[1]sglab gpg-key list --show-id%[1]s.

		By default, the command prints the key's ID, public key, and creation date.
		Use %[1]s--output json%[1]s to return the full key object.
		`, "`"),
		Example: heredoc.Doc(`
			# Get GPG key with ID as argument
			glab gpg-key get 7750633`),
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(args); err != nil {
				return err
			}

			return opts.run()
		},
	}

	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)

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

	key, _, err := client.Users.GetGPGKey(o.keyID)
	if err != nil {
		return cmdutils.WrapError(err, "failed to get GPG key.")
	}

	if o.outputFormat == "json" {
		return o.io.PrintJSON(key)
	}

	o.io.LogInfof("Showing GPG key with ID %d\n", key.ID)

	if key.ID != 0 {
		table := tableprinter.NewTablePrinter()
		table.AddRow("ID", key.ID)
		table.AddRow("Key", key.Key)
		table.AddRow("Created At", utils.TimeToPrettyTimeAgo(*key.CreatedAt))
		o.io.LogInfo(table.String())
	} else {
		o.io.LogInfo("GPG key does not exist.")
	}

	return nil
}
