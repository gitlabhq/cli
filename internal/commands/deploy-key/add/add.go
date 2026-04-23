package add

import (
	"errors"
	"io"
	"os"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	gitlabClient func() (*gitlab.Client, error)
	io           *iostreams.IOStreams
	baseRepo     func() (glrepo.Interface, error)

	title     string
	key       string
	expiresAt string
	canPush   bool

	keyFile string
}

func NewCmdAdd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
		baseRepo:     f.BaseRepo,
	}
	cmd := &cobra.Command{
		Use:   "add [key-file]",
		Short: "Add a deploy key to a GitLab project.",
		Long: heredoc.Doc(`
		Creates a new deploy key.

		Requires the '--title' flag.
		`),
		Example: heredoc.Doc(`
			# Read deploy key from stdin and upload.
			glab deploy-key add -t "my title"

			# Read deploy key from specified key file and upload
			cat ~/.ssh/id_ed25519.pub | glab deploy-key add --title='test' -

			# Read deploy key from specified key file, upload and set "can push" attribute.
			glab deploy-key add ~/.ssh/id_ed25519.pub -t "my title" --can-push true`),
		Args: cobra.MaximumNArgs(1),
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

	cmd.Flags().StringVarP(&opts.title, "title", "t", "", "New deploy key's title.")
	cmd.Flags().BoolVarP(&opts.canPush, "can-push", "c", false, "If true, deploy keys can be used for pushing code to the repository. (default false)")
	cmd.Flags().StringVarP(&opts.expiresAt, "expires-at", "e", "", "The expiration date of the deploy key, using the ISO-8601 format: YYYY-MM-DDTHH:MM:SSZ.")

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
		return cmdutils.WrapError(err, "failed to read deploy key file.")
	}

	o.key = string(keyInBytes)

	baseRepo, err := o.baseRepo()
	if err != nil {
		return err
	}

	err = UploadDeployKey(client, baseRepo.FullName(), o.title, o.key, o.canPush, o.expiresAt)
	if err != nil {
		return cmdutils.WrapError(err, "failed to add new deploy key.")
	}

	if o.io.IsOutputTTY() {
		cs := o.io.Color()
		o.io.LogInfof("%s New deploy key added.\n", cs.GreenCheck())
	} else {
		o.io.LogInfo("New deploy key added.\n")
	}

	return nil
}
