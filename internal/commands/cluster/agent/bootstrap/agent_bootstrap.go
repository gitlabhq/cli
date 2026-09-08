package bootstrap

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -typed -destination=./mocks_for_test.go -package=bootstrap gitlab.com/gitlab-org/cli/internal/commands/cluster/agent/bootstrap API,FluxWrapper,KubectlWrapper,Cmd
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -typed -destination=./stdlib_mocks_for_test.go -package=bootstrap "io" "Writer"

type API interface {
	GetDefaultBranch() (string, error)
	GetAgentByName(name string) (*gitlab.Agent, error)
	RegisterAgent(name string) (*gitlab.Agent, error)
	ConfigureAgent(agent *gitlab.Agent, branch string) error
	ConfigureEnvironment(agentID int64, name string, kubernetesNamespace string, fluxResourcePath string) error
	CreateAgentToken(agentID int64) (*gitlab.AgentToken, error)
	SyncFile(f file, branch string) error
	GetKASAddress() (string, error)
}

type FluxWrapper interface {
	createHelmRepositoryManifest() (file, error)
	createHelmReleaseManifest(kasAddress string) (file, error)
	reconcile() error
}

type KubectlWrapper interface {
	createAgentTokenSecret(tokenID int64, token string) error
}

type (
	APIFactory            func(*gitlab.Client, any, *commitAuthor) API
	KubectlWrapperFactory func(Cmd, string, string, string) KubectlWrapper
	FluxWrapperFactory    func(Cmd, string, string, string, string, string, string, string, string, string, string, []string, []string, string, string, string) FluxWrapper
	CmdFactory            func(io.Writer, io.Writer, []string) Cmd
)

var reconcileErr = errors.New("failed to reconcile the GitLab Agent")

const (
	kubectlBinaryName = "kubectl"
	fluxBinaryName    = "flux"

	defaultCommitAuthorName  = "glab"
	defaultCommitAuthorEmail = "noreply@glab.gitlab.com"
)

type options struct {
	manifestPath   string
	manifestBranch string

	noReconcile bool

	helmRepositoryAddress   string
	helmRepositoryName      string
	helmRepositoryNamespace string
	helmRepositoryFilepath  string

	helmReleaseName            string
	helmReleaseNamespace       string
	helmReleaseFilepath        string
	helmReleaseTargetNamespace string
	helmReleaseValues          []string
	helmReleaseValuesFrom      []string

	gitlabAgentTokenSecretName string

	fluxSourceType      string
	fluxSourceNamespace string
	fluxSourceName      string

	createEnvironment           bool
	environmentName             string
	environmentNamespace        string
	environmentFluxResourcePath string

	createFluxEnvironment           bool
	fluxEnvironmentName             string
	fluxEnvironmentNamespace        string
	fluxEnvironmentFluxResourcePath string

	useAPICommitAuthor bool
	commitAuthor       commitAuthor
}

func EnsureRequirements() error {
	if _, err := exec.LookPath(kubectlBinaryName); err != nil {
		return fmt.Errorf("unable to find %s binary in PATH", kubectlBinaryName)
	}
	if _, err := exec.LookPath(fluxBinaryName); err != nil {
		return fmt.Errorf("unable to find %s binary in PATH", fluxBinaryName)
	}
	return nil
}

func NewCmdAgentBootstrap(f cmdutils.Factory, ensureRequirements func() error, af APIFactory, kwf KubectlWrapperFactory, fwf FluxWrapperFactory, cf CmdFactory) *cobra.Command {
	var opts options
	agentBootstrapCmd := &cobra.Command{
		Use:   "bootstrap <agent-name> [flags]",
		Short: `Bootstrap a GitLab Agent for Kubernetes in a project.`,
		Long: heredoc.Docf(`Bootstrap a GitLab Agent for Kubernetes (agentk) in a project.

The first argument must be the name of the agent.

It requires the %[1]skubectl%[1]s and %[1]sflux%[1]s commands to be accessible via %[1]s$PATH%[1]s.

This command consists of multiple idempotent steps:

1. Register the agent with the project.
2. Configure the agent.
3. Configure an environment with dashboard for the agent.
4. Configure an environment with dashboard for FluxCD (if %[1]s--create-flux-environment%[1]s).
5. Create a token for the agent.
   - If the agent has reached the maximum amount of tokens,
     the one that has not been used the longest is revoked
     and a new one is created.
   - If the agent has not reached the maximum amount of tokens,
     a new one is created.
6. Push the Kubernetes Secret that contains the token to the cluster.
7. Create Flux HelmRepository and HelmRelease resources.
8. Commit and Push the created Flux Helm resources to the manifest path.
9. Trigger Flux reconciliation of GitLab Agent HelmRelease (unless %[1]s--no-reconcile%[1]s).
`, "`"),
		Example: heredoc.Doc(`
			# Bootstrap "my-agent" to the root of the Git project in CWD, and trigger reconciliation
			glab cluster agent bootstrap my-agent

			# Bootstrap "my-agent" to "manifests/" of the Git project in CWD, and trigger reconciliation
			# Especially useful when "flux bootstrap gitlab --path manifests/" is used.
			# Make sure that the "--path" from the "flux bootstrap gitlab" command matches
			# the "--manifest-path" of the "glab cluster agent bootstrap" command.
			glab cluster agent bootstrap my-agent --manifest-path manifests/

			# Bootstrap "my-agent" to "manifests/" of the Git project in CWD, and do not manually trigger a reconilication
			glab cluster agent bootstrap my-agent --manifest-path manifests/ --no-reconcile

			# Bootstrap "my-agent" without configuring an environment
			glab cluster agent bootstrap my-agent --create-environment=false

			# Bootstrap "my-agent" and configure an environment with custom name and Kubernetes namespace
			glab cluster agent bootstrap my-agent --environment-name production --environment-namespace default

			# Bootstrap "my-agent" without configuring a FluxCD environment
			glab cluster agent bootstrap my-agent --create-flux-environment=false

			# Bootstrap "my-agent" and configure a FluxCD environment with custom name and Kubernetes namespace
			glab cluster agent bootstrap my-agent --flux-environment-name production-flux --flux-environment-namespace flux-system

			# Bootstrap "my-agent" and pass additional GitLab Helm Chart values from a local file
			glab cluster agent bootstrap my-agent --helm-release-values values.yaml

			# Bootstrap "my-agent" and pass additional GitLab Helm Chart values from a Kubernetes ConfigMap
			glab cluster agent bootstrap my-agent --helm-release-values-from ConfigMap/agent-config

			# Bootstrap "my-agent" and commit with custom user
			glab cluster agent bootstrap my-agent --commit-author-name="Tanuki" --commit-author-email="tanuki@example.com"

			# Bootstrap "my-agent" with authenticated API user
			glab cluster agent bootstrap my-agent --use-api-commit-author`),
		Aliases: []string{"bs"},
		Args:    cobra.ExactArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return ensureRequirements()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			stdout, stderr := f.IO().StdOut, f.IO().StdErr

			client, err := f.GitLabClient()
			if err != nil {
				return err
			}

			repo, err := f.BaseRepo()
			if err != nil {
				return err
			}

			var commitAuthor *commitAuthor
			if !opts.useAPICommitAuthor {
				commitAuthor = &opts.commitAuthor
			}
			api := af(client, repo.FullName(), commitAuthor)

			if opts.manifestBranch == "" {
				opts.manifestBranch, err = api.GetDefaultBranch()
				if err != nil {
					return err
				}
			}

			var agentEnvironmentCfg *environmentConfiguration
			if opts.createEnvironment {
				agentEnvironmentCfg = &environmentConfiguration{
					name:                fmt.Sprintf("%s/%s", opts.helmReleaseNamespace, opts.helmReleaseName),
					kubernetesNamespace: opts.helmReleaseTargetNamespace,
					fluxResourcePath:    fmt.Sprintf("helm.toolkit.fluxcd.io/v2beta1/namespaces/%s/helmreleases/%s", opts.helmReleaseNamespace, opts.helmReleaseName),
				}

				if cmd.Flags().Changed("environment-name") {
					agentEnvironmentCfg.name = opts.environmentName
				}

				if cmd.Flags().Changed("environment-namespace") {
					agentEnvironmentCfg.kubernetesNamespace = opts.environmentNamespace
				}

				if cmd.Flags().Changed("environment-flux-resource-path") {
					agentEnvironmentCfg.fluxResourcePath = opts.environmentFluxResourcePath
				}
			}

			var fluxEnvironmentCfg *environmentConfiguration
			if opts.createFluxEnvironment {
				fluxEnvironmentCfg = &environmentConfiguration{
					name:                fmt.Sprintf("%s/%s", opts.fluxSourceNamespace, opts.fluxSourceName),
					kubernetesNamespace: opts.fluxSourceNamespace,
					fluxResourcePath:    "kustomize.toolkit.fluxcd.io/v1/namespaces/flux-system/kustomizations/flux-system",
				}

				if cmd.Flags().Changed("flux-environment-name") {
					fluxEnvironmentCfg.name = opts.fluxEnvironmentName
				}

				if cmd.Flags().Changed("flux-environment-namespace") {
					fluxEnvironmentCfg.kubernetesNamespace = opts.fluxEnvironmentNamespace
				}

				if cmd.Flags().Changed("flux-environment-flux-resource-path") {
					fluxEnvironmentCfg.fluxResourcePath = opts.fluxEnvironmentFluxResourcePath
				}
			}

			c := cf(stdout, stderr, os.Environ())

			fluxWrapper := fwf(
				c, fluxBinaryName, opts.manifestPath,
				opts.helmRepositoryAddress,
				opts.helmRepositoryName, opts.helmRepositoryNamespace, opts.helmRepositoryFilepath,
				opts.helmReleaseName, opts.helmReleaseNamespace, opts.helmReleaseFilepath, opts.helmReleaseTargetNamespace,
				opts.helmReleaseValues, opts.helmReleaseValuesFrom,
				opts.fluxSourceType, opts.fluxSourceNamespace, opts.fluxSourceName,
			)

			return (&bootstrapCmd{
				api:                 api,
				stdout:              stdout,
				stderr:              stderr,
				agentName:           args[0],
				manifestBranch:      opts.manifestBranch,
				kubectl:             kwf(c, kubectlBinaryName, opts.helmReleaseTargetNamespace, opts.gitlabAgentTokenSecretName),
				flux:                fluxWrapper,
				noReconcile:         opts.noReconcile,
				agentEnvironmentCfg: agentEnvironmentCfg,
				fluxEnvironmentCfg:  fluxEnvironmentCfg,
			}).run()
		},
	}
	fl := agentBootstrapCmd.Flags()
	fl.StringVarP(&opts.manifestPath, "manifest-path", "p", "", "Location of directory in Git repository for storing the GitLab Agent for Kubernetes Helm resources.")
	fl.StringVarP(&opts.manifestBranch, "manifest-branch", "b", "", "Branch to commit the Flux Manifests to. Defaults to the project default branch.")

	fl.BoolVar(&opts.noReconcile, "no-reconcile", false, "Do not trigger Flux reconciliation for GitLab Agent for Kubernetes Flux resource.")

	// https://charts.gitlab.io is the GitLab’s official Helm charts repository address
	fl.StringVar(&opts.helmRepositoryAddress, "helm-repository-address", "https://charts.gitlab.io", "Address of the HelmRepository.")
	fl.StringVar(&opts.helmRepositoryName, "helm-repository-name", "gitlab", "Name of the Flux HelmRepository manifest.")
	fl.StringVar(&opts.helmRepositoryNamespace, "helm-repository-namespace", "flux-system", "Namespace of the Flux HelmRepository manifest.")
	fl.StringVar(&opts.helmRepositoryFilepath, "helm-repository-filepath", "gitlab-helm-repository.yaml", "File path within the GitLab Agent project to commit the Flux HelmRepository to.")

	fl.StringVar(&opts.helmReleaseName, "helm-release-name", "gitlab-agent", "Name of the Flux HelmRelease manifest.")
	fl.StringVar(&opts.helmReleaseNamespace, "helm-release-namespace", "flux-system", "Namespace of the Flux HelmRelease manifest.")
	fl.StringVar(&opts.helmReleaseFilepath, "helm-release-filepath", "gitlab-agent-helm-release.yaml", "File path within the GitLab Agent project to commit the Flux HelmRelease to.")
	fl.StringVar(&opts.helmReleaseTargetNamespace, "helm-release-target-namespace", "gitlab-agent", "Namespace of the GitLab Agent deployment.")
	fl.StringSliceVar(&opts.helmReleaseValues, "helm-release-values", nil, "Local path to values.yaml files. Multiple files can be comma-separated or specified by repeating the flag.")
	fl.StringSliceVar(&opts.helmReleaseValuesFrom, "helm-release-values-from", nil, "Kubernetes object reference that contains the values.yaml data key in the format '<kind>/<name>', where 'kind' must be one of: (Secret, ConfigMap). Multiple references can be comma-separated or specified by repeating the flag.")

	fl.StringVar(&opts.gitlabAgentTokenSecretName, "gitlab-agent-token-secret-name", "gitlab-agent-token", "Name of the Secret where the token for the GitLab Agent is stored. The helm-release-target-namespace is implied for the namespace of the Secret.")

	fl.StringVar(&opts.fluxSourceType, "flux-source-type", "git", "Source type of the flux-system, like Git, OCI, or Helm.")
	fl.StringVar(&opts.fluxSourceNamespace, "flux-source-namespace", "flux-system", "Flux source namespace.")
	fl.StringVar(&opts.fluxSourceName, "flux-source-name", "flux-system", "Flux source name.")

	fl.BoolVar(&opts.createEnvironment, "create-environment", true, "Create an environment for the GitLab Agent.")
	fl.StringVar(&opts.environmentName, "environment-name", "<helm-release-namespace>/<helm-release-name>", "Name of the environment for the GitLab Agent.")
	fl.StringVar(&opts.environmentNamespace, "environment-namespace", "<helm-release-namespace>", "Kubernetes namespace of the environment for the GitLab Agent.")
	fl.StringVar(&opts.environmentFluxResourcePath, "environment-flux-resource-path", "helm.toolkit.fluxcd.io/v2beta1/namespaces/<helm-release-namespace>/helmreleases/<helm-release-name>", "Flux resource path of the environment for the GitLab Agent.")

	fl.BoolVar(&opts.createFluxEnvironment, "create-flux-environment", true, "Create an environment for FluxCD. Affects only the environment creation, not the use of Flux itself. Flux is always required for the bootstrap process.")
	fl.StringVar(&opts.fluxEnvironmentName, "flux-environment-name", "<flux-source-namespace>/<flux-source-name>", "Name of the environment for FluxCD.")
	fl.StringVar(&opts.fluxEnvironmentNamespace, "flux-environment-namespace", "<flux-source-namespace>", "Kubernetes namespace of the environment for FluxCD.")
	fl.StringVar(&opts.fluxEnvironmentFluxResourcePath, "flux-environment-flux-resource-path", "kustomize.toolkit.fluxcd.io/v1/namespaces/flux-system/kustomizations/flux-system", "Flux resource path of the environment for FluxCD.")

	fl.BoolVar(&opts.useAPICommitAuthor, "use-api-commit-author", false, "When creating Git commits use the user from the authenticated API request. Conflicts with '--commit-author-name' and '--commit-author-email'.")
	fl.StringVar(&opts.commitAuthor.name, "commit-author-name", defaultCommitAuthorName, "The Git commit author name to use. Conflicts with '--use-api-commit-author'.")
	fl.StringVar(&opts.commitAuthor.email, "commit-author-email", defaultCommitAuthorEmail, "The Git commit author email to use. Conflicts with '--use-api-commit-author'.")
	agentBootstrapCmd.MarkFlagsMutuallyExclusive("use-api-commit-author", "commit-author-name")
	agentBootstrapCmd.MarkFlagsMutuallyExclusive("use-api-commit-author", "commit-author-email")

	return agentBootstrapCmd
}

type bootstrapCmd struct {
	api                 API
	stdout              io.Writer
	stderr              io.Writer
	agentName           string
	manifestBranch      string
	kubectl             KubectlWrapper
	flux                FluxWrapper
	noReconcile         bool
	agentEnvironmentCfg *environmentConfiguration
	fluxEnvironmentCfg  *environmentConfiguration
}

type file struct {
	path    string
	content []byte
}

type environmentConfiguration struct {
	name                string
	kubernetesNamespace string
	fluxResourcePath    string
}

func (c *bootstrapCmd) run() error {
	// 1. Register the agent
	fmt.Fprintf(c.stderr, "Registering Agent ... ") //nolint:forbidigo // c.stderr is an io.Writer mocked directly in tests, not IOStreams
	agent, err := c.registerAgent()
	if err != nil {
		fmt.Fprintf(c.stderr, "[FAILED]\n") //nolint:forbidigo // c.stderr is an io.Writer mocked directly in tests, not IOStreams
		return err
	}
	fmt.Fprintf(c.stderr, "[OK]\n") //nolint:forbidigo // c.stderr is an io.Writer mocked directly in tests, not IOStreams

	// 2. Configure the Agent
	fmt.Fprintf(c.stderr, "Configuring Agent ... ") //nolint:forbidigo // c.stderr is an io.Writer mocked directly in tests, not IOStreams
	err = c.configureAgent(agent)
	if err != nil {
		fmt.Fprintf(c.stderr, "[FAILED]\n") //nolint:forbidigo // c.stderr is an io.Writer mocked directly in tests, not IOStreams
		return err
	}
	fmt.Fprintf(c.stderr, "[OK]\n") //nolint:forbidigo // c.stderr is an io.Writer mocked directly in tests, not IOStreams

	// 3. Configure Environment for Agent
	fmt.Fprintf(c.stderr, "Configuring Environment with Dashboard for Agent ... ") //nolint:forbidigo // c.stderr is an io.Writer mocked directly in tests, not IOStreams
	if c.agentEnvironmentCfg != nil {
		err = c.configureEnvironment(agent, c.agentEnvironmentCfg)
		if err != nil {
			fmt.Fprintf(c.stderr, "[FAILED]\n") //nolint:forbidigo // c.stderr is an io.Writer mocked directly in tests, not IOStreams
			return err
		}
		fmt.Fprintf(c.stderr, "[OK]\n") //nolint:forbidigo // c.stderr is an io.Writer mocked directly in tests, not IOStreams
	} else {
		fmt.Fprintf(c.stderr, "[SKIPPED]\n") //nolint:forbidigo // c.stderr is an io.Writer mocked directly in tests, not IOStreams
	}

	// 4. Configure Environment for FluxCD
	fmt.Fprintf(c.stderr, "Configuring Environment with Dashboard for FluxCD ... ") //nolint:forbidigo // c.stderr is an io.Writer mocked directly in tests, not IOStreams
	if c.fluxEnvironmentCfg != nil {
		err = c.configureEnvironment(agent, c.fluxEnvironmentCfg)
		if err != nil {
			fmt.Fprintf(c.stderr, "[FAILED]\n") //nolint:forbidigo // c.stderr is an io.Writer mocked directly in tests, not IOStreams
			return err
		}
		fmt.Fprintf(c.stderr, "[OK]\n") //nolint:forbidigo // c.stderr is an io.Writer mocked directly in tests, not IOStreams
	} else {
		fmt.Fprintf(c.stderr, "[SKIPPED]\n") //nolint:forbidigo // c.stderr is an io.Writer mocked directly in tests, not IOStreams
	}

	// 5. Create a token for the registered agent
	fmt.Fprintf(c.stderr, "Creating Agent Token ... ") //nolint:forbidigo // c.stderr is an io.Writer mocked directly in tests, not IOStreams
	agentToken, err := c.createAgentToken(agent)
	if err != nil {
		fmt.Fprintf(c.stderr, "[FAILED]\n") //nolint:forbidigo // c.stderr is an io.Writer mocked directly in tests, not IOStreams
		return err
	}
	fmt.Fprintf(c.stderr, "[OK]\n") //nolint:forbidigo // c.stderr is an io.Writer mocked directly in tests, not IOStreams

	// 6. Push token in Kubernetes secret to cluster
	fmt.Fprintf(c.stderr, "Creating Kubernetes Secret with Agent Token ... ") //nolint:forbidigo // c.stderr is an io.Writer mocked directly in tests, not IOStreams
	err = c.createAgentTokenKubernetesSecret(agentToken)
	if err != nil {
		fmt.Fprintf(c.stderr, "[FAILED]\n") //nolint:forbidigo // c.stderr is an io.Writer mocked directly in tests, not IOStreams
		return err
	}
	fmt.Fprintf(c.stderr, "[OK]\n") //nolint:forbidigo // c.stderr is an io.Writer mocked directly in tests, not IOStreams

	// 7. Create Flux Helm Resources
	fmt.Fprintf(c.stderr, "Creating Flux Helm Resources ... ") //nolint:forbidigo // c.stderr is an io.Writer mocked directly in tests, not IOStreams
	helmResourceFiles, err := c.createFluxHelmResources()
	if err != nil {
		fmt.Fprintf(c.stderr, "[FAILED]\n") //nolint:forbidigo // c.stderr is an io.Writer mocked directly in tests, not IOStreams
		return err
	}
	fmt.Fprintf(c.stderr, "[OK]\n") //nolint:forbidigo // c.stderr is an io.Writer mocked directly in tests, not IOStreams

	// 8. Sync Flux Helm Resources
	fmt.Fprintf(c.stderr, "Syncing Flux Helm Resources ... ") //nolint:forbidigo // c.stderr is an io.Writer mocked directly in tests, not IOStreams
	err = c.syncFluxHelmResourceFiles(helmResourceFiles)
	if err != nil {
		fmt.Fprintf(c.stderr, "[FAILED]\n") //nolint:forbidigo // c.stderr is an io.Writer mocked directly in tests, not IOStreams
		return err
	}
	fmt.Fprintf(c.stderr, "[OK]\n") //nolint:forbidigo // c.stderr is an io.Writer mocked directly in tests, not IOStreams

	// 9. Flux Reconcile
	fmt.Fprintf(c.stderr, "Reconciling Flux Helm Resources ... ") //nolint:forbidigo // c.stderr is an io.Writer mocked directly in tests, not IOStreams
	if !c.noReconcile {
		fmt.Fprintln(c.stderr, "Output from flux command:") //nolint:forbidigo // c.stderr is an io.Writer mocked directly in tests, not IOStreams
		err = c.fluxReconcile()
		if err != nil {
			return fmt.Errorf("%w:\n%w", reconcileErr, err)
		}
	} else {
		fmt.Fprintf(c.stderr, "[SKIPPED]\n") //nolint:forbidigo // c.stderr is an io.Writer mocked directly in tests, not IOStreams
	}

	fmt.Fprintln(c.stderr, "Successfully bootstrapped the GitLab Agent") //nolint:forbidigo // c.stderr is an io.Writer mocked directly in tests, not IOStreams
	return nil
}

func (c *bootstrapCmd) registerAgent() (*gitlab.Agent, error) {
	agent, err := c.api.GetAgentByName(c.agentName)
	if err != nil {
		if !errors.Is(err, agentNotFoundErr) {
			return nil, err
		}

		// register agent
		agent, err = c.api.RegisterAgent(c.agentName)
		if err != nil {
			return nil, err
		}
	}
	return agent, nil
}

func (c *bootstrapCmd) configureAgent(agent *gitlab.Agent) error {
	return c.api.ConfigureAgent(agent, c.manifestBranch)
}

func (c *bootstrapCmd) configureEnvironment(agent *gitlab.Agent, cfg *environmentConfiguration) error {
	return c.api.ConfigureEnvironment(agent.ID, cfg.name, cfg.kubernetesNamespace, cfg.fluxResourcePath)
}

func (c *bootstrapCmd) createAgentToken(agent *gitlab.Agent) (*gitlab.AgentToken, error) {
	return c.api.CreateAgentToken(agent.ID)
}

func (c *bootstrapCmd) createAgentTokenKubernetesSecret(token *gitlab.AgentToken) error {
	return c.kubectl.createAgentTokenSecret(token.ID, token.Token)
}

func (c *bootstrapCmd) createFluxHelmResources() ([]file, error) {
	helmRepository, err := c.flux.createHelmRepositoryManifest()
	if err != nil {
		return nil, err
	}

	kasAddress, err := c.api.GetKASAddress()
	if err != nil {
		return nil, err
	}

	helmRelease, err := c.flux.createHelmReleaseManifest(kasAddress)
	if err != nil {
		return nil, err
	}

	return []file{helmRepository, helmRelease}, nil
}

func (c *bootstrapCmd) syncFluxHelmResourceFiles(files []file) error {
	for _, f := range files {
		err := c.api.SyncFile(f, c.manifestBranch)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *bootstrapCmd) fluxReconcile() error {
	return c.flux.reconcile()
}
