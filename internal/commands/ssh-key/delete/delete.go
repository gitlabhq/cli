package delete

import (
	"context"
	"errors"
	"fmt"

	"charm.land/huh/v2"
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

type options struct {
	apiClient func(repoHost string) (*api.Client, error)
	io        *iostreams.IOStreams

	keyID   int64
	perPage int
	page    int
}

func NewCmdDelete(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
	}
	cmd := &cobra.Command{
		Use:   "delete [<key-id>]",
		Short: "Deletes a single SSH key specified by the ID.",
		Long: heredoc.Docf(`Pass the key ID as an argument, or run the command without arguments
		to select a key interactively. Find key IDs by running %[1]sglab ssh-key list --show-id%[1]s.

		This action is permanent and cannot be undone.
		`, "`"),
		Example: heredoc.Doc(`
			# Delete SSH key with ID as argument
			glab ssh-key delete 7750633

			# Interactive
			glab ssh-key delete

			# Interactive, with pagination
			glab ssh-key delete -P 50 -p 2`),
		Args: cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(cmd.Context(), args); err != nil {
				return err
			}
			return opts.run()
		},
	}

	cmd.Flags().IntVarP(&opts.page, "page", "p", 1, "Page number.")
	cmd.Flags().IntVarP(&opts.perPage, "per-page", "P", 30, "Number of items to list per page.")

	return cmd
}

func (o *options) complete(ctx context.Context, args []string) error {
	if len(args) == 0 {
		keyID, err := keySelectPrompt(ctx, o)
		if err != nil {
			return err
		}
		o.keyID = keyID
	}

	if len(args) == 1 {
		o.keyID = int64(utils.StringToInt(args[0]))
	}

	return nil
}

func (o *options) run() error {
	c, err := o.apiClient("")
	if err != nil {
		return err
	}
	client := c.Lab()

	_, err = client.Users.DeleteSSHKey(o.keyID)
	if err != nil {
		return cmdutils.WrapError(err, "deleting SSH key.")
	}

	if o.io.IsOutputTTY() {
		cs := o.io.Color()
		o.io.LogInfof("%s SSH key deleted.\n", cs.GreenCheck())
	} else {
		o.io.LogInfo("SSH key deleted.\n")
	}

	return nil
}

func keySelectPrompt(ctx context.Context, opts *options) (int64, error) {
	if !opts.io.PromptEnabled() {
		return 0, cmdutils.FlagError{Err: errors.New("the <key-id> argument is required when prompts are disabled")}
	}

	sshKeyListOptions := &gitlab.ListSSHKeysOptions{
		ListOptions: gitlab.ListOptions{
			PerPage: int64(opts.perPage),
			Page:    int64(opts.page),
		},
	}

	c, err := opts.apiClient("")
	if err != nil {
		return 0, err
	}
	client := c.Lab()

	keys, resp, err := client.Users.ListSSHKeys(sshKeyListOptions)
	if err != nil {
		return 0, cmdutils.WrapError(err, "Retrieving list of SSH keys.")
	}
	if len(keys) == 0 {
		return 0, cmdutils.WrapError(errors.New("no keys found"), "Retrieving list of SSH keys.")
	}

	options := make([]huh.Option[int64], 0, len(keys))
	for _, key := range keys {
		options = append(options, huh.NewOption(key.Title, key.ID))
	}

	var selectedID int64
	selector := huh.NewSelect[int64]().
		Title(fmt.Sprintf(
			"Select key to delete - Showing %d/%d keys - page %d/%d",
			len(keys),
			resp.TotalItems,
			opts.page,
			resp.TotalPages,
		)).
		Options(options...).
		Value(&selectedID)

	err = opts.io.Run(ctx, selector)
	if err != nil {
		return 0, cmdutils.WrapError(err, "prompting for SSH key to delete.")
	}

	return selectedID, nil
}
