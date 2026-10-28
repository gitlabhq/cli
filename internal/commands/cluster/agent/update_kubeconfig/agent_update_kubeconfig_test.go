//go:build !integration

package update_kubeconfig

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/commands/cluster/agent/agentutils"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
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

const testContextName = "gitlab_example_com-foo_bar-test-agent"

func setupUpdateKubeconfigCmd(t *testing.T) cmdtest.CmdExecFunc {
	t.Helper()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockMetadata.EXPECT().GetMetadata(gomock.Any()).Return(&gitlab.Metadata{
		KAS: gitlab.MetadataKAS{Enabled: true, ExternalK8SProxyURL: "https://kas.gitlab.example.com/k8s-proxy"},
	}, nil, nil)
	tc.MockClusterAgents.EXPECT().GetAgent("OWNER/REPO", int64(42), gomock.Any()).Return(&gitlab.Agent{
		ID:            42,
		Name:          "test-agent",
		ConfigProject: gitlab.ConfigProject{PathWithNamespace: "foo/bar"},
	}, nil, nil)
	tc.MockUsers.EXPECT().CurrentUser(gomock.Any()).Return(&gitlab.User{Username: "gitlab-user"}, nil, nil)

	return cmdtest.SetupCmdForTest(
		t,
		NewCmdAgentUpdateKubeconfig,
		false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", "gitlab.example.com"),
	)
}

func requireContextInKubeconfig(t *testing.T, path string) {
	t.Helper()

	config, err := clientcmd.LoadFromFile(path)
	require.NoError(t, err)
	assert.Contains(t, config.Contexts, testContextName)
}

func TestAgentUpdateKubeconfig_Snap_WritesToRealHome(t *testing.T) {
	realHome := t.TempDir()
	t.Setenv("SNAP_REAL_HOME", realHome)
	t.Setenv("KUBECONFIG", "")
	exec := setupUpdateKubeconfigCmd(t)

	out, err := exec("--agent 42")

	require.NoError(t, err)
	assert.Contains(t, out.OutBuf.String(), "Updated context "+testContextName)
	requireContextInKubeconfig(t, filepath.Join(realHome, ".kube", "config"))
}

func TestAgentUpdateKubeconfig_Snap_KubeconfigEnvVarWins(t *testing.T) {
	realHome := t.TempDir()
	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	t.Setenv("SNAP_REAL_HOME", realHome)
	t.Setenv("KUBECONFIG", kubeconfig)
	exec := setupUpdateKubeconfigCmd(t)

	_, err := exec("--agent 42")

	require.NoError(t, err)
	requireContextInKubeconfig(t, kubeconfig)
	assert.NoFileExists(t, filepath.Join(realHome, ".kube", "config"))
}

func TestAgentUpdateKubeconfig_Snap_KubeconfigFlagWins(t *testing.T) {
	realHome := t.TempDir()
	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	t.Setenv("SNAP_REAL_HOME", realHome)
	t.Setenv("KUBECONFIG", "")
	exec := setupUpdateKubeconfigCmd(t)

	_, err := exec("--agent 42 --kubeconfig " + kubeconfig)

	require.NoError(t, err)
	requireContextInKubeconfig(t, kubeconfig)
	assert.NoFileExists(t, filepath.Join(realHome, ".kube", "config"))
}

func TestAgentUpdateKubeconfig_PermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}

	unwritableDir := func(t *testing.T, dir string) string {
		t.Helper()
		require.NoError(t, os.MkdirAll(dir, 0o700))
		require.NoError(t, os.Chmod(dir, 0o500))
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		return filepath.Join(dir, "config")
	}
	unreadableFile := func(t *testing.T, dir string) string {
		t.Helper()
		require.NoError(t, os.MkdirAll(dir, 0o700))
		path := filepath.Join(dir, "config")
		require.NoError(t, os.WriteFile(path, nil, 0o000))
		return path
	}
	unreadableOtherFile := func(t *testing.T, dir string) string {
		t.Helper()
		require.NoError(t, os.MkdirAll(dir, 0o700))
		path := filepath.Join(dir, "other")
		require.NoError(t, os.WriteFile(path, nil, 0o000))
		return path
	}
	realKubeDir := func(realHome string) string { return filepath.Join(realHome, ".kube") }
	otherDir := func(string) string { return "" }

	tests := []struct {
		name string
		// dir picks where the kubeconfig lives from SNAP_REAL_HOME; "" means a temporary directory.
		dir         func(realHome string) string
		kubeconfig  func(t *testing.T, dir string) string
		snap        bool
		defaultPath bool
		viaFlag     bool
		wantHint    bool
	}{
		{name: "snap write to default path shows connect hint", dir: realKubeDir, kubeconfig: unwritableDir, snap: true, defaultPath: true, wantHint: true},
		{name: "snap read of ~/.kube/config shows connect hint", dir: realKubeDir, kubeconfig: unreadableFile, snap: true, wantHint: true},
		{name: "snap read of other file under ~/.kube returns plain error", dir: realKubeDir, kubeconfig: unreadableOtherFile, snap: true, wantHint: false},
		{name: "snap write outside ~/.kube returns plain error", dir: otherDir, kubeconfig: unwritableDir, snap: true, wantHint: false},
		{name: "snap read of --kubeconfig outside ~/.kube returns plain error", dir: otherDir, kubeconfig: unreadableFile, snap: true, viaFlag: true, wantHint: false},
		{name: "non-snap write returns plain error", dir: otherDir, kubeconfig: unwritableDir, snap: false, wantHint: false},
		{name: "non-snap read returns plain error", dir: otherDir, kubeconfig: unreadableFile, snap: false, wantHint: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			realHome := t.TempDir()
			if tt.snap {
				t.Setenv("SNAP_REAL_HOME", realHome)
			} else {
				t.Setenv("SNAP_REAL_HOME", "")
			}
			dir := tt.dir(realHome)
			if dir == "" {
				dir = t.TempDir()
			}
			kubeconfig := tt.kubeconfig(t, dir)
			args := "--agent 42"
			switch {
			case tt.defaultPath:
				t.Setenv("KUBECONFIG", "")
			case tt.viaFlag:
				t.Setenv("KUBECONFIG", "")
				args += " --kubeconfig " + kubeconfig
			default:
				t.Setenv("KUBECONFIG", kubeconfig)
			}
			exec := setupUpdateKubeconfigCmd(t)

			_, err := exec(args)

			require.ErrorContains(t, err, "permission denied")
			if tt.wantHint {
				assert.ErrorContains(t, err, "sudo snap connect glab:dot-kube-config")
			} else {
				assert.NotContains(t, err.Error(), "snap connect")
			}
		})
	}
}

func TestNewPathOptions_DefaultsToHomeOutsideSnap(t *testing.T) {
	t.Setenv("SNAP_REAL_HOME", "")

	assert.Equal(t, clientcmd.RecommendedHomeFile, newPathOptions().GlobalFile)
}
