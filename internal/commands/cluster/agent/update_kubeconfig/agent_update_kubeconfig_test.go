//go:build !integration

package update_kubeconfig

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/commands/cluster/agent/agentutils"
)

func TestAgent_UpdateKubeConfig_GlabExec(t *testing.T) {
	// GIVEN
	startingConfig := clientcmdapi.NewConfig()

	// WHEN
	params := updateKubeconfigParams{
		startingConfig:      *startingConfig,
		glabExecutable:      "glab",
		glHost:              "gitlab.example.com",
		glRepoFullName:      "gitlab-user/repo",
		glUser:              "gitlab-user",
		kasK8sProxyURL:      "https://kas.gitlab.example.com/k8s-proxy",
		agent:               &gitlab.Agent{ID: 42, Name: "test-agent", ConfigProject: gitlab.ConfigProject{PathWithNamespace: "foo/bar"}},
		tokenExpiryDuration: 42 * time.Hour,
		cacheMode:           agentutils.ForcedFilesystemCacheMode,
		checkRevoked:        false,
	}
	modifiedConfig, contextName := updateKubeconfig(params)

	// THEN
	// verify the cluster entry
	assert.Contains(t, modifiedConfig.Clusters, "gitlab_example_com")
	assert.Equal(t, "https://kas.gitlab.example.com/k8s-proxy", modifiedConfig.Clusters["gitlab_example_com"].Server)

	// verify auth info entry
	assert.Contains(t, modifiedConfig.AuthInfos, "gitlab_example_com-42")
	actualExec := modifiedConfig.AuthInfos["gitlab_example_com-42"].Exec
	assert.Equal(t, k8sAuthInfoExecApiVersion, actualExec.APIVersion)
	assert.Equal(t, "glab", actualExec.Command)
	assert.Equal(t, []string{"cluster", "agent", "get-token", "--agent", "42", "--repo", "gitlab-user/repo", "--token-expiry-duration", "42h0m0s", "--cache-mode", agentutils.ForcedFilesystemCacheMode}, actualExec.Args)
	assert.Equal(t, []clientcmdapi.ExecEnvVar{{Name: "GITLAB_HOST", Value: "gitlab.example.com"}}, actualExec.Env)
	assert.Equal(t, clientcmdapi.NeverExecInteractiveMode, actualExec.InteractiveMode)
	assert.Empty(t, modifiedConfig.AuthInfos["gitlab_example_com-42"].Token)
	assert.Empty(t, modifiedConfig.AuthInfos["gitlab_example_com-42"].TokenFile)

	// verify context entry
	assert.Contains(t, modifiedConfig.Contexts, "gitlab_example_com-foo_bar-test-agent")
	actualContext := modifiedConfig.Contexts["gitlab_example_com-foo_bar-test-agent"]
	assert.Equal(t, "gitlab_example_com", actualContext.Cluster)
	assert.Equal(t, "gitlab_example_com-42", actualContext.AuthInfo)

	// verify returned context name
	assert.Equal(t, "gitlab_example_com-foo_bar-test-agent", contextName)
}
