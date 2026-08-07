//go:build !integration

package docker

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// failingConfig wraps a config.Config and makes reads of failKey return err,
// standing in for the failures GetWithSource surfaces in production: a
// structurally broken host entry, or a keyring that is locked or denies
// access. When failHost is set, only that host's reads fail.
type failingConfig struct {
	config.Config
	failHost string
	failKey  string
	err      error
}

func (c failingConfig) GetWithSource(hostname, key string, searchENVVars bool) (string, string, error) {
	if key == c.failKey && (c.failHost == "" || hostname == c.failHost) {
		return "", "", c.err
	}
	return c.Config.GetWithSource(hostname, key, searchENVVars)
}

// sandboxDocker points PATH at a directory holding a fake glab binary, and HOME
// and $DOCKER_CONFIG at scratch directories, so the shim install and the
// config.json write both land inside the test's sandbox. It returns the
// $DOCKER_CONFIG directory.
func sandboxDocker(t *testing.T) string {
	t.Helper()

	binDir := t.TempDir()
	name := "glab"
	if runtime.GOOS == "windows" {
		name = "glab.exe"
	}
	require.NoError(t, os.WriteFile(filepath.Join(binDir, name), []byte("#!/bin/sh\n"), 0o755))

	home := t.TempDir()
	dockerDir := t.TempDir()
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DOCKER_CONFIG", dockerDir)

	return dockerDir
}

func TestConfigureDocker_RegistersConfiguredDomains(t *testing.T) {
	dockerDir := sandboxDocker(t)

	cfg := config.NewFromString(`
---
hosts:
  gitlab.example.com:
    token: token1
    container_registry_domains: registry.gitlab.example.com
`)
	ios, _, out, _ := cmdtest.TestIOStreams()

	require.NoError(t, configureDocker(ios, cfg))
	assert.Contains(t, out.String(), "registry.gitlab.example.com")

	data, err := os.ReadFile(filepath.Join(dockerDir, "config.json"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "registry.gitlab.example.com")
}

// TestConfigureDocker_ReportsUnreadableDomains covers the message a user gets
// when their container_registry_domains cannot be read. Telling them to go
// configure a domain would misdirect: the domain is configured, it just could
// not be read.
func TestConfigureDocker_ReportsUnreadableDomains(t *testing.T) {
	sandboxDocker(t)

	cfg := failingConfig{
		Config: config.NewFromString(`
---
hosts:
  gitlab.example.com:
    token: token1
    container_registry_domains: registry.gitlab.example.com
`),
		failKey: "container_registry_domains",
		err:     errors.New("keyring is locked"),
	}
	ios, _, _, errOut := cmdtest.TestIOStreams()

	err := configureDocker(ios, cfg)
	require.ErrorContains(t, err, "keyring is locked")
	assert.NotContains(t, err.Error(), "no hosts were configured")
	assert.Contains(t, errOut.String(), "Skipped gitlab.example.com")
}

// TestConfigureDocker_KeepsGoingPastAnUnreadableHost pins that one broken host
// does not stop the others from being configured, even though the overall
// command still reports the read failure via a non-zero exit.
func TestConfigureDocker_KeepsGoingPastAnUnreadableHost(t *testing.T) {
	sandboxDocker(t)

	cfg := failingConfig{
		Config: config.NewFromString(`
---
hosts:
  broken.example.com:
    token: token1
  gdk.example.com:
    token: token2
    container_registry_domains: registry.gdk.example.com
`),
		failHost: "broken.example.com",
		failKey:  "container_registry_domains",
		err:      errors.New("keyring is locked"),
	}
	ios, _, out, errOut := cmdtest.TestIOStreams()

	err := configureDocker(ios, cfg)
	require.ErrorContains(t, err, "keyring is locked")
	assert.Contains(t, errOut.String(), "Skipped broken.example.com")
	assert.Contains(t, out.String(), "registry.gdk.example.com")
}

// TestConfigureDocker_IgnoresTrailingCommaInDomains reproduces a trailing
// comma in container_registry_domains: parseDomains must drop the empty
// segment it produces, or it lands as an empty-string key in credHelpers.
func TestConfigureDocker_IgnoresTrailingCommaInDomains(t *testing.T) {
	dockerDir := sandboxDocker(t)

	cfg := config.NewFromString(`
---
hosts:
  gitlab.example.com:
    token: token1
    container_registry_domains: registry.gitlab.example.com,
`)
	ios, _, out, _ := cmdtest.TestIOStreams()

	require.NoError(t, configureDocker(ios, cfg))
	assert.Contains(t, out.String(), "registry.gitlab.example.com")

	data, err := os.ReadFile(filepath.Join(dockerDir, "config.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(data), `"":`, "a trailing comma must not write an empty-string domain")
}

func TestConfigureDocker_NoDomainsConfigured(t *testing.T) {
	sandboxDocker(t)

	cfg := config.NewFromString(`
---
hosts:
  gitlab.example.com:
    token: token1
`)
	ios, _, _, _ := cmdtest.TestIOStreams()

	require.ErrorContains(t, configureDocker(ios, cfg), "no hosts were configured")
}

// TestConfigureDocker_RefusesToReplaceAnotherHelper checks that the guard in
// dockercredhelper.Register surfaces through the command rather than silently
// taking the registry away from the tool that owns it.
func TestConfigureDocker_RefusesToReplaceAnotherHelper(t *testing.T) {
	dockerDir := sandboxDocker(t)
	require.NoError(t, os.WriteFile(filepath.Join(dockerDir, "config.json"),
		[]byte(`{"credHelpers":{"registry.gitlab.example.com":"ecr-login"}}`), 0o600))

	cfg := config.NewFromString(`
---
hosts:
  gitlab.example.com:
    token: token1
    container_registry_domains: registry.gitlab.example.com
`)
	ios, _, _, _ := cmdtest.TestIOStreams()

	err := configureDocker(ios, cfg)
	require.ErrorContains(t, err, "ecr-login")

	data, readErr := os.ReadFile(filepath.Join(dockerDir, "config.json"))
	require.NoError(t, readErr)
	assert.Contains(t, string(data), "ecr-login", "the existing helper must survive")
}

func TestConfigureDocker_WarnsWhenShadowingADockerLogin(t *testing.T) {
	dockerDir := sandboxDocker(t)
	require.NoError(t, os.WriteFile(filepath.Join(dockerDir, "config.json"),
		[]byte(`{"auths":{"registry.gitlab.example.com":{"auth":"dXNlcjpwYXNz"}}}`), 0o600))

	cfg := config.NewFromString(`
---
hosts:
  gitlab.example.com:
    token: token1
    container_registry_domains: registry.gitlab.example.com
`)
	ios, _, _, errOut := cmdtest.TestIOStreams()

	require.NoError(t, configureDocker(ios, cfg))
	assert.Contains(t, errOut.String(), "no longer used")
	assert.Contains(t, errOut.String(), "docker logout registry.gitlab.example.com")
}
