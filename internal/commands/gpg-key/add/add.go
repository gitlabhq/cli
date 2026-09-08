package add

import (
	"io"
	"os"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	gitlabClient func() (*gitlab.Client, error)
	io           *iostreams.IOStreams

	keyFile string
}

func NewCmdAdd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
	}
	cmd := &cobra.Command{
		Use:   "add [key-file]",
		Short: "Add a GPG key to your GitLab account.",
		Long: heredoc.Doc(`
		Creates a new GPG key owned by the currently authenticated user.
		`),
		Example: heredoc.Doc(`
			# Read GPG key from stdin and upload.
			glab gpg-key add

			# Read GPG key from specified key file and upload.
			glab gpg-key add ~/.gnupg/pubkey.asc`),
		Args: cobra.MaximumNArgs(1), // Allow 0 or 1 args to support stdin or file
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
	if len(args) == 0 {
		o.keyFile = "-"
	} else {
		o.keyFile = args[0]
	}

	return nil
}

func (o *options) run() error {
	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	var keyFileReader io.Reader
	if o.keyFile == "-" {
		keyFileReader = o.io.In
	} else {
		f, err := os.Open(o.keyFile)
		if err != nil {
			return err
		}
		defer func() {
			cerr := f.Close()
			if err == nil {
				err = cerr
			}
		}()

		keyFileReader = f
	}

	keyInBytes, err := io.ReadAll(keyFileReader)
	if err != nil {
		return cmdutils.WrapError(err, "failed to read GPG key file.")
	}

	gpgKeyAddOptions := &gitlab.AddGPGKeyOptions{
		Key: new(string(keyInBytes)),
	}
	_, _, err = client.Users.AddGPGKey(gpgKeyAddOptions)
	if err != nil {
		return cmdutils.WrapError(err, "failed to add new GPG key.")
	}

	cs := o.io.Color()
	o.io.LogInfof("%s New GPG key added to your account.", cs.GreenCheck())

	return nil
}
