package serve

import (
	"context"

	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/session"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

type options struct {
	io           *iostreams.IOStreams
	baseRepo     func() (glrepo.Interface, error)
	gitlabClient func() (*gitlab.Client, error)
	config       func() config.Config

	stateDir string
	baseDir  string
}

// NewCmd returns the detached daemon `job-proxy start` spawns. It is hidden
// because it is an implementation detail: it blocks for the life of the job and
// writes its state under --state-dir.
func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:           f.IO(),
		baseRepo:     f.BaseRepo,
		gitlabClient: f.GitLabClient,
		config:       f.Config,
	}
	cmd := &cobra.Command{
		Use:               "serve",
		Short:             "Run the job-level proxy in the foreground (internal).",
		Hidden:            true,
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		Annotations:       map[string]string{mcpannotations.Exclude: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(); err != nil {
				return err
			}
			return opts.run(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&opts.stateDir, "state-dir", "", "Directory for the proxy's runtime state.")
	cobra.CheckErr(cmd.MarkFlagRequired("state-dir"))
	return cmd
}

// complete anchors the CI log to the project directory rather than wherever
// the job later changes to. `job-proxy start` exports the same directory so
// ci-summary reads it.
func (o *options) complete() error {
	dir, err := session.LogBaseDir()
	if err != nil {
		return err
	}
	o.baseDir = dir
	return nil
}

func (o *options) run(ctx context.Context) error {
	r, err := o.baseRepo()
	if err != nil {
		return cmdutils.WrapError(err, "failed to resolve GitLab project from the git remote.")
	}
	client, err := o.gitlabClient()
	if err != nil {
		return cmdutils.WrapError(err, "failed to create a GitLab API client.")
	}
	caCert, err := o.config().Get(r.RepoHost(), "ca_cert")
	if err != nil {
		return cmdutils.WrapError(err, "failed to read ca_cert from the glab configuration.")
	}
	return session.Serve(ctx, session.ServeConfig{
		Client:     client,
		ProjectID:  r.FullName(),
		BaseDir:    o.baseDir,
		StateDir:   o.stateDir,
		CACertPath: caCert,
		IO:         o.io,
	})
}
