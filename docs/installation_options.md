---
stage: Create
group: Code Review
info: To determine the technical writer assigned to the Stage/Group associated with this page, see <https://handbook.gitlab.com/handbook/product/ux/technical-writing/#assignments>
---

# Other options to install the GitLab CLI

These installation instructions are either not officially supported by GitLab, or
are maintained by the community.

## Table of contents

- [macOS](#macos)
- [Windows](#windows)
- [Linux](#linux)
  - [Homebrew](#homebrew)
  - [mise-en-place](#mise-en-place)
  - [ASDF](#asdf)
  - [Snapcraft](#snapcraft)
  - [Arch Linux](#arch-linux)
  - [Alpine Linux](#alpine-linux)
    - [Install a pinned version from edge](#install-a-pinned-version-from-edge)
    - [Alpine Linux Docker-way](#alpine-linux-docker-way)
  - [Fedora](#fedora)
  - [Nix/NixOS](#nixnixos)
  - [WakeMeOps (Debian/Ubuntu)](#wakemeops-debianubuntu)
  - [Spack](#spack)
- [Docker](#docker)
  - [GitLab CICD](#gitlab-cicd)

## macOS

- Homebrew (officially supported)
  - Install with: `brew install glab`
  - Update with: `brew upgrade glab`
- [MacPorts](https://ports.macports.org/port/glab/details/):
  - Install with: `sudo port install glab`
  - Update with: `sudo port selfupdate && sudo port upgrade glab`
- [mise-en-place](https://mise.jdx.dev/)
  - Add to the `[tools]` section of one of mise's configuration files: `"ubi:gitlab-org/cli" = { version = "latest", exe = "glab", provider = "gitlab" }`.
  - Install with `mise install`
- [ASDF tool version manager](https://asdf-vm.com/guide/introduction.html):
  - Install with: `asdf plugin add glab; asdf install glab latest; asdf global glab latest`

## Windows

- Homebrew (through [Windows Subsystem for Linux](https://learn.microsoft.com/en-us/windows/wsl/install)) (officially supported)
  - Install with: `brew install glab`
  - Update with: `brew upgrade glab`
- [WinGet](https://github.com/microsoft/winget-cli)
  - Install with: `winget install glab.glab`
  - Update with: `winget install glab.glab`
- [Chocolatey](https://chocolatey.org)
  - Install with: `choco install glab`
  - Update with: `choco upgrade glab`
- [scoop](https://scoop.sh)
  - Install with: `scoop install glab`
  - Update with: `scoop update glab`
- [mise-en-place](https://mise.jdx.dev/)
  - Add `"ubi:gitlab-org/cli" = { version = "latest", exe = "glab", provider = "gitlab" }` to one of mise's configuration files.
  - Install with `mise install`
- [ASDF tool version manager](https://asdf-vm.com/guide/introduction.html):
  - Requires [Windows Subsystem for Linux (WSL)](https://learn.microsoft.com/en-us/windows/wsl/install).
  - Install with: `asdf plugin add glab; asdf install glab latest; asdf global glab latest`
- Download an EXE installer or the `glab.exe` binary from the [releases page](https://gitlab.com/gitlab-org/cli/-/releases)

## Linux

- Download prebuilt binaries from the [releases page](https://gitlab.com/gitlab-org/cli/-/releases)

### Homebrew

Installing from Homebrew is the officially supported installation method for Linux.

- Install with: `brew install glab`
- Update with: `brew upgrade glab`

### mise-en-place

Add to the `[tools]` section of one of mise's configuration files:

```toml
"ubi:gitlab-org/cli" = { version = "latest", exe = "glab", provider = "gitlab" }
```

Then run `mise install` to install it.

### ASDF

To install with the [ASDF tool version manager](https://asdf-vm.com/guide/introduction.html), run these commands:

```shell
asdf plugin add glab; asdf install glab latest; asdf global glab latest
```

### Snapcraft

To install `glab` from the [Snap Store](https://snapcraft.io/glab):

1. Make sure you have [snap installed](https://snapcraft.io/docs/installing-snapd) on your Linux distribution.
1. For the latest stable release: `sudo snap install glab`
1. For the latest edge release (updated on every commit to `main`): `sudo snap install --edge glab`
1. Grant `glab` access to SSH keys: `sudo snap connect glab:ssh-keys`
1. Grant `glab` access to the OS keyring for encrypted credential storage: `sudo snap connect glab:password-manager-service`. Without this connection, `glab auth login` falls back to plaintext storage. See [credential storage](source/authentication.md#credential-storage).

[![Download from the Snap Store](https://snapcraft.io/static/images/badges/en/snap-store-black.svg)](https://snapcraft.io/glab)

### Arch Linux

For Arch Linux, `glab` is available:

- From the [`extra/glab`](https://archlinux.org/packages/extra/x86_64/glab/) package.
- By downloading and installing an archive from the
  [releases page](https://gitlab.com/gitlab-org/cli/-/releases).
- From the [Snap Store](https://snapcraft.io/glab), if
  [snap](https://snapcraft.io/docs/installing-snap-on-arch-linux) is installed.
- Installing with the package manager: `pacman -S glab`

### Alpine Linux

`glab` is available on the [Alpine Community Repository](https://git.alpinelinux.org/aports/tree/community/glab?h=master) as `glab`.

When installing, use `--no-cache` so no `apk update` is required:

```shell
apk add --no-cache glab
```

#### Install a pinned version from edge

To ensure that by default edge is used to get the latest updates. We need the edge repository in `/etc/apk/repositories`.

Afterwards you can install it with `apk add --no-cache glab@edge`

We use `--no-cache` so an `apk update` is not required.

```shell
echo "@edge http://dl-cdn.alpinelinux.org/alpine/edge/community" >> /etc/apk/repositories
apk add --no-cache glab@edge
```

#### Alpine Linux Docker-way

Use edge directly

```shell
FROM alpine:3.13
RUN apk add --no-cache glab
```

Fetching latest glab version from edge

```shell
FROM alpine:3.13
RUN echo "@edge http://dl-cdn.alpinelinux.org/alpine/edge/community" >> /etc/apk/repositories
RUN apk add --no-cache glab@edge
```

### Fedora

Fedora users can find `glab` as `glab` in the official repositories.

Install it with the command `dnf install glab`.

### Nix/NixOS

Nix (NixOS) users can install from [nixpkgs](https://search.nixos.org/packages?channel=unstable&show=glab&from=0&size=30&sort=relevance&query=glab) with the command `nix-env -iA nixos.glab`.

### WakeMeOps (Debian/Ubuntu)

`glab` also exists in the [WakeMeOps repository](https://docs.wakemeops.com/packages/glab/):

```shell
# Add WakeMeOps repository
curl -sSL "https://raw.githubusercontent.com/upciti/wakemeops/main/assets/install_repository" | sudo bash

# Install glab
sudo apt install glab
```

### Spack

- To install: `spack install glab`.
- To update: `spack uninstall glab && spack install glab`

## Docker

A Docker image for `glab` is available at
[`gitlab/glab`](https://hub.docker.com/r/gitlab/glab/):

```shell
docker pull gitlab/glab
```

### GitLab CI/CD

To use `glab` in a CI/CD pipeline, you must set the `entrypoint` of the image to
the `glab` executable itself. For more information, see the GitLab documentation
for [Override the entrypoint of an image](https://docs.gitlab.com/ci/docker/using_docker_images/#override-the-entrypoint-of-an-image). An example `.gitlab-ci.yml`:

Example `.gitlab-ci.yml`:

```yaml
example:
  stage: test
  image:
    name: "gitlab/glab"
    entrypoint: [""]
  script:
    - glab --version
```

## Shell completions

If your shell is not supported by [`glab completion`](./source/completion/_index.md), you can
use [Carapace-bin](https://github.com/carapace-sh/carapace-bin) to get completions for `glab`
and other CLI tools. To set up completions for your shell, follow the [Carapace-bin installation instructions](https://carapace-sh.github.io/carapace-bin/install.html).
