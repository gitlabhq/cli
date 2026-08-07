package list

import (
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

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

	// Pagination
	page    int
	perPage int

	showKeyIDs   bool
	outputFormat string
}

func NewCmdList(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
		baseRepo:     f.BaseRepo,
	}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Get a list of deploy keys for the current project.",
		Long: heredoc.Docf(`Each row shows the key's title, key, push access, and creation date.
		Pass %[1]s--show-id%[1]s to also display the key ID, which the %[1]sget%[1]s and %[1]sdelete%[1]s
		commands accept as an argument. Use %[1]s--repo%[1]s to target a project other than
		the current one.
		`, "`"),
		Example: heredoc.Doc(`
			# List the current project's deploy keys
			glab deploy-key list

			# Include the key ID in the output
			glab deploy-key list --show-id`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.run()
		},
	}

	cmd.Flags().BoolVarP(&opts.showKeyIDs, "show-id", "", false, "Shows IDs of deploy keys.")
	cmd.Flags().IntVarP(&opts.page, "page", "p", 1, "Page number.")
	cmd.Flags().IntVarP(&opts.perPage, "per-page", "P", 30, "Number of items to list per page.")
	cmdutils.EnableJSONOutput(cmd, opts.io, &opts.outputFormat)

	return cmd
}

func (o *options) run() error {
	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	listProjectDeployKeysOptions := &gitlab.ListProjectDeployKeysOptions{
		ListOptions: gitlab.ListOptions{
			Page:    int64(o.page),
			PerPage: int64(o.perPage),
		},
	}

	baseRepo, err := o.baseRepo()
	if err != nil {
		return err
	}

	keys, _, err := client.DeployKeys.ListProjectDeployKeys(baseRepo.FullName(), listProjectDeployKeysOptions)
	if err != nil {
		return cmdutils.WrapError(err, "failed to get deploy keys.")
	}

	if o.outputFormat == "json" {
		return o.io.PrintJSON(keys)
	}

	cs := o.io.Color()
	table := tableprinter.NewTablePrinter()
	isTTy := o.io.IsOutputTTY()

	if len(keys) > 0 {
		if o.showKeyIDs {
			table.AddRow("ID", "Title", "Key", "Can Push", "Created At")
		} else {
			table.AddRow("Title", "Key", "Can Push", "Created At")
		}
	}

	for _, key := range keys {
		createdAt := key.CreatedAt.String()
		if o.showKeyIDs {
			table.AddCell(key.ID)
		}
		if isTTy {
			createdAt = utils.TimeToPrettyTimeAgo(*key.CreatedAt)
		}
		table.AddRow(key.Title, key.Key, key.CanPush, cs.Gray(createdAt))
	}

	o.io.LogInfo(table.String())

	return nil
}
