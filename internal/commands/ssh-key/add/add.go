package add

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	apiClient func(repoHost string) (*api.Client, error)
	io        *iostreams.IOStreams

	title     string
	key       string
	expiresAt string
	usageType string

	expiresAtTime *gitlab.ISOTime
	replace       bool

	keyFile string
}

func NewCmdAdd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
	}
	cmd := &cobra.Command{
		Use:   "add [key-file]",
		Short: "Add an SSH key to your GitLab account.",
		Long: heredoc.Doc(`
		Creates a new SSH key owned by the currently authenticated user.

		Requires the '--title' flag.

		GitLab rejects a key whose fingerprint is already registered on your account, even when that key has expired. Use '--replace' to delete the existing key with the same SHA256 fingerprint and add it again with the new title, usage type, and expiration date.
		`),
		Example: heredoc.Doc(`
			# Read ssh key from stdin and upload.
			glab ssh-key add -t "my title"

			# Read ssh key from specified key file, upload and set the ssh key type to "authentication".
			glab ssh-key add ~/.ssh/id_ed25519.pub -t "my title" --usage-type "auth"

			# Re-register an expired key with a new expiration date.
			glab ssh-key add ~/.ssh/id_ed25519.pub -t "my title" --expires-at 2027-01-01 --replace`),
		Args: cobra.MaximumNArgs(1),
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
			if len(args) != 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			// .pub filenames would be desirable, but FilterFileExt breaks bash completion when a command has required flags
			return nil, cobra.ShellCompDirectiveDefault
		},
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(args); err != nil {
				return err
			}

			return opts.run(cmd.Context())
		},
	}

	fl := cmd.Flags()
	fl.StringVarP(&opts.title, "title", "t", "", "New SSH key's title.")
	fl.VarP(cmdutils.NewEnumValue([]string{"auth", "signing", "auth_and_signing"}, "auth_and_signing", &opts.usageType), "usage-type", "u", "Usage scope for the key. Possible values: 'auth', 'signing' or 'auth_and_signing'. Default value: 'auth_and_signing'.")
	fl.StringVarP(&opts.expiresAt, "expires-at", "e", "", "The expiration date of the SSH key. Uses ISO 8601 format: YYYY-MM-DDTHH:MM:SSZ.")
	fl.BoolVar(&opts.replace, "replace", false, "Delete an existing key with the same fingerprint, such as an expired key, before adding this one.")

	_ = cmd.MarkFlagRequired("title")

	return cmd
}

func (o *options) complete(args []string) error {
	if len(args) == 0 {
		if o.io.IsInteractive() {
			return &cmdutils.FlagError{Err: errors.New("missing key file")}
		}
		o.keyFile = "-"
	} else {
		o.keyFile = args[0]
	}

	if o.expiresAt != "" {
		expiresAt, err := parseExpiresAt(o.expiresAt)
		if err != nil {
			return &cmdutils.FlagError{Err: fmt.Errorf("invalid --expires-at value %q: %w", o.expiresAt, err)}
		}
		o.expiresAtTime = expiresAt
	}

	return nil
}

func (o *options) run(ctx context.Context) error {
	c, err := o.apiClient("")
	if err != nil {
		return err
	}
	client := c.Lab()

	var keyFileReader io.Reader
	if o.keyFile == "-" {
		keyFileReader = o.io.In
		defer o.io.In.Close()
	} else {
		f, err := os.Open(o.keyFile)
		if err != nil {
			return err
		}
		defer f.Close()

		keyFileReader = f
	}

	keyInBytes, err := io.ReadAll(keyFileReader)
	if err != nil {
		return cmdutils.WrapError(err, "failed to read SSH key file.")
	}

	o.key = string(keyInBytes)

	var replacedKeyID int64
	if o.replace {
		replacedKeyID, err = o.deleteKeyWithSameFingerprint(ctx, client)
		if err != nil {
			return err
		}
	}

	err = UploadSSHKey(ctx, client, o.title, o.key, o.usageType, o.expiresAtTime)
	if err != nil {
		if replacedKeyID != 0 {
			return cmdutils.WrapError(err, fmt.Sprintf("failed to add new SSH public key. The existing key with ID %d was already deleted.", replacedKeyID))
		}
		return cmdutils.WrapError(err, "failed to add new SSH public key.")
	}

	if o.io.IsOutputTTY() {
		cs := o.io.Color()
		o.io.LogInfof("%s New SSH public key added to your account.\n", cs.GreenCheck())
	} else {
		o.io.LogInfo("New SSH public key added to your account.\n")
	}

	return nil
}

func (o *options) deleteKeyWithSameFingerprint(ctx context.Context, client *gitlab.Client) (int64, error) {
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(o.key))
	if err != nil {
		return 0, cmdutils.WrapError(err, "failed to parse SSH public key.")
	}

	existing, err := findSSHKeyByFingerprint(ctx, client, ssh.FingerprintSHA256(pub))
	if err != nil {
		return 0, cmdutils.WrapError(err, "failed to list existing SSH keys.")
	}
	if existing == nil {
		return 0, nil
	}

	if _, err := client.Users.DeleteSSHKey(existing.ID, gitlab.WithContext(ctx)); err != nil {
		return 0, cmdutils.WrapError(err, fmt.Sprintf("failed to delete existing SSH key with ID %d.", existing.ID))
	}
	o.io.LogInfof("Deleted existing SSH key %q (ID %d) with the same fingerprint.\n", existing.Title, existing.ID)

	return existing.ID, nil
}
