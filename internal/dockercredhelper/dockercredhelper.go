// Package dockercredhelper wires glab into Docker as a credential helper: it
// installs the docker-credential-glab shim that Docker executes, and points
// Docker's config.json at that shim for a set of registry domains.
package dockercredhelper

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	dockerconfig "github.com/docker/cli/cli/config"
)

const (
	// FullName is the shim's file name, and is what Docker looks for in the
	// user's PATH.
	FullName = "docker-credential-glab"
	// ShortName is the short name of the credential helper, and is what a
	// Docker config's credHelpers object lists for a registry.
	ShortName = "glab"
)

// script is the shim Docker executes. It delegates straight to
// `glab auth docker-helper`, which reads the requested registry on stdin.
var script = []byte("#!/bin/sh -eu\nglab auth docker-helper \"$@\"\n")

// Registration reports the outcome of pointing Docker at glab for one domain.
type Registration struct {
	Domain string
	// ShadowedLogin is true when Docker's config.json already held a
	// credential for Domain, which `docker login` writes. Docker consults
	// credHelpers ahead of that entry, so the stored credential is now unused
	// and the caller should say so.
	ShadowedLogin bool
}

// ConflictError reports domains that a different credential helper already
// claims. Register returns it having written nothing.
type ConflictError struct {
	// Helpers maps a domain to the credential helper already configured for it.
	Helpers map[string]string
	// ConfigPath is the config.json holding those entries.
	ConfigPath string
}

func (e *ConflictError) Error() string {
	domains := slices.Sorted(maps.Keys(e.Helpers))

	claims := make([]string, 0, len(domains))
	for _, domain := range domains {
		claims = append(claims, fmt.Sprintf("%s is handled by %q", domain, e.Helpers[domain]))
	}

	// No trailing period: the CLI's error renderer appends one.
	return fmt.Sprintf(
		"refusing to replace another Docker credential helper: %s. "+
			"Remove the matching credHelpers entries from %s if you want %s to manage these registries",
		strings.Join(claims, "; "), e.ConfigPath, ShortName)
}

// Register points Docker at the glab credential helper for each of domains,
// by writing credHelpers in dir's config.json. Callers resolve dir via
// ConfigDir unless they have a reason to use a different directory.
//
// Every domain is checked before anything is written, and a domain already
// claimed by a different helper fails the whole call with a *ConflictError.
// Docker resolves credHelpers ahead of every other credential source and
// credHelpers holds one helper per domain, so overwriting an entry silently
// takes a registry away from whichever tool owns it — ecr-login, gcloud, a
// per-registry osxkeychain — and Save discards the old value irrecoverably.
func Register(dir string, domains ...string) ([]Registration, error) {
	cfg, err := dockerconfig.Load(dir)
	if err != nil {
		return nil, fmt.Errorf("reading current docker config: %w", err)
	}

	conflicts := make(map[string]string)
	for _, domain := range domains {
		if existing, ok := cfg.CredentialHelpers[domain]; ok && existing != ShortName {
			conflicts[domain] = existing
		}
	}
	if len(conflicts) > 0 {
		return nil, &ConflictError{Helpers: conflicts, ConfigPath: filepath.Join(dir, "config.json")}
	}

	// WARNING: This must be added to avoid accessing an uninitialized
	// map. This happens when someone hasn't used a cred helper already
	// and isn't handled by the Docker configuration module.
	// See https://gitlab.com/gitlab-org/cli/-/issues/7921
	if cfg.CredentialHelpers == nil {
		cfg.CredentialHelpers = make(map[string]string)
	}

	registrations := make([]Registration, 0, len(domains))
	for _, domain := range domains {
		// Check whether AuthConfigs holds the key, not what it holds: under a
		// configured credsStore, `docker login` leaves an empty marker entry
		// here and stores the actual secret in the OS keychain, while
		// `docker logout` removes the key outright.
		_, shadowed := cfg.AuthConfigs[domain]

		cfg.CredentialHelpers[domain] = ShortName
		registrations = append(registrations, Registration{Domain: domain, ShadowedLogin: shadowed})
	}

	if err := cfg.Save(); err != nil {
		return nil, fmt.Errorf("registering %s as a docker credential helper: %w", ShortName, err)
	}

	return registrations, nil
}

// ConfigDir resolves the directory Docker reads config.json from, mirroring
// dockerconfig.Dir(): $DOCKER_CONFIG when set, otherwise ~/.docker. Honoring
// $DOCKER_CONFIG matters because writing to the wrong directory fails
// silently: the command reports success, but Docker reads a config.json with
// no credHelpers entry for the registry.
//
// This is not dockerconfig.Dir() itself, because that function memoizes its
// result in a process-wide sync.Once the first time it's called: the first
// caller in the process would lock in a directory that later callers (for
// example, tests overriding $HOME) can't change.
func ConfigDir() (string, error) {
	if dir := os.Getenv("DOCKER_CONFIG"); dir != "" {
		return dir, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}

	return filepath.Join(home, ".docker"), nil
}
