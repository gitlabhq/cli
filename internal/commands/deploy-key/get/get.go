package get

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/tableprinter"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

type options struct {
	gitlabClient func() (*gitlab.Client, error)
	io           *iostreams.IOStreams
	baseRepo     func() (glrepo.Interface, error)

	keyID        int64
	outputFormat string
}

func NewCmdGet(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
		baseRepo:     f.BaseRepo,
	}
	cmd := &cobra.Command{
		Use:   "get <key-id>",
		Short: "Returns a single deploy key specified by the ID.",
		Long: heredoc.Docf(`Pass the ID of the key to return as an argument. Find key IDs by
		running %[1]sglab deploy-key list --show-id%[1]s. Use %[1]s--repo%[1]s to target a
		project other than the current one.

		By default, the command prints the key's title, public key, push access,
		and creation date. Use %[1]s--output json%[1]s to return the full key object.
		`, "`"),
		Example: heredoc.Doc(`
			# Get deploy key with ID as argument
			glab deploy-key get 1234`),
		Args: cobra.ExactArgs(1),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(args)

			return opts.run()
		},
	}

	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)

	return cmd
}

func (o *options) complete(args []string) {
	if len(args) == 1 {
		o.keyID = int64(utils.StringToInt(args[0]))
	}
}

func (o *options) run() error {
	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	baseRepo, err := o.baseRepo()
	if err != nil {
		return err
	}

	key, _, err := client.DeployKeys.GetDeployKey(baseRepo.FullName(), o.keyID, nil)
	if err != nil {
		return cmdutils.WrapError(err, "getting deploy key.")
	}

	if o.outputFormat == "json" {
		return o.io.PrintJSON(key)
	}

	if key.ID != 0 {
		table := tableprinter.NewTablePrinter()
		table.AddRow("Title", "Key", "Can Push", "Created At")
		table.AddRow(key.Title, key.Key, key.CanPush, key.CreatedAt)
		o.io.LogInfo(table.String())
	} else {
		o.io.LogInfo("Deploy key does not exist.")
	}

	return nil
}
