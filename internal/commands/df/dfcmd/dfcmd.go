package dfcmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/pm"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

// ProxySpec describes one package-manager wrapper command. The wrapper
// forwards all args to the package manager binary through the Dependency
// Firewall inspection proxy.
type ProxySpec struct {
	Manager func() pm.PackageManager
	Use     string
	Short   string
	Long    string
	Example string
}

type options struct {
	io           *iostreams.IOStreams
	executor     cmdutils.Executor
	baseRepo     func() (host, projectID string, err error)
	gitlabClient func() (*gitlab.Client, error)
	manager      func() pm.PackageManager
	name         string

	args    []string
	baseDir string
}

// NewProxyCmd builds a df package-manager wrapper command from spec.
func NewProxyCmd(f cmdutils.Factory, spec ProxySpec) *cobra.Command {
	opts := &options{
		io:       f.IO(),
		executor: f.Executor(),
		baseRepo: func() (string, string, error) {
			r, err := f.BaseRepo()
			if err != nil {
				return "", "", err
			}
			return r.RepoHost(), r.FullName(), nil
		},
		gitlabClient: f.GitLabClient,
		manager:      spec.Manager,
		name:         firstWord(spec.Use),
	}

	return &cobra.Command{
		Use:   spec.Use,
		Short: spec.Short + " (EXPERIMENTAL)",
		Annotations: map[string]string{
			mcpannotations.Exclude: "true",
		},
		DisableFlagParsing: true,
		Long:               spec.Long + text.ExperimentalString,
		Example:            spec.Example,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.args = args
			if err := opts.complete(); err != nil {
				return err
			}
			return opts.run(cmd.Context())
		},
	}
}

// firstWord returns the command name from a cobra Use string, e.g.
// "npm <npm args>" -> "npm".
func firstWord(use string) string {
	name, _, _ := strings.Cut(use, " ")
	return name
}

func (o *options) complete() error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	o.baseDir = wd
	return nil
}

func (o *options) run(ctx context.Context) error {
	_, projectID, err := o.baseRepo()
	if err != nil {
		return cmdutils.WrapError(err, "failed to resolve GitLab project from the git remote.")
	}

	client, err := o.gitlabClient()
	if err != nil {
		return cmdutils.WrapError(err, "failed to create a GitLab API client.")
	}

	if err := pm.Run(ctx, o.manager(), pm.RunOptions{
		IO:        o.io,
		Executor:  o.executor,
		BaseDir:   o.baseDir,
		Client:    client,
		ProjectID: projectID,
		Args:      o.args,
	}); err != nil {
		if ee, ok := errors.AsType[*pm.ExitError](err); ok {
			return cmdutils.WrapErrorWithCode(ee.Unwrap(), ee.Code, ee.Error())
		}
		return cmdutils.WrapError(err, fmt.Sprintf("failed to run %s through the Dependency Firewall.", o.name))
	}
	return nil
}
