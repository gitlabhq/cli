package init

import (
	"context"
	"errors"
	"fmt"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

var errUnsupportedAuth = errors.New("init command does not support this authentication method")

type options struct {
	io        *iostreams.IOStreams
	baseRepo  func() (glrepo.Interface, error)
	apiClient func(repoHost string) (*api.Client, error)
	exec      cmdutils.Executor

	stateName string
	binary    string
	directory string
	initArgs  []string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		baseRepo:  f.BaseRepo,
		apiClient: f.ApiClient,
		exec:      f.Executor(),
	}

	cmd := &cobra.Command{
		Use:   "init <state> [flags]",
		Short: `Initialize OpenTofu or Terraform.`,
		Long: heredoc.Docf(`
			Configures the GitLab HTTP backend for OpenTofu or Terraform state
			and runs %[1]stofu init%[1]s. You must run this command from a GitLab
			project repository.
		`, "`"),
		Example: heredoc.Doc(`
			# Initialize state with name production in working directory
			glab opentofu init production

			# Initialize state with name production in infra/ directory
			glab opentofu init production -d infra/

			# Initialize state with name production with Terraform
			glab opentofu init production -b terraform

			# Initialize state with name production with reconfiguring state
			glab opentofu init production -- -reconfigure`),
		Args: cobra.MinimumNArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(args)

			return opts.run(cmd.Context())
		},
	}

	fl := cmd.Flags()
	fl.StringVarP(&opts.binary, "binary", "b", "tofu", "Name or path of the OpenTofu or Terraform binary to use for the initialization.")
	fl.StringVarP(&opts.directory, "directory", "d", ".", "Directory of the OpenTofu or Terraform project to initialize.")

	return cmd
}

func (o *options) complete(args []string) {
	o.stateName = args[0]
	o.initArgs = args[1:]
}

func (o *options) run(ctx context.Context) error {
	repo, err := o.baseRepo()
	if err != nil {
		return err
	}

	apiClient, err := o.apiClient(repo.RepoHost())
	if err != nil {
		return err
	}

	client := apiClient.Lab()
	baseURL := client.BaseURL()
	stateAPIURL := baseURL.JoinPath("projects", gitlab.PathEscape(repo.FullName()), "terraform", "state", gitlab.PathEscape(o.stateName))
	args := []string{
		fmt.Sprintf(`-chdir=%s`, o.directory),
		"init",
		fmt.Sprintf(`-backend-config=address=%s`, stateAPIURL.String()),
		fmt.Sprintf(`-backend-config=lock_address=%s`, stateAPIURL.JoinPath("lock").String()),
		fmt.Sprintf(`-backend-config=unlock_address=%s`, stateAPIURL.JoinPath("lock").String()),
		`-backend-config=lock_method=POST`,
		`-backend-config=unlock_method=DELETE`,
		`-backend-config=retry_wait_min=5`,
	}

	authArgs, err := authBackendConfig(ctx, apiClient)
	if err != nil {
		return err
	}
	args = append(args, authArgs...)
	args = append(args, o.initArgs...)

	return o.exec.Exec(ctx, o.binary, args, nil)
}

// authBackendConfig renders the backend configuration OpenTofu needs to
// authenticate against the state API.
func authBackendConfig(ctx context.Context, apiClient *api.Client) ([]string, error) {
	// Password auth carries no token and needs a username from the API, so it
	// cannot come from Credential.
	if as, ok := apiClient.AuthSource().(*gitlab.PasswordCredentialsAuthSource); ok {
		currentUser, _, err := apiClient.Lab().Users.CurrentUser(gitlab.WithContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("unable to retrieve current user: %w", err)
		}

		return []string{
			fmt.Sprintf(`-backend-config=username=%s`, currentUser.Username),
			fmt.Sprintf(`-backend-config=password=%s`, as.Password),
		}, nil
	}

	cred, err := apiClient.Credential(ctx)
	switch {
	case errors.Is(err, api.ErrUnsupportedAuthSource):
		// Name the requirement rather than passing on the wrapped Go type.
		return nil, errUnsupportedAuth
	case errors.Is(err, api.ErrUnauthenticated):
		// Already actionable, and wrapping it reads as two errors.
		return nil, err
	case err != nil:
		return nil, fmt.Errorf("unable to retrieve an access token to authenticate OpenTofu: %w", err)
	}

	switch cred.Kind {
	case api.CredentialJobToken:
		return []string{fmt.Sprintf(`-backend-config=headers={"%s" = "%s"}`, gitlab.JobTokenHeaderName, cred.Token)}, nil
	case api.CredentialPAT, api.CredentialOAuth2:
		return []string{fmt.Sprintf(`-backend-config=headers={"Authorization" = "Bearer %s"}`, cred.Token)}, nil
	default:
		return nil, fmt.Errorf("%w: %s", errUnsupportedAuth, cred.Kind)
	}
}
