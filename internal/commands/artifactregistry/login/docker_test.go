//go:build !integration

package login

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

const testHostname = "gitlab.example.com"

// writeFakeGlab drops an executable file named "glab" (or "glab.exe" on
// Windows) into dir, so exec.LookPath("glab") succeeds against a PATH
// containing dir, without depending on a real glab build.
func writeFakeGlab(t *testing.T, dir string) {
	t.Helper()

	name := "glab"
	if runtime.GOOS == "windows" {
		name = "glab.exe"
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755))
}

// setPath points PATH (and only PATH) at dirs, so exec.LookPath("glab")
// resolves exclusively against the fake binaries the test controls.
func setPath(t *testing.T, dirs ...string) {
	t.Helper()
	t.Setenv("PATH", strings.Join(dirs, string(os.PathListSeparator)))
}

// setHome points HOME (and USERPROFILE, for Windows parity) at dir.
func setHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	// dockercredhelper.ConfigDir prefers $DOCKER_CONFIG over HOME, so clear
	// it: an ambient value in the developer's shell would otherwise send
	// loginDocker's writes outside the sandbox this helper sets up.
	t.Setenv("DOCKER_CONFIG", "")
	// The same hazard for the other writers: each prefers its own variable
	// over the home directory, so a developer who has one set would have this
	// package's tests write into their real gradle.properties or .npmrc, and
	// fail. make test clears VISUAL, EDITOR, PAGER and GITLAB_TOKEN, not
	// these. The two tests that exercise an override set it after this helper
	// has run.
	t.Setenv("GRADLE_USER_HOME", "")
	t.Setenv("npm_config_userconfig", "")
	t.Setenv("NPM_CONFIG_USERCONFIG", "")
}

// failingConfig wraps a config.Config and makes reads of failKey return err,
// standing in for the failures a config read surfaces in production: a
// structurally broken host entry, or a keyring that is locked or denies
// access.
type failingConfig struct {
	config.Config
	failKey string
	err     error
}

func (c failingConfig) GetWithSource(hostname, key string, searchENVVars bool) (string, string, error) {
	if key == c.failKey {
		return "", "", c.err
	}
	return c.Config.GetWithSource(hostname, key, searchENVVars)
}

func (c failingConfig) Get(hostname, key string) (string, error) {
	if key == c.failKey {
		return "", c.err
	}
	return c.Config.Get(hostname, key)
}

// writeFailingConfig wraps a config.Config whose Write fails, standing in for
// an unwritable configuration file: a read-only directory, or a full disk.
type writeFailingConfig struct {
	config.Config
	err error
}

func (c writeFailingConfig) Write() error { return c.err }

func newFixtureConfig(t *testing.T) config.Config {
	t.Helper()
	return config.NewFromString(`
---
hosts:
  ` + testHostname + `:
    token: token1
`)
}

func readDockerConfig(t *testing.T, home string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".docker", "config.json"))
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))
	return parsed
}

func TestLoginDocker_InstallsShim(t *testing.T) {
	binDir := t.TempDir()
	home := t.TempDir()
	writeFakeGlab(t, binDir)
	setPath(t, binDir)
	setHome(t, home)

	ios, _, _, _ := cmdtest.TestIOStreams()
	cfg := newFixtureConfig(t)

	err := loginDocker(ios, cfg, testHostname, "registry.example.com")
	require.NoError(t, err)

	shimPath := filepath.Join(binDir, "docker-credential-glab")
	info, statErr := os.Stat(shimPath)
	require.NoError(t, statErr)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())

	content, readErr := os.ReadFile(shimPath)
	require.NoError(t, readErr)
	assert.Equal(t, "#!/bin/sh -eu\nglab auth docker-helper \"$@\"\n", string(content))
}

func TestLoginDocker_WritesCredHelpers(t *testing.T) {
	binDir := t.TempDir()
	home := t.TempDir()
	writeFakeGlab(t, binDir)
	setPath(t, binDir)
	setHome(t, home)

	ios, _, _, _ := cmdtest.TestIOStreams()
	cfg := newFixtureConfig(t)

	err := loginDocker(ios, cfg, testHostname, "registry.example.com")
	require.NoError(t, err)

	dockerCfg := readDockerConfig(t, home)
	credHelpers, ok := dockerCfg["credHelpers"].(map[string]any)
	require.True(t, ok, "credHelpers should be present in ~/.docker/config.json")
	assert.Equal(t, "glab", credHelpers["registry.example.com"])
}

func TestLoginDocker_WritesCredHelpersToDockerConfigEnv(t *testing.T) {
	binDir := t.TempDir()
	home := t.TempDir()
	dockerDir := t.TempDir()
	writeFakeGlab(t, binDir)
	setPath(t, binDir)
	setHome(t, home)
	t.Setenv("DOCKER_CONFIG", dockerDir)

	ios, _, _, _ := cmdtest.TestIOStreams()
	cfg := newFixtureConfig(t)

	err := loginDocker(ios, cfg, testHostname, "registry.example.com")
	require.NoError(t, err)

	data, readErr := os.ReadFile(filepath.Join(dockerDir, "config.json"))
	require.NoError(t, readErr)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))

	credHelpers, ok := parsed["credHelpers"].(map[string]any)
	require.True(t, ok, "credHelpers should be present in $DOCKER_CONFIG/config.json")
	assert.Equal(t, "glab", credHelpers["registry.example.com"])

	// $DOCKER_CONFIG wins outright: the HOME-derived path is where Docker
	// would not look, so writing there instead would fail silently.
	_, statErr := os.Stat(filepath.Join(home, ".docker", "config.json"))
	assert.True(t, os.IsNotExist(statErr), "~/.docker/config.json should not have been created")
}

func TestLoginDocker_RegistersArtifactRegistryDomain(t *testing.T) {
	binDir := t.TempDir()
	home := t.TempDir()
	writeFakeGlab(t, binDir)
	setPath(t, binDir)
	setHome(t, home)

	ios, _, _, _ := cmdtest.TestIOStreams()
	cfg := newFixtureConfig(t)

	err := loginDocker(ios, cfg, testHostname, "registry.example.com")
	require.NoError(t, err)

	domains, getErr := cfg.Get(testHostname, "artifact_registry_domains")
	require.NoError(t, getErr)
	assert.Contains(t, domains, "registry.example.com")
}

func TestLoginDocker_GlabNotOnPath(t *testing.T) {
	emptyBinDir := t.TempDir()
	home := t.TempDir()
	setPath(t, emptyBinDir)
	setHome(t, home)

	ios, _, _, _ := cmdtest.TestIOStreams()
	cfg := newFixtureConfig(t)

	preDomains, err := cfg.Get(testHostname, "artifact_registry_domains")
	require.NoError(t, err)

	err = loginDocker(ios, cfg, testHostname, "registry.example.com")
	require.Error(t, err)

	// Partial-failure guard: nothing should have been written.
	_, statErr := os.Stat(filepath.Join(home, ".docker", "config.json"))
	assert.True(t, os.IsNotExist(statErr), "docker config.json should not have been created")

	postDomains, err := cfg.Get(testHostname, "artifact_registry_domains")
	require.NoError(t, err)
	assert.Equal(t, preDomains, postDomains)
}

func TestLoginDocker_RunningTwiceDoesNotDuplicateDomain(t *testing.T) {
	binDir := t.TempDir()
	home := t.TempDir()
	writeFakeGlab(t, binDir)
	setPath(t, binDir)
	setHome(t, home)

	ios, _, _, _ := cmdtest.TestIOStreams()
	cfg := newFixtureConfig(t)

	require.NoError(t, loginDocker(ios, cfg, testHostname, "registry.example.com"))
	require.NoError(t, loginDocker(ios, cfg, testHostname, "registry.example.com"))

	domains, err := cfg.Get(testHostname, "artifact_registry_domains")
	require.NoError(t, err)

	count := 0
	for _, d := range config.ParseDomains(domains) {
		if d == "registry.example.com" {
			count++
		}
	}
	assert.Equal(t, 1, count, "registry.example.com should appear exactly once in %q", domains)
}

// TestLoginDocker_AlreadyRecordedDomainSkipsTheWrite pins that a re-login
// whose domain is already recorded writes nothing: the config write is skipped,
// not just the append. Rewriting the file from a snapshot taken before a
// concurrent login would drop the entry that login had added.
//
// The config's Write fails, so the test passes only if it was never called.
func TestLoginDocker_AlreadyRecordedDomainSkipsTheWrite(t *testing.T) {
	binDir := t.TempDir()
	home := t.TempDir()
	writeFakeGlab(t, binDir)
	setPath(t, binDir)
	setHome(t, home)

	ios, _, _, _ := cmdtest.TestIOStreams()
	cfg := writeFailingConfig{
		Config: config.NewFromString(`
---
hosts:
  ` + testHostname + `:
    token: token1
    artifact_registry_domains: registry.example.com
`),
		err: errors.New("disk is full"),
	}

	require.NoError(t, loginDocker(ios, cfg, testHostname, "registry.example.com"))
}

// TestLoginDocker_WarnsOnShadowedDockerLogin covers the credential `docker
// login` left in config.json. Docker consults credHelpers ahead of it, so it is
// now dead weight the user has to be told about, or they will keep believing
// that stored credential is what authenticates their pulls.
func TestLoginDocker_WarnsOnShadowedDockerLogin(t *testing.T) {
	binDir := t.TempDir()
	home := t.TempDir()
	writeFakeGlab(t, binDir)
	setPath(t, binDir)
	setHome(t, home)

	dockerDir := filepath.Join(home, ".docker")
	require.NoError(t, os.MkdirAll(dockerDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dockerDir, "config.json"),
		[]byte(`{"auths":{"registry.example.com":{"auth":"dXNlcjpwYXNzd29yZA=="}}}`), 0o600))

	ios, _, _, errOut := cmdtest.TestIOStreams()
	cfg := newFixtureConfig(t)

	err := loginDocker(ios, cfg, testHostname, "registry.example.com")
	require.NoError(t, err)
	assert.Contains(t, errOut.String(), "registry.example.com already had credentials from `docker login`")
	assert.Contains(t, errOut.String(), "docker logout registry.example.com")
}

func TestLoginDocker_NoShadowedLoginWarningWithoutStoredCredentials(t *testing.T) {
	binDir := t.TempDir()
	home := t.TempDir()
	writeFakeGlab(t, binDir)
	setPath(t, binDir)
	setHome(t, home)

	ios, _, _, errOut := cmdtest.TestIOStreams()
	cfg := newFixtureConfig(t)

	err := loginDocker(ios, cfg, testHostname, "registry.example.com")
	require.NoError(t, err)
	assert.NotContains(t, errOut.String(), "already had credentials from `docker login`")
}

// TestLoginDocker_WarnsWhenAnotherHostResolvesTheRegistry covers a domain
// already recorded under an earlier host. artifact_registry_domains is per-host,
// but the credential helper takes the first host that lists the domain, so this
// login would otherwise report success while `docker pull` keeps authenticating
// against the other host.
func TestLoginDocker_WarnsWhenAnotherHostResolvesTheRegistry(t *testing.T) {
	binDir := t.TempDir()
	home := t.TempDir()
	writeFakeGlab(t, binDir)
	setPath(t, binDir)
	setHome(t, home)

	ios, _, _, errOut := cmdtest.TestIOStreams()
	// cfg.Hosts() hoists gitlab.com to the front whatever the file's order, so
	// it is the host the credential helper resolves the domain through.
	cfg := config.NewFromString(`
---
hosts:
  ` + testHostname + `:
    token: token1
  ` + glinstance.DefaultHostname + `:
    token: token0
    artifact_registry_domains: registry.example.com
`)

	err := loginDocker(ios, cfg, testHostname, "registry.example.com")
	require.NoError(t, err)
	assert.Contains(t, errOut.String(), "also listed in artifact_registry_domains for "+glinstance.DefaultHostname)
	assert.Contains(t, errOut.String(), "docker pull authenticates against "+glinstance.DefaultHostname+", not "+testHostname)
}

// TestLoginDocker_NoCrossHostWarningWhenThisHostResolvesFirst is the case that
// must stay quiet: another host lists the domain too, but the host being
// configured comes first, so it is the one Docker resolves.
func TestLoginDocker_NoCrossHostWarningWhenThisHostResolvesFirst(t *testing.T) {
	binDir := t.TempDir()
	home := t.TempDir()
	writeFakeGlab(t, binDir)
	setPath(t, binDir)
	setHome(t, home)

	ios, _, _, errOut := cmdtest.TestIOStreams()
	cfg := config.NewFromString(`
---
hosts:
  ` + glinstance.DefaultHostname + `:
    token: token0
  ` + testHostname + `:
    token: token1
    artifact_registry_domains: registry.example.com
`)

	err := loginDocker(ios, cfg, glinstance.DefaultHostname, "registry.example.com")
	require.NoError(t, err)
	assert.NotContains(t, errOut.String(), "also listed in artifact_registry_domains")
}

func TestLoginDocker_RefusesToReplaceAnotherHelper(t *testing.T) {
	binDir := t.TempDir()
	home := t.TempDir()
	writeFakeGlab(t, binDir)
	setPath(t, binDir)
	setHome(t, home)

	dockerDir := filepath.Join(home, ".docker")
	require.NoError(t, os.MkdirAll(dockerDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dockerDir, "config.json"),
		[]byte(`{"credHelpers":{"registry.example.com":"ecr-login"}}`), 0o600))

	ios, _, _, _ := cmdtest.TestIOStreams()
	cfg := newFixtureConfig(t)

	err := loginDocker(ios, cfg, testHostname, "registry.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ecr-login")

	dockerCfg := readDockerConfig(t, home)
	credHelpers, ok := dockerCfg["credHelpers"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "ecr-login", credHelpers["registry.example.com"], "the existing helper must survive the refusal")

	domains, getErr := cfg.Get(testHostname, "artifact_registry_domains")
	require.NoError(t, getErr)
	assert.Empty(t, domains, "artifact_registry_domains must not be written when the Docker write is refused")
}

// TestLoginDocker_DomainReadFailureNamesTheUnrecordedRegistry covers the read
// that sits between Register and the config write. It leaves exactly the state
// a failed write leaves, so it has to report it the same way.
func TestLoginDocker_DomainReadFailureNamesTheUnrecordedRegistry(t *testing.T) {
	binDir := t.TempDir()
	home := t.TempDir()
	writeFakeGlab(t, binDir)
	setPath(t, binDir)
	setHome(t, home)

	ios, _, _, _ := cmdtest.TestIOStreams()
	cfg := failingConfig{
		Config:  newFixtureConfig(t),
		failKey: "artifact_registry_domains",
		err:     errors.New("keyring is locked"),
	}

	err := loginDocker(ios, cfg, testHostname, "registry.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "keyring is locked")
	assert.Contains(t, err.Error(), "docker pull fails against it until this command is rerun")
}

// TestLoginDocker_ConfigWriteFailureNamesTheUnrecordedRegistry covers the state
// left when the config write fails after credHelpers already points at glab:
// Docker calls the helper, which cannot resolve the registry. The error has to
// say so, since a bare write failure reads as if nothing had changed.
func TestLoginDocker_ConfigWriteFailureNamesTheUnrecordedRegistry(t *testing.T) {
	binDir := t.TempDir()
	home := t.TempDir()
	writeFakeGlab(t, binDir)
	setPath(t, binDir)
	setHome(t, home)

	ios, _, _, _ := cmdtest.TestIOStreams()
	cfg := writeFailingConfig{Config: newFixtureConfig(t), err: errors.New("disk is full")}

	err := loginDocker(ios, cfg, testHostname, "registry.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disk is full")
	assert.Contains(t, err.Error(), "docker pull fails against it until this command is rerun")

	credHelpers, ok := readDockerConfig(t, home)["credHelpers"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "glab", credHelpers["registry.example.com"], "the error describes a credHelpers entry that is really there")
}

// TestLoginDocker_WarnsOnDualListedDomain covers the dual-listing the default
// `glab auth login` config creates: registry.gitlab.com in
// container_registry_domains, then registered here as an artifact registry.
// The warning has to say the container-registry fallback will not fire, since
// run only reaches loginDocker once the exchange has succeeded, and it has to
// lead with undoing this login rather than with dropping the
// container_registry_domains entry.
func TestLoginDocker_WarnsOnDualListedDomain(t *testing.T) {
	binDir := t.TempDir()
	home := t.TempDir()
	writeFakeGlab(t, binDir)
	setPath(t, binDir)
	setHome(t, home)

	ios, _, _, errOut := cmdtest.TestIOStreams()
	cfg := config.NewFromString(`
---
hosts:
  ` + testHostname + `:
    token: token1
    container_registry_domains: registry.example.com
`)

	err := loginDocker(ios, cfg, testHostname, "registry.example.com")
	require.NoError(t, err)

	warning := errOut.String()
	assert.Contains(t, warning, "also listed in container_registry_domains")
	assert.Contains(t, warning, "the fallback will not fire")

	undo := strings.Index(warning, "glab config set artifact_registry_domains <remaining domains> --host "+testHostname)
	drop := strings.Index(warning, "glab config set container_registry_domains <remaining domains> --host "+testHostname)
	require.NotEqual(t, -1, undo, "the warning must offer undoing this login")
	require.NotEqual(t, -1, drop, "the warning must keep the container_registry_domains remedy")
	assert.Less(t, undo, drop, "the remedy for a container registry comes first: that is the case where docker pull just broke")
}

// TestLoginDocker_DualListedWarningNamesEachHost pins that the two remedies
// name the host each key is set on. container_registry_domains is read across
// every host, so the domain can be listed under one host while this login
// records it under another, and a single --host would send one of the two
// `glab config set` commands to the wrong file.
func TestLoginDocker_DualListedWarningNamesEachHost(t *testing.T) {
	const otherHostname = "gitlab.other.example.com"

	binDir := t.TempDir()
	home := t.TempDir()
	writeFakeGlab(t, binDir)
	setPath(t, binDir)
	setHome(t, home)

	ios, _, _, errOut := cmdtest.TestIOStreams()
	cfg := config.NewFromString(`
---
hosts:
  ` + testHostname + `:
    token: token1
  ` + otherHostname + `:
    token: token2
    container_registry_domains: registry.example.com
`)

	err := loginDocker(ios, cfg, testHostname, "registry.example.com")
	require.NoError(t, err)

	warning := errOut.String()
	assert.Contains(t, warning, "glab config set artifact_registry_domains <remaining domains> --host "+testHostname)
	assert.Contains(t, warning, "glab config set container_registry_domains <remaining domains> --host "+otherHostname)
}

func TestLoginDocker_NoDualListedWarningWhenNotShared(t *testing.T) {
	binDir := t.TempDir()
	home := t.TempDir()
	writeFakeGlab(t, binDir)
	setPath(t, binDir)
	setHome(t, home)

	ios, _, _, errOut := cmdtest.TestIOStreams()
	cfg := newFixtureConfig(t)

	err := loginDocker(ios, cfg, testHostname, "registry.example.com")
	require.NoError(t, err)
	assert.NotContains(t, errOut.String(), "also listed in container_registry_domains")
}
