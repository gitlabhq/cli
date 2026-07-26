package delete

import (
	"fmt"
	"strconv"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	io           *iostreams.IOStreams
	gitlabClient func() (*gitlab.Client, error)
	baseRepo     func() (glrepo.Interface, error)

	keyID int64
}

func NewCmdDelete(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
		baseRepo:     f.BaseRepo,
	}
	cmd := &cobra.Command{
		Use:   "delete <key-id>",
		Short: "Deletes a single deploy key specified by the ID.",
		Long: heredoc.Docf(`Pass the ID of the key to delete as an argument. Find key IDs by
		running %[1]sglab deploy-key list --show-id%[1]s. Use %[1]s--repo%[1]s to target a
		project other than the current one.

		This action is permanent and cannot be undone.
		`, "`"),
		Example: heredoc.Doc(`
			# Delete deploy key with ID as argument
			glab deploy-key delete 1234`),
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
	// cobra.ExactArgs(1) guarantees exactly one argument, so no length guard is
	// needed here. A guard would leave an unreachable branch that silently left
	// keyID at zero, which is the bug this command had.
	strInt, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Errorf("deploy key ID must be an integer: %s", args[0])
	}
	o.keyID = int64(strInt)

	return nil
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

	_, err = client.DeployKeys.DeleteDeployKey(baseRepo.FullName(), o.keyID)
	if err != nil {
		return cmdutils.WrapError(err, "deleting deploy key.")
	}

	if o.io.IsOutputTTY() {
		cs := o.io.Color()
		o.io.LogInfof("%s Deploy key deleted.\n", cs.GreenCheck())
	} else {
		o.io.LogInfo("Deploy key deleted.\n")
	}

	return nil
}
