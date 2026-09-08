//go:build !integration

package bootstrap

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/google/shlex"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestAgentBootstrap_FailsToGetDefaultBranchForDefaultManifestBranch(t *testing.T) {
	// GIVEN
	exec, api, _, stderr, _, _ := setupCmdExec(t)

	actualErr := errors.New("dummy error")

	api.EXPECT().GetDefaultBranch().Return("", actualErr)
	stderr.EXPECT().Write(ContainsBytes([]byte(actualErr.Error())))

	// WHEN
	err := exec("test-agent-name")

	// THEN
	assert.ErrorIs(t, err, actualErr)
}

func TestAgentBootstrap_HappyPath_AgentNotRegisteredYet(t *testing.T) {
	// GIVEN
	exec, api, _, stderr, kubectlWrapper, fluxWrapper := setupCmdExec(t)

	defaultKASAddress := "wss://kas.gitlab.example.com"
	defaultBranch := "main"
	agentName := "test-agent-name"
	agentTokenValue := "glagent-test-token"
	agent := &gitlab.Agent{ID: 1, Name: agentName}
	agentToken := &gitlab.AgentToken{ID: 42, Token: agentTokenValue}
	helmRepositoryFile := file{path: "gitlab-helm-repository.yaml", content: []byte("any")}
	helmReleaseFile := file{path: "gitlab-agent-helm-release.yaml", content: []byte("any")}

	// Agent environment configuration
	defaultEnvironmentName := "flux-system/gitlab-agent"
	defaultKubernetesNamespace := "gitlab-agent"
	defaultFluxResourcePath := "helm.toolkit.fluxcd.io/v2beta1/namespaces/flux-system/helmreleases/gitlab-agent"

	// Flux environment configuration
	fluxEnvironmentName := "flux-system/flux-system"
	fluxKubernetesNamespace := "flux-system"
	fluxFluxResourcePath := "kustomize.toolkit.fluxcd.io/v1/namespaces/flux-system/kustomizations/flux-system"

	gomock.InOrder(
		api.EXPECT().GetDefaultBranch().Return(defaultBranch, nil),
		stderr.EXPECT().Write([]byte("Registering Agent ... ")),
		api.EXPECT().GetAgentByName(agentName).Return(nil, agentNotFoundErr),
		api.EXPECT().RegisterAgent(agentName).Return(agent, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Agent ... ")),
		api.EXPECT().ConfigureAgent(agent, defaultBranch).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Environment with Dashboard for Agent ... ")),
		api.EXPECT().ConfigureEnvironment(agent.ID, defaultEnvironmentName, defaultKubernetesNamespace, defaultFluxResourcePath).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Environment with Dashboard for FluxCD ... ")),
		api.EXPECT().ConfigureEnvironment(agent.ID, fluxEnvironmentName, fluxKubernetesNamespace, fluxFluxResourcePath).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Agent Token ... ")),
		api.EXPECT().CreateAgentToken(agent.ID).Return(agentToken, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Kubernetes Secret with Agent Token ... ")),
		kubectlWrapper.EXPECT().createAgentTokenSecret(int64(42), agentTokenValue).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Flux Helm Resources ... ")),
		fluxWrapper.EXPECT().createHelmRepositoryManifest().Return(helmRepositoryFile, nil),
		api.EXPECT().GetKASAddress().Return(defaultKASAddress, nil),
		fluxWrapper.EXPECT().createHelmReleaseManifest(defaultKASAddress).Return(helmReleaseFile, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Syncing Flux Helm Resources ... ")),
		api.EXPECT().SyncFile(helmRepositoryFile, defaultBranch).Return(nil),
		api.EXPECT().SyncFile(helmReleaseFile, defaultBranch).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Reconciling Flux Helm Resources ... ")),
		stderr.EXPECT().Write([]byte("Output from flux command:\n")),
		fluxWrapper.EXPECT().reconcile().Return(nil),
		stderr.EXPECT().Write([]byte("Successfully bootstrapped the GitLab Agent\n")),
	)

	// WHEN
	err := exec(agentName)

	// THEN
	assert.NoError(t, err)
}

func TestAgentBootstrap_HappyPath_AgentAlreadyRegistered(t *testing.T) {
	// GIVEN
	exec, api, _, stderr, kubectlWrapper, fluxWrapper := setupCmdExec(t)

	defaultKASAddress := "wss://kas.gitlab.example.com"
	defaultBranch := "main"
	agentName := "test-agent-name"
	agentTokenValue := "glagent-test-token"
	agent := &gitlab.Agent{ID: 1, Name: agentName}
	agentToken := &gitlab.AgentToken{ID: 42, Token: agentTokenValue}
	helmRepositoryFile := file{path: "gitlab-helm-repository.yaml", content: []byte("any")}
	helmReleaseFile := file{path: "gitlab-agent-helm-release.yaml", content: []byte("any")}

	// Agent environment configuration
	defaultEnvironmentName := "flux-system/gitlab-agent"
	defaultKubernetesNamespace := "gitlab-agent"
	defaultFluxResourcePath := "helm.toolkit.fluxcd.io/v2beta1/namespaces/flux-system/helmreleases/gitlab-agent"

	// Flux environment configuration
	fluxEnvironmentName := "flux-system/flux-system"
	fluxKubernetesNamespace := "flux-system"
	fluxFluxResourcePath := "kustomize.toolkit.fluxcd.io/v1/namespaces/flux-system/kustomizations/flux-system"

	gomock.InOrder(
		api.EXPECT().GetDefaultBranch().Return(defaultBranch, nil),
		stderr.EXPECT().Write([]byte("Registering Agent ... ")),
		api.EXPECT().GetAgentByName(agentName).Return(agent, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Agent ... ")),
		api.EXPECT().ConfigureAgent(agent, defaultBranch).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Environment with Dashboard for Agent ... ")),
		api.EXPECT().ConfigureEnvironment(agent.ID, defaultEnvironmentName, defaultKubernetesNamespace, defaultFluxResourcePath).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Environment with Dashboard for FluxCD ... ")),
		api.EXPECT().ConfigureEnvironment(agent.ID, fluxEnvironmentName, fluxKubernetesNamespace, fluxFluxResourcePath).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Agent Token ... ")),
		api.EXPECT().CreateAgentToken(agent.ID).Return(agentToken, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Kubernetes Secret with Agent Token ... ")),
		kubectlWrapper.EXPECT().createAgentTokenSecret(int64(42), agentTokenValue).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Flux Helm Resources ... ")),
		fluxWrapper.EXPECT().createHelmRepositoryManifest().Return(helmRepositoryFile, nil),
		api.EXPECT().GetKASAddress().Return(defaultKASAddress, nil),
		fluxWrapper.EXPECT().createHelmReleaseManifest(defaultKASAddress).Return(helmReleaseFile, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Syncing Flux Helm Resources ... ")),
		api.EXPECT().SyncFile(helmRepositoryFile, defaultBranch).Return(nil),
		api.EXPECT().SyncFile(helmReleaseFile, defaultBranch).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Reconciling Flux Helm Resources ... ")),
		stderr.EXPECT().Write([]byte("Output from flux command:\n")),
		fluxWrapper.EXPECT().reconcile().Return(nil),
		stderr.EXPECT().Write([]byte("Successfully bootstrapped the GitLab Agent\n")),
	)

	// WHEN
	err := exec(agentName)

	// THEN
	assert.NoError(t, err)
}

func TestAgentBootstrap_HappyPath_NoEnvironmentCreation(t *testing.T) {
	// GIVEN
	exec, api, _, stderr, kubectlWrapper, fluxWrapper := setupCmdExec(t)

	defaultKASAddress := "wss://kas.gitlab.example.com"
	defaultBranch := "main"
	agentName := "test-agent-name"
	agentTokenValue := "glagent-test-token"
	agent := &gitlab.Agent{ID: 1, Name: agentName}
	agentToken := &gitlab.AgentToken{ID: 42, Token: agentTokenValue}
	helmRepositoryFile := file{path: "gitlab-helm-repository.yaml", content: []byte("any")}
	helmReleaseFile := file{path: "gitlab-agent-helm-release.yaml", content: []byte("any")}

	// Flux environment configuration
	fluxEnvironmentName := "flux-system/flux-system"
	fluxKubernetesNamespace := "flux-system"
	fluxFluxResourcePath := "kustomize.toolkit.fluxcd.io/v1/namespaces/flux-system/kustomizations/flux-system"

	gomock.InOrder(
		api.EXPECT().GetDefaultBranch().Return(defaultBranch, nil),
		stderr.EXPECT().Write([]byte("Registering Agent ... ")),
		api.EXPECT().GetAgentByName(agentName).Return(nil, agentNotFoundErr),
		api.EXPECT().RegisterAgent(agentName).Return(agent, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Agent ... ")),
		api.EXPECT().ConfigureAgent(agent, defaultBranch).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Environment with Dashboard for Agent ... ")),
		stderr.EXPECT().Write([]byte("[SKIPPED]\n")),
		stderr.EXPECT().Write([]byte("Configuring Environment with Dashboard for FluxCD ... ")),
		api.EXPECT().ConfigureEnvironment(agent.ID, fluxEnvironmentName, fluxKubernetesNamespace, fluxFluxResourcePath).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Agent Token ... ")),
		api.EXPECT().CreateAgentToken(agent.ID).Return(agentToken, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Kubernetes Secret with Agent Token ... ")),
		kubectlWrapper.EXPECT().createAgentTokenSecret(int64(42), agentTokenValue).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Flux Helm Resources ... ")),
		fluxWrapper.EXPECT().createHelmRepositoryManifest().Return(helmRepositoryFile, nil),
		api.EXPECT().GetKASAddress().Return(defaultKASAddress, nil),
		fluxWrapper.EXPECT().createHelmReleaseManifest(defaultKASAddress).Return(helmReleaseFile, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Syncing Flux Helm Resources ... ")),
		api.EXPECT().SyncFile(helmRepositoryFile, defaultBranch).Return(nil),
		api.EXPECT().SyncFile(helmReleaseFile, defaultBranch).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Reconciling Flux Helm Resources ... ")),
		stderr.EXPECT().Write([]byte("Output from flux command:\n")),
		fluxWrapper.EXPECT().reconcile().Return(nil),
		stderr.EXPECT().Write([]byte("Successfully bootstrapped the GitLab Agent\n")),
	)

	// WHEN
	err := exec(fmt.Sprintf("%s --create-environment=false", agentName))

	// THEN
	assert.NoError(t, err)
}

func TestAgentBootstrap_HappyPath_CustomEnvironmentValues(t *testing.T) {
	// GIVEN
	exec, api, _, stderr, kubectlWrapper, fluxWrapper := setupCmdExec(t)

	defaultKASAddress := "wss://kas.gitlab.example.com"
	defaultBranch := "main"
	agentName := "test-agent-name"
	agentTokenValue := "glagent-test-token"
	agent := &gitlab.Agent{ID: 1, Name: agentName}
	agentToken := &gitlab.AgentToken{ID: 42, Token: agentTokenValue}
	helmRepositoryFile := file{path: "gitlab-helm-repository.yaml", content: []byte("any")}
	helmReleaseFile := file{path: "gitlab-agent-helm-release.yaml", content: []byte("any")}
	customEnvironmentName := "custom-name"
	customKubernetesNamespace := "custom-namespace"
	customFluxResourcePath := "custom-flux-resource-path"

	// Flux environment configuration
	fluxEnvironmentName := "flux-system/flux-system"
	fluxKubernetesNamespace := "flux-system"
	fluxFluxResourcePath := "kustomize.toolkit.fluxcd.io/v1/namespaces/flux-system/kustomizations/flux-system"

	gomock.InOrder(
		api.EXPECT().GetDefaultBranch().Return(defaultBranch, nil),
		stderr.EXPECT().Write([]byte("Registering Agent ... ")),
		api.EXPECT().GetAgentByName(agentName).Return(nil, agentNotFoundErr),
		api.EXPECT().RegisterAgent(agentName).Return(agent, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Agent ... ")),
		api.EXPECT().ConfigureAgent(agent, defaultBranch).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Environment with Dashboard for Agent ... ")),
		api.EXPECT().ConfigureEnvironment(agent.ID, customEnvironmentName, customKubernetesNamespace, customFluxResourcePath).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Environment with Dashboard for FluxCD ... ")),
		api.EXPECT().ConfigureEnvironment(agent.ID, fluxEnvironmentName, fluxKubernetesNamespace, fluxFluxResourcePath).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Agent Token ... ")),
		api.EXPECT().CreateAgentToken(agent.ID).Return(agentToken, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Kubernetes Secret with Agent Token ... ")),
		kubectlWrapper.EXPECT().createAgentTokenSecret(int64(42), agentTokenValue).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Flux Helm Resources ... ")),
		fluxWrapper.EXPECT().createHelmRepositoryManifest().Return(helmRepositoryFile, nil),
		api.EXPECT().GetKASAddress().Return(defaultKASAddress, nil),
		fluxWrapper.EXPECT().createHelmReleaseManifest(defaultKASAddress).Return(helmReleaseFile, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Syncing Flux Helm Resources ... ")),
		api.EXPECT().SyncFile(helmRepositoryFile, defaultBranch).Return(nil),
		api.EXPECT().SyncFile(helmReleaseFile, defaultBranch).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Reconciling Flux Helm Resources ... ")),
		stderr.EXPECT().Write([]byte("Output from flux command:\n")),
		fluxWrapper.EXPECT().reconcile().Return(nil),
		stderr.EXPECT().Write([]byte("Successfully bootstrapped the GitLab Agent\n")),
	)

	// WHEN
	err := exec(fmt.Sprintf(
		"%s --environment-name=%s --environment-namespace=%s --environment-flux-resource-path=%s",
		agentName,
		customEnvironmentName,
		customKubernetesNamespace,
		customFluxResourcePath,
	))

	// THEN
	assert.NoError(t, err)
}

func TestAgentBootstrap_HappyPath_NoReconcile(t *testing.T) {
	// GIVEN
	exec, api, _, stderr, kubectlWrapper, fluxWrapper := setupCmdExec(t)

	defaultKASAddress := "wss://kas.gitlab.example.com"
	defaultBranch := "main"
	agentName := "test-agent-name"
	agentTokenValue := "glagent-test-token"
	agent := &gitlab.Agent{ID: 1, Name: agentName}
	agentToken := &gitlab.AgentToken{ID: 42, Token: agentTokenValue}
	helmRepositoryFile := file{path: "gitlab-helm-repository.yaml", content: []byte("any")}
	helmReleaseFile := file{path: "gitlab-agent-helm-release.yaml", content: []byte("any")}
	defaultEnvironmentName := "flux-system/gitlab-agent"
	defaultKubernetesNamespace := "gitlab-agent"
	defaultFluxResourcePath := "helm.toolkit.fluxcd.io/v2beta1/namespaces/flux-system/helmreleases/gitlab-agent"

	// Flux environment configuration
	fluxEnvironmentName := "flux-system/flux-system"
	fluxKubernetesNamespace := "flux-system"
	fluxFluxResourcePath := "kustomize.toolkit.fluxcd.io/v1/namespaces/flux-system/kustomizations/flux-system"

	gomock.InOrder(
		api.EXPECT().GetDefaultBranch().Return(defaultBranch, nil),
		stderr.EXPECT().Write([]byte("Registering Agent ... ")),
		api.EXPECT().GetAgentByName(agentName).Return(nil, agentNotFoundErr),
		api.EXPECT().RegisterAgent(agentName).Return(agent, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Agent ... ")),
		api.EXPECT().ConfigureAgent(agent, defaultBranch).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Environment with Dashboard for Agent ... ")),
		api.EXPECT().ConfigureEnvironment(agent.ID, defaultEnvironmentName, defaultKubernetesNamespace, defaultFluxResourcePath).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Environment with Dashboard for FluxCD ... ")),
		api.EXPECT().ConfigureEnvironment(agent.ID, fluxEnvironmentName, fluxKubernetesNamespace, fluxFluxResourcePath).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Agent Token ... ")),
		api.EXPECT().CreateAgentToken(agent.ID).Return(agentToken, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Kubernetes Secret with Agent Token ... ")),
		kubectlWrapper.EXPECT().createAgentTokenSecret(int64(42), agentTokenValue).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Flux Helm Resources ... ")),
		fluxWrapper.EXPECT().createHelmRepositoryManifest().Return(helmRepositoryFile, nil),
		api.EXPECT().GetKASAddress().Return(defaultKASAddress, nil),
		fluxWrapper.EXPECT().createHelmReleaseManifest(defaultKASAddress).Return(helmReleaseFile, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Syncing Flux Helm Resources ... ")),
		api.EXPECT().SyncFile(helmRepositoryFile, defaultBranch).Return(nil),
		api.EXPECT().SyncFile(helmReleaseFile, defaultBranch).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Reconciling Flux Helm Resources ... ")),
		stderr.EXPECT().Write([]byte("[SKIPPED]\n")),
		stderr.EXPECT().Write([]byte("Successfully bootstrapped the GitLab Agent\n")),
	)

	// WHEN
	err := exec(fmt.Sprintf("%s --no-reconcile", agentName))

	// THEN
	assert.NoError(t, err)
}

func TestAgentBootstrap_HappyPath_CustomFluxHelmManifestFileNames(t *testing.T) {
	// GIVEN
	exec, api, _, stderr, kubectlWrapper, fluxWrapper := setupCmdExec(t)

	defaultKASAddress := "wss://kas.gitlab.example.com"
	defaultBranch := "main"
	agentName := "test-agent-name"
	agentTokenValue := "glagent-test-token"
	agent := &gitlab.Agent{ID: 1, Name: agentName}
	agentToken := &gitlab.AgentToken{ID: 42, Token: agentTokenValue}
	helmRepositoryFile := file{path: "test-1.yaml", content: []byte("any")}
	helmReleaseFile := file{path: "test-2.yaml", content: []byte("any")}
	defaultEnvironmentName := "flux-system/gitlab-agent"
	defaultKubernetesNamespace := "gitlab-agent"
	defaultFluxResourcePath := "helm.toolkit.fluxcd.io/v2beta1/namespaces/flux-system/helmreleases/gitlab-agent"

	// Flux environment configuration
	fluxEnvironmentName := "flux-system/flux-system"
	fluxKubernetesNamespace := "flux-system"
	fluxFluxResourcePath := "kustomize.toolkit.fluxcd.io/v1/namespaces/flux-system/kustomizations/flux-system"

	gomock.InOrder(
		api.EXPECT().GetDefaultBranch().Return(defaultBranch, nil),
		stderr.EXPECT().Write([]byte("Registering Agent ... ")),
		api.EXPECT().GetAgentByName(agentName).Return(nil, agentNotFoundErr),
		api.EXPECT().RegisterAgent(agentName).Return(agent, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Agent ... ")),
		api.EXPECT().ConfigureAgent(agent, defaultBranch).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Environment with Dashboard for Agent ... ")),
		api.EXPECT().ConfigureEnvironment(agent.ID, defaultEnvironmentName, defaultKubernetesNamespace, defaultFluxResourcePath).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Environment with Dashboard for FluxCD ... ")),
		api.EXPECT().ConfigureEnvironment(agent.ID, fluxEnvironmentName, fluxKubernetesNamespace, fluxFluxResourcePath).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Agent Token ... ")),
		api.EXPECT().CreateAgentToken(agent.ID).Return(agentToken, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Kubernetes Secret with Agent Token ... ")),
		kubectlWrapper.EXPECT().createAgentTokenSecret(int64(42), agentTokenValue).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Flux Helm Resources ... ")),
		fluxWrapper.EXPECT().createHelmRepositoryManifest().Return(helmRepositoryFile, nil),
		api.EXPECT().GetKASAddress().Return(defaultKASAddress, nil),
		fluxWrapper.EXPECT().createHelmReleaseManifest(defaultKASAddress).Return(helmReleaseFile, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Syncing Flux Helm Resources ... ")),
		api.EXPECT().SyncFile(helmRepositoryFile, defaultBranch).Return(nil),
		api.EXPECT().SyncFile(helmReleaseFile, defaultBranch).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Reconciling Flux Helm Resources ... ")),
		stderr.EXPECT().Write([]byte("Output from flux command:\n")),
		fluxWrapper.EXPECT().reconcile().Return(nil),
		stderr.EXPECT().Write([]byte("Successfully bootstrapped the GitLab Agent\n")),
	)

	// WHEN
	err := exec(fmt.Sprintf(
		"%s --helm-repository-filepath=%s --helm-release-filepath=%s",
		agentName,
		helmRepositoryFile.path,
		helmReleaseFile.path,
	))

	// THEN
	assert.NoError(t, err)
}

func TestAgentBootstrap_Error_GetAgentByName(t *testing.T) {
	// GIVEN
	exec, api, _, stderr, _, _ := setupCmdExec(t)

	defaultBranch := "main"
	agentName := "test-agent-name"

	actualErr := errors.New("dummy error")

	gomock.InOrder(
		api.EXPECT().GetDefaultBranch().Return(defaultBranch, nil),
		stderr.EXPECT().Write([]byte("Registering Agent ... ")),
		api.EXPECT().GetAgentByName(agentName).Return(nil, actualErr),
		stderr.EXPECT().Write([]byte("[FAILED]\n")),
		stderr.EXPECT().Write(ContainsBytes([]byte(actualErr.Error()))),
	)

	// WHEN
	err := exec(agentName)

	// THEN
	assert.ErrorIs(t, err, actualErr)
}

func TestAgentBootstrap_Error_RegisterAgent(t *testing.T) {
	// GIVEN
	exec, api, _, stderr, _, _ := setupCmdExec(t)

	defaultBranch := "main"
	agentName := "test-agent-name"

	actualErr := errors.New("dummy error")

	gomock.InOrder(
		api.EXPECT().GetDefaultBranch().Return(defaultBranch, nil),
		stderr.EXPECT().Write([]byte("Registering Agent ... ")),
		api.EXPECT().GetAgentByName(agentName).Return(nil, agentNotFoundErr),
		api.EXPECT().RegisterAgent(agentName).Return(nil, actualErr),
		stderr.EXPECT().Write([]byte("[FAILED]\n")),
		stderr.EXPECT().Write(ContainsBytes([]byte(actualErr.Error()))),
	)

	// WHEN
	err := exec(agentName)

	// THEN
	assert.ErrorIs(t, err, actualErr)
}

func TestAgentBootstrap_Error_ConfigureAgent(t *testing.T) {
	// GIVEN
	exec, api, _, stderr, _, _ := setupCmdExec(t)

	defaultBranch := "main"
	agentName := "test-agent-name"
	agent := &gitlab.Agent{ID: 1, Name: agentName}

	actualErr := errors.New("dummy error")

	gomock.InOrder(
		api.EXPECT().GetDefaultBranch().Return(defaultBranch, nil),
		stderr.EXPECT().Write([]byte("Registering Agent ... ")),
		api.EXPECT().GetAgentByName(agentName).Return(agent, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Agent ... ")),
		api.EXPECT().ConfigureAgent(agent, defaultBranch).Return(actualErr),
		stderr.EXPECT().Write([]byte("[FAILED]\n")),
		stderr.EXPECT().Write(ContainsBytes([]byte(actualErr.Error()))),
	)

	// WHEN
	err := exec(agentName)

	// THEN
	assert.ErrorIs(t, err, actualErr)
}

func TestAgentBootstrap_Error_ConfigureEnvironment(t *testing.T) {
	// GIVEN
	exec, api, _, stderr, _, _ := setupCmdExec(t)

	defaultBranch := "main"
	agentName := "test-agent-name"
	agent := &gitlab.Agent{ID: 1, Name: agentName}
	defaultEnvironmentName := "flux-system/gitlab-agent"
	defaultKubernetesNamespace := "gitlab-agent"
	defaultFluxResourcePath := "helm.toolkit.fluxcd.io/v2beta1/namespaces/flux-system/helmreleases/gitlab-agent"

	actualErr := errors.New("dummy error")

	gomock.InOrder(
		api.EXPECT().GetDefaultBranch().Return(defaultBranch, nil),
		stderr.EXPECT().Write([]byte("Registering Agent ... ")),
		api.EXPECT().GetAgentByName(agentName).Return(agent, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Agent ... ")),
		api.EXPECT().ConfigureAgent(agent, defaultBranch).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Environment with Dashboard for Agent ... ")),
		api.EXPECT().ConfigureEnvironment(agent.ID, defaultEnvironmentName, defaultKubernetesNamespace, defaultFluxResourcePath).Return(actualErr),
		stderr.EXPECT().Write([]byte("[FAILED]\n")),
		stderr.EXPECT().Write(ContainsBytes([]byte(actualErr.Error()))),
	)

	// WHEN
	err := exec(agentName)

	// THEN
	assert.ErrorIs(t, err, actualErr)
}

func TestAgentBootstrap_Error_CreateAgentToken(t *testing.T) {
	// GIVEN
	exec, api, _, stderr, _, _ := setupCmdExec(t)

	defaultBranch := "main"
	agentName := "test-agent-name"
	agent := &gitlab.Agent{ID: 1, Name: agentName}
	defaultEnvironmentName := "flux-system/gitlab-agent"
	defaultKubernetesNamespace := "gitlab-agent"
	defaultFluxResourcePath := "helm.toolkit.fluxcd.io/v2beta1/namespaces/flux-system/helmreleases/gitlab-agent"

	// Flux environment configuration
	fluxEnvironmentName := "flux-system/flux-system"
	fluxKubernetesNamespace := "flux-system"
	fluxFluxResourcePath := "kustomize.toolkit.fluxcd.io/v1/namespaces/flux-system/kustomizations/flux-system"

	actualErr := errors.New("dummy error")

	gomock.InOrder(
		api.EXPECT().GetDefaultBranch().Return(defaultBranch, nil),
		stderr.EXPECT().Write([]byte("Registering Agent ... ")),
		api.EXPECT().GetAgentByName(agentName).Return(agent, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Agent ... ")),
		api.EXPECT().ConfigureAgent(agent, defaultBranch).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Environment with Dashboard for Agent ... ")),
		api.EXPECT().ConfigureEnvironment(agent.ID, defaultEnvironmentName, defaultKubernetesNamespace, defaultFluxResourcePath).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Environment with Dashboard for FluxCD ... ")),
		api.EXPECT().ConfigureEnvironment(agent.ID, fluxEnvironmentName, fluxKubernetesNamespace, fluxFluxResourcePath).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Agent Token ... ")),
		api.EXPECT().CreateAgentToken(agent.ID).Return(nil, actualErr),
		stderr.EXPECT().Write([]byte("[FAILED]\n")),
		stderr.EXPECT().Write(ContainsBytes([]byte(actualErr.Error()))),
	)

	// WHEN
	err := exec(agentName)

	// THEN
	assert.ErrorIs(t, err, actualErr)
}

func TestAgentBootstrap_Error_createAgentTokenSecret(t *testing.T) {
	// GIVEN
	exec, api, _, stderr, kubectlWrapper, _ := setupCmdExec(t)

	defaultBranch := "main"
	agentName := "test-agent-name"
	agentTokenValue := "glagent-test-token"
	agent := &gitlab.Agent{ID: 1, Name: agentName}
	agentToken := &gitlab.AgentToken{ID: 42, Token: agentTokenValue}
	defaultEnvironmentName := "flux-system/gitlab-agent"
	defaultKubernetesNamespace := "gitlab-agent"
	defaultFluxResourcePath := "helm.toolkit.fluxcd.io/v2beta1/namespaces/flux-system/helmreleases/gitlab-agent"

	// Flux environment configuration
	fluxEnvironmentName := "flux-system/flux-system"
	fluxKubernetesNamespace := "flux-system"
	fluxFluxResourcePath := "kustomize.toolkit.fluxcd.io/v1/namespaces/flux-system/kustomizations/flux-system"

	actualErr := errors.New("dummy error")

	gomock.InOrder(
		api.EXPECT().GetDefaultBranch().Return(defaultBranch, nil),
		stderr.EXPECT().Write([]byte("Registering Agent ... ")),
		api.EXPECT().GetAgentByName(agentName).Return(agent, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Agent ... ")),
		api.EXPECT().ConfigureAgent(agent, defaultBranch).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Environment with Dashboard for Agent ... ")),
		api.EXPECT().ConfigureEnvironment(agent.ID, defaultEnvironmentName, defaultKubernetesNamespace, defaultFluxResourcePath).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Environment with Dashboard for FluxCD ... ")),
		api.EXPECT().ConfigureEnvironment(agent.ID, fluxEnvironmentName, fluxKubernetesNamespace, fluxFluxResourcePath).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Agent Token ... ")),
		api.EXPECT().CreateAgentToken(agent.ID).Return(agentToken, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Kubernetes Secret with Agent Token ... ")),
		kubectlWrapper.EXPECT().createAgentTokenSecret(int64(42), agentTokenValue).Return(actualErr),
		stderr.EXPECT().Write([]byte("[FAILED]\n")),
		stderr.EXPECT().Write(ContainsBytes([]byte(actualErr.Error()))),
	)

	// WHEN
	err := exec(agentName)

	// THEN
	assert.ErrorIs(t, err, actualErr)
}

func TestAgentBootstrap_Error_createHelmRepositoryManifest(t *testing.T) {
	// GIVEN
	exec, api, _, stderr, kubectlWrapper, fluxWrapper := setupCmdExec(t)

	defaultBranch := "main"
	agentName := "test-agent-name"
	agentTokenValue := "glagent-test-token"
	agent := &gitlab.Agent{ID: 1, Name: agentName}
	agentToken := &gitlab.AgentToken{ID: 42, Token: agentTokenValue}
	defaultEnvironmentName := "flux-system/gitlab-agent"
	defaultKubernetesNamespace := "gitlab-agent"
	defaultFluxResourcePath := "helm.toolkit.fluxcd.io/v2beta1/namespaces/flux-system/helmreleases/gitlab-agent"

	// Flux environment configuration
	fluxEnvironmentName := "flux-system/flux-system"
	fluxKubernetesNamespace := "flux-system"
	fluxFluxResourcePath := "kustomize.toolkit.fluxcd.io/v1/namespaces/flux-system/kustomizations/flux-system"

	actualErr := errors.New("dummy error")

	gomock.InOrder(
		api.EXPECT().GetDefaultBranch().Return(defaultBranch, nil),
		stderr.EXPECT().Write([]byte("Registering Agent ... ")),
		api.EXPECT().GetAgentByName(agentName).Return(agent, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Agent ... ")),
		api.EXPECT().ConfigureAgent(agent, defaultBranch).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Environment with Dashboard for Agent ... ")),
		api.EXPECT().ConfigureEnvironment(agent.ID, defaultEnvironmentName, defaultKubernetesNamespace, defaultFluxResourcePath).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Environment with Dashboard for FluxCD ... ")),
		api.EXPECT().ConfigureEnvironment(agent.ID, fluxEnvironmentName, fluxKubernetesNamespace, fluxFluxResourcePath).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Agent Token ... ")),
		api.EXPECT().CreateAgentToken(agent.ID).Return(agentToken, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Kubernetes Secret with Agent Token ... ")),
		kubectlWrapper.EXPECT().createAgentTokenSecret(int64(42), agentTokenValue).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Flux Helm Resources ... ")),
		fluxWrapper.EXPECT().createHelmRepositoryManifest().Return(file{}, actualErr),
		stderr.EXPECT().Write([]byte("[FAILED]\n")),
		stderr.EXPECT().Write(ContainsBytes([]byte(actualErr.Error()))),
	)

	// WHEN
	err := exec(agentName)

	// THEN
	assert.ErrorIs(t, err, actualErr)
}

func TestAgentBootstrap_Error_SyncFile_HelmRepositoryFile(t *testing.T) {
	// GIVEN
	exec, api, _, stderr, kubectlWrapper, fluxWrapper := setupCmdExec(t)

	defaultKASAddress := "wss://kas.gitlab.example.com"
	defaultBranch := "main"
	agentName := "test-agent-name"
	agentTokenValue := "glagent-test-token"
	agent := &gitlab.Agent{ID: 1, Name: agentName}
	agentToken := &gitlab.AgentToken{ID: 42, Token: agentTokenValue}
	helmRepositoryFile := file{path: "gitlab-helm-repository.yaml", content: []byte("any")}
	helmReleaseFile := file{path: "gitlab-agent-helm-release.yaml", content: []byte("any")}
	defaultEnvironmentName := "flux-system/gitlab-agent"
	defaultKubernetesNamespace := "gitlab-agent"
	defaultFluxResourcePath := "helm.toolkit.fluxcd.io/v2beta1/namespaces/flux-system/helmreleases/gitlab-agent"

	// Flux environment configuration
	fluxEnvironmentName := "flux-system/flux-system"
	fluxKubernetesNamespace := "flux-system"
	fluxFluxResourcePath := "kustomize.toolkit.fluxcd.io/v1/namespaces/flux-system/kustomizations/flux-system"

	actualErr := errors.New("dummy error")

	gomock.InOrder(
		api.EXPECT().GetDefaultBranch().Return(defaultBranch, nil),
		stderr.EXPECT().Write([]byte("Registering Agent ... ")),
		api.EXPECT().GetAgentByName(agentName).Return(agent, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Agent ... ")),
		api.EXPECT().ConfigureAgent(agent, defaultBranch).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Environment with Dashboard for Agent ... ")),
		api.EXPECT().ConfigureEnvironment(agent.ID, defaultEnvironmentName, defaultKubernetesNamespace, defaultFluxResourcePath).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Environment with Dashboard for FluxCD ... ")),
		api.EXPECT().ConfigureEnvironment(agent.ID, fluxEnvironmentName, fluxKubernetesNamespace, fluxFluxResourcePath).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Agent Token ... ")),
		api.EXPECT().CreateAgentToken(agent.ID).Return(agentToken, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Kubernetes Secret with Agent Token ... ")),
		kubectlWrapper.EXPECT().createAgentTokenSecret(int64(42), agentTokenValue).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Flux Helm Resources ... ")),
		fluxWrapper.EXPECT().createHelmRepositoryManifest().Return(helmRepositoryFile, nil),
		api.EXPECT().GetKASAddress().Return(defaultKASAddress, nil),
		fluxWrapper.EXPECT().createHelmReleaseManifest(defaultKASAddress).Return(helmReleaseFile, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Syncing Flux Helm Resources ... ")),
		api.EXPECT().SyncFile(helmRepositoryFile, defaultBranch).Return(actualErr),
		stderr.EXPECT().Write([]byte("[FAILED]\n")),
		stderr.EXPECT().Write(ContainsBytes([]byte(actualErr.Error()))),
	)

	// WHEN
	err := exec(agentName)

	// THEN
	assert.ErrorIs(t, err, actualErr)
}

func TestAgentBootstrap_Error_SyncFile_HelmReleaseFile(t *testing.T) {
	// GIVEN
	exec, api, _, stderr, kubectlWrapper, fluxWrapper := setupCmdExec(t)

	defaultKASAddress := "wss://kas.gitlab.example.com"
	defaultBranch := "main"
	agentName := "test-agent-name"
	agentTokenValue := "glagent-test-token"
	agent := &gitlab.Agent{ID: 1, Name: agentName}
	agentToken := &gitlab.AgentToken{ID: 42, Token: agentTokenValue}
	helmRepositoryFile := file{path: "gitlab-helm-repository.yaml", content: []byte("any")}
	helmReleaseFile := file{path: "gitlab-agent-helm-release.yaml", content: []byte("any")}
	defaultEnvironmentName := "flux-system/gitlab-agent"
	defaultKubernetesNamespace := "gitlab-agent"
	defaultFluxResourcePath := "helm.toolkit.fluxcd.io/v2beta1/namespaces/flux-system/helmreleases/gitlab-agent"

	// Flux environment configuration
	fluxEnvironmentName := "flux-system/flux-system"
	fluxKubernetesNamespace := "flux-system"
	fluxFluxResourcePath := "kustomize.toolkit.fluxcd.io/v1/namespaces/flux-system/kustomizations/flux-system"

	actualErr := errors.New("dummy error")

	gomock.InOrder(
		api.EXPECT().GetDefaultBranch().Return(defaultBranch, nil),
		stderr.EXPECT().Write([]byte("Registering Agent ... ")),
		api.EXPECT().GetAgentByName(agentName).Return(agent, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Agent ... ")),
		api.EXPECT().ConfigureAgent(agent, defaultBranch).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Environment with Dashboard for Agent ... ")),
		api.EXPECT().ConfigureEnvironment(agent.ID, defaultEnvironmentName, defaultKubernetesNamespace, defaultFluxResourcePath).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Environment with Dashboard for FluxCD ... ")),
		api.EXPECT().ConfigureEnvironment(agent.ID, fluxEnvironmentName, fluxKubernetesNamespace, fluxFluxResourcePath).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Agent Token ... ")),
		api.EXPECT().CreateAgentToken(agent.ID).Return(agentToken, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Kubernetes Secret with Agent Token ... ")),
		kubectlWrapper.EXPECT().createAgentTokenSecret(int64(42), agentTokenValue).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Flux Helm Resources ... ")),
		fluxWrapper.EXPECT().createHelmRepositoryManifest().Return(helmRepositoryFile, nil),
		api.EXPECT().GetKASAddress().Return(defaultKASAddress, nil),
		fluxWrapper.EXPECT().createHelmReleaseManifest(defaultKASAddress).Return(helmReleaseFile, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Syncing Flux Helm Resources ... ")),
		api.EXPECT().SyncFile(helmRepositoryFile, defaultBranch).Return(nil),
		api.EXPECT().SyncFile(helmReleaseFile, defaultBranch).Return(actualErr),
		stderr.EXPECT().Write([]byte("[FAILED]\n")),
		stderr.EXPECT().Write(ContainsBytes([]byte(actualErr.Error()))),
	)

	// WHEN
	err := exec(agentName)

	// THEN
	assert.ErrorIs(t, err, actualErr)
}

func TestAgentBootstrap_Error_reconcile(t *testing.T) {
	// GIVEN
	exec, api, _, stderr, kubectlWrapper, fluxWrapper := setupCmdExec(t)

	defaultKASAddress := "wss://kas.gitlab.example.com"
	defaultBranch := "main"
	agentName := "test-agent-name"
	agentTokenValue := "glagent-test-token"
	agent := &gitlab.Agent{ID: 1, Name: agentName}
	agentToken := &gitlab.AgentToken{ID: 42, Token: agentTokenValue}
	helmRepositoryFile := file{path: "gitlab-helm-repository.yaml", content: []byte("any")}
	helmReleaseFile := file{path: "gitlab-agent-helm-release.yaml", content: []byte("any")}
	defaultEnvironmentName := "flux-system/gitlab-agent"
	defaultKubernetesNamespace := "gitlab-agent"
	defaultFluxResourcePath := "helm.toolkit.fluxcd.io/v2beta1/namespaces/flux-system/helmreleases/gitlab-agent"

	// Flux environment configuration
	fluxEnvironmentName := "flux-system/flux-system"
	fluxKubernetesNamespace := "flux-system"
	fluxFluxResourcePath := "kustomize.toolkit.fluxcd.io/v1/namespaces/flux-system/kustomizations/flux-system"

	actualErr := errors.New("dummy error")

	gomock.InOrder(
		api.EXPECT().GetDefaultBranch().Return(defaultBranch, nil),
		stderr.EXPECT().Write([]byte("Registering Agent ... ")),
		api.EXPECT().GetAgentByName(agentName).Return(agent, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Agent ... ")),
		api.EXPECT().ConfigureAgent(agent, defaultBranch).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Environment with Dashboard for Agent ... ")),
		api.EXPECT().ConfigureEnvironment(agent.ID, defaultEnvironmentName, defaultKubernetesNamespace, defaultFluxResourcePath).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Environment with Dashboard for FluxCD ... ")),
		api.EXPECT().ConfigureEnvironment(agent.ID, fluxEnvironmentName, fluxKubernetesNamespace, fluxFluxResourcePath).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Agent Token ... ")),
		api.EXPECT().CreateAgentToken(agent.ID).Return(agentToken, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Kubernetes Secret with Agent Token ... ")),
		kubectlWrapper.EXPECT().createAgentTokenSecret(int64(42), agentTokenValue).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Flux Helm Resources ... ")),
		fluxWrapper.EXPECT().createHelmRepositoryManifest().Return(helmRepositoryFile, nil),
		api.EXPECT().GetKASAddress().Return(defaultKASAddress, nil),
		fluxWrapper.EXPECT().createHelmReleaseManifest(defaultKASAddress).Return(helmReleaseFile, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Syncing Flux Helm Resources ... ")),
		api.EXPECT().SyncFile(helmRepositoryFile, defaultBranch).Return(nil),
		api.EXPECT().SyncFile(helmReleaseFile, defaultBranch).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Reconciling Flux Helm Resources ... ")),
		stderr.EXPECT().Write([]byte("Output from flux command:\n")),
		fluxWrapper.EXPECT().reconcile().Return(actualErr),
		stderr.EXPECT().Write(ContainsBytes([]byte(actualErr.Error()))),
	)

	// WHEN
	err := exec(agentName)

	// THEN
	assert.ErrorIs(t, err, actualErr)
}

func TestAgentBootstrap_HappyPath_WithoutFlux(t *testing.T) {
	// GIVEN
	exec, api, _, stderr, kubectlWrapper, fluxWrapper := setupCmdExec(t)

	defaultKASAddress := "wss://kas.gitlab.example.com"
	defaultBranch := "main"
	agentName := "test-agent-name"
	agentTokenValue := "glagent-test-token"
	agent := &gitlab.Agent{ID: 1, Name: agentName}
	agentToken := &gitlab.AgentToken{ID: 42, Token: agentTokenValue}
	helmRepositoryFile := file{path: "gitlab-helm-repository.yaml", content: []byte("any")}
	helmReleaseFile := file{path: "gitlab-agent-helm-release.yaml", content: []byte("any")}
	defaultEnvironmentName := "flux-system/gitlab-agent"
	defaultKubernetesNamespace := "gitlab-agent"
	defaultFluxResourcePath := "helm.toolkit.fluxcd.io/v2beta1/namespaces/flux-system/helmreleases/gitlab-agent"

	gomock.InOrder(
		api.EXPECT().GetDefaultBranch().Return(defaultBranch, nil),
		stderr.EXPECT().Write([]byte("Registering Agent ... ")),
		api.EXPECT().GetAgentByName(agentName).Return(agent, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Agent ... ")),
		api.EXPECT().ConfigureAgent(agent, defaultBranch).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Environment with Dashboard for Agent ... ")),
		api.EXPECT().ConfigureEnvironment(agent.ID, defaultEnvironmentName, defaultKubernetesNamespace, defaultFluxResourcePath).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Configuring Environment with Dashboard for FluxCD ... ")),
		stderr.EXPECT().Write([]byte("[SKIPPED]\n")),
		stderr.EXPECT().Write([]byte("Creating Agent Token ... ")),
		api.EXPECT().CreateAgentToken(agent.ID).Return(agentToken, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Kubernetes Secret with Agent Token ... ")),
		kubectlWrapper.EXPECT().createAgentTokenSecret(int64(42), agentTokenValue).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Creating Flux Helm Resources ... ")),
		fluxWrapper.EXPECT().createHelmRepositoryManifest().Return(helmRepositoryFile, nil),
		api.EXPECT().GetKASAddress().Return(defaultKASAddress, nil),
		fluxWrapper.EXPECT().createHelmReleaseManifest(defaultKASAddress).Return(helmReleaseFile, nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Syncing Flux Helm Resources ... ")),
		api.EXPECT().SyncFile(helmRepositoryFile, defaultBranch).Return(nil),
		api.EXPECT().SyncFile(helmReleaseFile, defaultBranch).Return(nil),
		stderr.EXPECT().Write([]byte("[OK]\n")),
		stderr.EXPECT().Write([]byte("Reconciling Flux Helm Resources ... ")),
		stderr.EXPECT().Write([]byte("Output from flux command:\n")),
		fluxWrapper.EXPECT().reconcile().Return(nil),
		stderr.EXPECT().Write([]byte("Successfully bootstrapped the GitLab Agent\n")),
	)

	// WHEN
	err := exec(agentName + " --create-flux-environment=false")

	// THEN
	assert.NoError(t, err)
}

type execFunc func(cli string) error

func setupCmdExec(t *testing.T) (execFunc, *MockAPI, *MockWriter, *MockWriter, *MockKubectlWrapper, *MockFluxWrapper) { //nolint:unparam
	t.Helper()

	ctrl := gomock.NewController(t)
	mockAPI := NewMockAPI(ctrl)
	mockStdout := NewMockWriter(ctrl)
	mockStderr := NewMockWriter(ctrl)
	mockKubectlWrapper := NewMockKubectlWrapper(ctrl)
	mockFluxWrapper := NewMockFluxWrapper(ctrl)
	mockCmd := NewMockCmd(ctrl)

	cmd := NewCmdAgentBootstrap(
		cmdtest.NewTestFactory(
			&iostreams.IOStreams{
				In:       io.NopCloser(&bytes.Buffer{}),
				StdOut:   mockStdout,
				StdErr:   mockStderr,
				IsaTTY:   true,
				IsInTTY:  true,
				IsErrTTY: true,
			},
		),
		func() error { return nil },
		func(*gitlab.Client, any, *commitAuthor) API { return mockAPI },
		func(_ Cmd, _, _, _ string) KubectlWrapper { return mockKubectlWrapper },
		func(_ Cmd, _, _, _, _, _, _, _, _, _, _ string, _, _ []string, _, _, _ string) FluxWrapper {
			return mockFluxWrapper
		},
		func(_, _ io.Writer, _ []string) Cmd { return mockCmd },
	)

	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(mockStdout)
	cmd.SetErr(mockStderr)

	// Set on root cmd, thus we also need to set it here.
	// cmd.SilenceErrors = true
	cmd.SilenceUsage = true

	exec := func(cli string) error {
		argv, err := shlex.Split(cli)
		if err != nil {
			return err
		}

		cmd.SetArgs(argv)
		_, err = cmd.ExecuteC()
		return err
	}

	return exec, mockAPI, mockStdout, mockStderr, mockKubectlWrapper, mockFluxWrapper
}

func ContainsBytes(b []byte) gomock.Matcher {
	return &containsBytesMatcher{b: b}
}

type containsBytesMatcher struct {
	b       []byte
	actualB []byte
}

func (m containsBytesMatcher) Matches(arg any) bool {
	b, ok := arg.([]byte)
	if !ok {
		return false
	}
	m.actualB = b
	return bytes.Contains(m.actualB, m.b)
}

func (m containsBytesMatcher) String() string {
	return fmt.Sprintf("does not contain: %q, got %q", m.b, m.actualB)
}
