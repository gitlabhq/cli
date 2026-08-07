package update_kubeconfig

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/cluster/agent/agentutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

const (
	k8sAuthInfoExecApiVersion = "client.authentication.k8s.io/v1"

	flagAgent                  = "agent"
	flagUseContext             = "use-context"
	flagTokenExpiryDuration    = "token-expiry-duration"
	flagCheckRevoked           = "check-revoked"
	tokenExpiryDurationDefault = 24 * time.Hour
	minTokenExpiryDuration     = 24 * time.Hour
)

var sanitizeReplacer = strings.NewReplacer("/", "_", ".", "_")

type options struct {
	gitlabClient func() (*gitlab.Client, error)
	baseRepo     func() (glrepo.Interface, error)
	io           *iostreams.IOStreams
	configAccess clientcmd.ConfigAccess

	useContext          bool
	agentID             int64
	tokenExpiryDuration time.Duration
	cacheMode           agentutils.CacheMode
	checkRevoked        bool
}

func NewCmdAgentUpdateKubeconfig(f cmdutils.Factory) *cobra.Command {
	pathOptions := clientcmd.NewDefaultPathOptions()
	if len(pathOptions.ExplicitFileFlag) == 0 {
		pathOptions.ExplicitFileFlag = clientcmd.RecommendedConfigPathFlag
	}

	opts := options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
		baseRepo:     f.BaseRepo,
		configAccess: pathOptions,
	}

	agentUpdateKubeconfigCmd := &cobra.Command{
		Use:   "update-kubeconfig [flags]",
		Short: `Update your kubeconfig for use with a GitLab Agent for Kubernetes.`,
		Long: heredoc.Docf(`
			Configures %[1]skubectl%[1]s to authenticate through the GitLab Agent for Kubernetes
			using an exec credential plugin.

			- Use %[1]s--agent%[1]s to specify the agent by its numeric ID.
			- Use %[1]s--use-context%[1]s to set the agent as the default %[1]skubectl%[1]s context.
			- Use %[1]s--token-expiry-duration%[1]s to control how long the generated token is valid.
			- Use %[1]s--cache-mode%[1]s to control how generated tokens are cached.
		`, "`"),
		Example: heredoc.Doc(`
			# Update kubeconfig for agent 123
			glab cluster agent update-kubeconfig --agent 123

			# Update kubeconfig and set as the default context
			glab cluster agent update-kubeconfig --agent 123 --use-context`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(); err != nil {
				return err
			}
			return opts.run()
		},
	}
	fl := agentUpdateKubeconfigCmd.Flags()
	fl.Int64VarP(&opts.agentID, flagAgent, "a", opts.agentID, "The numeric agent ID to create the kubeconfig entry for.")
	fl.StringVar(&pathOptions.LoadingRules.ExplicitPath, pathOptions.ExplicitFileFlag, pathOptions.LoadingRules.ExplicitPath, "Use a particular kubeconfig file.")
	fl.BoolVarP(&opts.useContext, flagUseContext, "u", opts.useContext, "Use as default context.")
	fl.DurationVar(&opts.tokenExpiryDuration, flagTokenExpiryDuration, tokenExpiryDurationDefault, "Duration for generated token's validity. Minimum is 1 day. Expires at end of day, and ignores time.")
	fl.BoolVar(&opts.checkRevoked, flagCheckRevoked, false, "Check if a cached token is revoked. Requires an API call to GitLab, which adds latency every time a cached token is accessed.")
	agentutils.AddTokenCacheModeFlag(fl, &opts.cacheMode)
	cobra.CheckErr(agentUpdateKubeconfigCmd.MarkFlagRequired(flagAgent))

	return agentUpdateKubeconfigCmd
}

func (o *options) validate() error {
	if o.tokenExpiryDuration < minTokenExpiryDuration {
		return fmt.Errorf("token expiry duration must be at least 24 hours")
	}
	return nil
}

func (o *options) run() error {
	client, err := o.gitlabClient()
	if err != nil {
		return err
	}

	repo, err := o.baseRepo()
	if err != nil {
		return err
	}

	// Retrieve metadata of the instance to determine KAS URL
	metadata, _, err := client.Metadata.GetMetadata()
	if err != nil {
		return err
	}
	if !metadata.KAS.Enabled {
		return fmt.Errorf("the GitLab agent server for Kubernetes is disabled on %s; ask your administrator to enable and configure it", repo.RepoHost())
	}
	kasK8SProxyURL, err := agentutils.GetKasK8SProxyURL(metadata)
	if err != nil {
		return err
	}

	// Retrieve agent information, most importantly its name to use it as context name.
	repoFullName := repo.FullName()
	agent, _, err := client.ClusterAgents.GetAgent(repoFullName, o.agentID)
	if err != nil {
		return err
	}

	// Retrieve user information
	user, _, err := client.Users.CurrentUser()
	if err != nil {
		return err
	}

	// Retrieve glab executable path for exec
	glabExecutable, err := os.Executable()
	if err != nil {
		return err
	}

	startingConfig, err := o.configAccess.GetStartingConfig()
	if err != nil {
		return err
	}

	params := updateKubeconfigParams{
		startingConfig:      *startingConfig,
		glabExecutable:      glabExecutable,
		glHost:              repo.RepoHost(),
		glRepoFullName:      repoFullName,
		glUser:              user.Username,
		kasK8sProxyURL:      kasK8SProxyURL,
		agent:               agent,
		tokenExpiryDuration: o.tokenExpiryDuration,
		cacheMode:           o.cacheMode,
		checkRevoked:        o.checkRevoked,
	}
	config, contextName := updateKubeconfig(params)

	if o.useContext {
		config.CurrentContext = contextName
	}

	if err := clientcmd.ModifyConfig(o.configAccess, config, true); err != nil {
		return err
	}

	o.io.LogInfof("Updated context %s.\n", contextName)

	if o.useContext {
		o.io.LogInfof("Using context %s.\n", contextName)
	}
	return nil
}

type updateKubeconfigParams struct {
	startingConfig      clientcmdapi.Config
	glabExecutable      string
	glHost              string
	glRepoFullName      string
	glUser              string
	kasK8sProxyURL      string
	agent               *gitlab.Agent
	tokenExpiryDuration time.Duration
	cacheMode           agentutils.CacheMode
	checkRevoked        bool
}

func updateKubeconfig(params updateKubeconfigParams) (clientcmdapi.Config, string) {
	config := params.startingConfig

	// Updating `clusters` entry: `kubectl config set-cluster ...`
	clusterName := sanitizeForKubeconfig(params.glHost)
	startingCluster, exists := config.Clusters[clusterName]
	if !exists {
		startingCluster = clientcmdapi.NewCluster()
	}
	config.Clusters[clusterName] = modifyCluster(*startingCluster, params.kasK8sProxyURL)

	// Updating `users` entry: `kubectl config set-credentials ...`
	authInfoName := fmt.Sprintf("%s-%d", clusterName, params.agent.ID)
	startingAuthInfo, exists := config.AuthInfos[authInfoName]
	if !exists {
		startingAuthInfo = clientcmdapi.NewAuthInfo()
	}
	config.AuthInfos[authInfoName] = modifyAuthInfo(
		*startingAuthInfo,
		params.glabExecutable,
		params.glHost,
		params.glRepoFullName,
		params.agent.ID,
		params.tokenExpiryDuration,
		params.cacheMode,
		params.checkRevoked,
	)

	// Updating `contexts` entry: `kubectl config set-context ...`
	contextName := fmt.Sprintf("%s-%s-%s", clusterName, sanitizeForKubeconfig(params.agent.ConfigProject.PathWithNamespace), params.agent.Name)
	startingContext, exists := config.Contexts[contextName]
	if !exists {
		startingContext = clientcmdapi.NewContext()
	}
	config.Contexts[contextName] = modifyContext(*startingContext, clusterName, authInfoName)

	return config, contextName
}

func modifyCluster(cluster clientcmdapi.Cluster, server string) *clientcmdapi.Cluster {
	cluster.Server = server
	return &cluster
}

func modifyAuthInfo(authInfo clientcmdapi.AuthInfo, glabExecutable string, glabHost string, glRepoFullName string, agentID int64, tokenExpiryDuration time.Duration, cacheMode agentutils.CacheMode, checkRevoked bool) *clientcmdapi.AuthInfo {
	// Clear existing auth info
	authInfo.Token = ""
	authInfo.TokenFile = ""

	// Two reasons to set --repo and GITLAB_HOST:
	// - Propagate the host and repo if it's not the default one.
	// - Isolate from the variable(s) that might be set to a different value when kubectl calls this command to get a token.
	//   This requires ALWAYS setting this variable, even it has the default value as the caller might have
	//   a custom value set, but it must be ignored.

	args := []string{
		"cluster", "agent", "get-token",
		"--agent", strconv.FormatInt(agentID, 10),
		"--repo", glRepoFullName,
		"--token-expiry-duration", tokenExpiryDuration.String(),
		"--cache-mode", cacheMode,
	}
	if checkRevoked {
		args = append(args, "--check-revoked")
	}
	authInfo.Exec = &clientcmdapi.ExecConfig{
		Command: glabExecutable,
		Args:    args,
		Env: []clientcmdapi.ExecEnvVar{
			{
				Name:  "GITLAB_HOST",
				Value: glabHost,
			},
		},
		APIVersion: k8sAuthInfoExecApiVersion,
		InstallHint: heredoc.Doc(`
			To authenticate to the current cluster, glab is required.

			Follow the installation instructions at https://gitlab.com/gitlab-org/cli#installation.
		`),
		InteractiveMode: clientcmdapi.NeverExecInteractiveMode,
	}

	return &authInfo
}

func modifyContext(ctx clientcmdapi.Context, clusterName, authInfoName string) *clientcmdapi.Context {
	ctx.Cluster = clusterName
	ctx.AuthInfo = authInfoName
	return &ctx
}

func sanitizeForKubeconfig(name string) string {
	return sanitizeReplacer.Replace(name)
}
