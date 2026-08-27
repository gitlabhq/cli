# GLab

![GLab](docs/source/img/glab-logo.png)

GLab is an open source GitLab CLI tool. It brings GitLab to your terminal, next to where you are already working with `git` and your code, without switching between windows and browser tabs. While it's powerful for issues and merge requests, `glab` does even more:

- View, manage, and retry CI/CD pipelines directly from your CLI.
- Create changelogs.
- Create and manage releases.
- Ask GitLab Duo Chat (Classic) questions about Git.
- Manage GitLab agents for Kubernetes.

`glab` is available for repositories hosted on GitLab.com, GitLab Dedicated, and GitLab Self-Managed. It supports multiple authenticated GitLab instances, and automatically detects the authenticated hostname from the remotes available in your working Git directory.

![command example](docs/source/img/getting-started.gif)

## Table of contents

- [Requirements](#requirements)
- [Usage](#usage)
  - [Core commands](#core-commands)
  - [GitLab Duo for the CLI](#gitlab-duo-for-the-cli)
- [Demo](#demo)
- [Documentation](#documentation)
- [Installation](#installation)
  - [Homebrew](#homebrew)
  - [Other installation methods](#other-installation-methods)
  - [Building from source](#building-from-source)
    - [Prerequisites for building from source](#prerequisites-for-building-from-source)
- [Authentication](#authentication)
- [Configuration](#configuration)
  - [Configure `glab` to use your GitLab Self-Managed or GitLab Dedicated instance](#configure-glab-to-use-your-gitlab-self-managed-or-gitlab-dedicated-instance)
  - [Configure `glab` to use mTLS certificates](#configure-glab-to-use-mtls-certificates)
  - [Configure `glab` to use self-signed certificates](#configure-glab-to-use-self-signed-certificates)
  - [Configure `glab` to use custom HTTP headers](#configure-glab-to-use-custom-http-headers)
- [Environment variables](#environment-variables)
  - [GitLab access variables](#gitlab-access-variables)
  - [`glab` configuration variables](#glab-configuration-variables)
  - [Other variables](#other-variables)
  - [Token and environment variable precedence](#token-and-environment-variable-precedence)
  - [Debugging](#debugging)
- [Troubleshooting](#troubleshooting)
- [Issues](#issues)
- [Security](#security)
- [Contributing](#contributing)
  - [Versioning](#versioning)
  - [Classify version changes](#classify-version-changes)
  - [Compatibility](#compatibility)
- [Inspiration](#inspiration)

## Requirements

`glab` officially supports GitLab versions 16.0 and later. Certain commands might require
more recent versions. While many commands might work properly in GitLab versions
15.x and earlier, no support is provided for these versions.

## Usage

To get started with `glab`:

1. Follow the [installation instructions](#installation) appropriate for your operating system.
1. [Authenticate](#authentication) into your instance of GitLab.
1. Optional. Configure `glab` further to meet your needs:
   - 1Password users can configure it to [authenticate to `glab`](https://developer.1password.com/docs/cli/shell-plugins/gitlab/).
   - Set any needed global, per-project, or per-host [configuration](#configuration).
   - Set any needed [environment variables](#environment-variables).

You're ready!

### Core commands

Run `glab --help` to view a list of core commands in your terminal.

- [`glab alias`](docs/source/alias): Create, list, and delete aliases.
- [`glab api`](docs/source/api): Make authenticated requests to the GitLab API.
- [`glab auth`](docs/source/auth): Manage the authentication state of the CLI.
- [`glab changelog`](docs/source/changelog): Interact with the changelog API.
- [`glab check-update`](docs/source/check-update): Check for updates to the CLI.
- [`glab ci`](docs/source/ci): Work with GitLab CI/CD pipelines and jobs.
- [`glab cluster`](docs/source/cluster): Manage GitLab agents for Kubernetes and their clusters.
- [`glab completion`](docs/source/completion): Generate shell completion scripts.
- [`glab config`](docs/source/config): Set and get CLI settings.
- [`glab container-registry`](docs/source/container-registry): Work with GitLab container registries.
- [`glab deploy-key`](docs/source/deploy-key): Manage deploy keys.
- [`glab duo`](docs/source/duo): Work with GitLab Duo.
- [`glab gpg-key`](docs/source/gpg-key): Manage GPG keys registered with your GitLab account.
- [`glab incident`](docs/source/incident): Work with GitLab incidents.
- [`glab issue`](docs/source/issue): Work with GitLab issues.
- [`glab iteration`](docs/source/iteration): Retrieve iteration information.
- [`glab job`](docs/source/job): Work with GitLab CI/CD jobs.
- [`glab label`](docs/source/label): Manage labels for your project.
- [`glab mcp`](docs/source/mcp): Work with a Model Context Protocol (MCP) server. (EXPERIMENTAL)
- [`glab milestone`](docs/source/milestone): Manage group or project milestones.
- [`glab mr`](docs/source/mr): Create, view, and manage merge requests.
- [`glab opentofu`](docs/source/opentofu): Work with the OpenTofu or Terraform integration.
- [`glab release`](docs/source/release): Manage GitLab releases.
- [`glab repo`](docs/source/repo): Work with GitLab repositories and projects.
- [`glab schedule`](docs/source/schedule): Work with GitLab CI/CD schedules.
- [`glab securefile`](docs/source/securefile): Manage secure files for a project.
- [`glab snippet`](docs/source/snippet): Create, view and manage snippets.
- [`glab ssh-key`](docs/source/ssh-key): Manage SSH keys registered with your GitLab account.
- [`glab stack`](docs/source/stack): Create, manage, and work with stacked diffs.
- [`glab token`](docs/source/token): Manage personal, project, or group tokens.
- [`glab user`](docs/source/user): Interact with a GitLab user account.
- [`glab variable`](docs/source/variable): Manage variables for a GitLab project or group.
- [`glab version`](docs/source/version): Show version information for the CLI.

Commands follow this pattern:

```shell
glab <command> <subcommand> [flags]
```

Many core commands also have sub-commands. Some examples:

- List merge requests assigned to you: `glab mr list --assignee=@me`
- List review requests for you: `glab mr list --reviewer=@me`
- Approve a merge request: `glab mr approve 235`
- Create an issue, and add milestone, title, and label: `glab issue create -m release-2.0.0 -t "My title here" --label important`

### GitLab Duo for the CLI

The GitLab CLI also includes support for using the GitLab Duo Agent Platform directly in your terminal.

Use `glab duo cli` to ask GitLab Duo questions about your codebase and to autonomously perform actions on your behalf.

For more information, see [GitLab Duo CLI](https://docs.gitlab.com/user/gitlab_duo_cli/).

## Demo

[![asciicast](https://asciinema.org/a/368622.svg)](https://asciinema.org/a/368622)

## Documentation

Read the [documentation](docs/source/_index.md) for usage instructions or check out `glab help`.

## Installation

Download a binary suitable for your OS at the [releases page](https://gitlab.com/gitlab-org/cli/-/releases).
Other installation methods depend on your operating system.

### Homebrew

Homebrew is the officially supported package manager for macOS, Linux, and Windows (through [Windows Subsystem for Linux](https://learn.microsoft.com/en-us/windows/wsl/install))

- Homebrew
  - Install with: `brew install glab`
  - Update with: `brew upgrade glab`

### Other installation methods

Other options to install the GitLab CLI that may not be officially supported or are maintained by the community are [also available](docs/installation_options.md).

### Building from source

If a supported binary for your OS is not found at the [releases page](https://gitlab.com/gitlab-org/cli/-/releases), you can build from source:

#### Prerequisites for building from source

- `make`
- Go version as defined by [`main/go.mod`](https://gitlab.com/gitlab-org/cli/-/blob/main/go.mod?ref_type=heads#L3)

To build from source:

1. Run `go version` to verify that you have the minimum required Go version.
   If Go is not installed, see [Download and install](https://go.dev/doc/install).
1. Clone the repository: `git clone https://gitlab.com/gitlab-org/cli.git`
1. Build the binary: `make build`
1. Install `glab` in `$GOPATH/bin`: `make install`
1. Optional. If `$GOPATH/bin` or `$GOBIN` is not in your `$PATH`,
   run `export PATH=$PWD/bin:$PATH`.
1. Confirm the installation: `glab version`

## Authentication

To authenticate `glab` with OAuth, a personal access token, or a CI job token, see
[Authenticate with GitLab](https://docs.gitlab.com/cli/authentication/).

## Configuration

By default, `glab` follows the
[XDG Base Directory Spec](https://specifications.freedesktop.org/basedir-spec/basedir-spec-latest.html),
which means it searches for configuration files in multiple locations with proper precedence.

### Configuration Levels

Configure `glab` at different levels: system-wide, globally (per-user), locally (per-repository), or per host:

- **System-wide** (for all users): Place configuration at `/etc/xdg/glab-cli/config.yml` (or `$XDG_CONFIG_DIRS/glab-cli/config.yml`).
  - Useful for Linux distributions and system administrators to provide default configurations.
  - User configurations will override system-wide settings.
- **Globally** (per-user): run `glab config set --global editor vim`.
  - The global configuration file is available at `~/.config/glab-cli/config.yml` (or `$XDG_CONFIG_HOME/glab-cli/config.yml`).
  - To override this location, set the `GLAB_CONFIG_DIR` environment variable.
- **The current repository**: run `glab config set editor vim` in any folder in a Git repository.
  - The local configuration file is available at `.git/glab-cli/config.yml` in the current working Git directory.
- **Per host**: run `glab config set editor vim --host gitlab.example.org`, changing
  the `--host` parameter to meet your needs.
  - Per-host configuration info is always stored in the global configuration file, with or without the `global` flag.

### Configuration Search Order

When `glab` looks for configuration files, it searches in this order (highest priority first):

1. `$GLAB_CONFIG_DIR/config.yml` (if `GLAB_CONFIG_DIR` is set)
2. `~/.config/glab-cli/config.yml` (legacy location, for backward compatibility)
3. `$XDG_CONFIG_HOME/glab-cli/config.yml` (platform-specific XDG location)
4. `$XDG_CONFIG_DIRS/glab-cli/config.yml` (system-wide configs, default: `/etc/xdg/glab-cli/config.yml`)

The first configuration file found is used.

#### Configuration File Locations

**For backward compatibility**, `glab` checks `~/.config/glab-cli/config.yml` first on all platforms.
If no legacy config exists, `glab` uses platform-specific XDG Base Directory locations:

- **Linux**: `~/.config/glab-cli/config.yml` (XDG_CONFIG_HOME)
- **macOS**: `~/Library/Application Support/glab-cli/config.yml` (XDG_CONFIG_HOME)
- **Windows**: `%LOCALAPPDATA%\glab-cli\config.yml` (XDG_CONFIG_HOME)

**Note**: If you have config files in both the legacy location (`~/.config/glab-cli/config.yml`)
and the platform-specific XDG location, `glab` will use the legacy location and display a warning.
Consider consolidating to one location to avoid confusion.

### Configure `glab` to use your GitLab Self-Managed or GitLab Dedicated instance

When outside a Git repository, `glab` uses `gitlab.com` by default. For `glab` to default
to your GitLab Self-Managed or GitLab Dedicated instance when you are not in a Git repository, change the host
configuration settings. Use this command, changing `gitlab.example.com` to the domain name
of your instance:

```shell
glab config set -g host gitlab.example.com
```

Setting this configuration enables you to perform commands outside a Git repository while
using your GitLab Self-Managed or GitLab Dedicated instance. For example:

- `glab repo clone group/project`
- `glab issue list -R group/project`

If you don't set a default domain name, you can declare one for the current command with
the `GITLAB_HOST` environment variable, like this:

- `GITLAB_HOST=gitlab.example.com glab repo clone group/project`
- `GITLAB_HOST=gitlab.example.com glab issue list -R group/project`

When inside a Git repository `glab` will use that repository's GitLab host by default. For example `glab issue list`
will list all issues of the current directory's Git repository.

### Configure `glab` to use mTLS certificates

To use a mutual TLS (Mutual Transport Layer Security) certificate with `glab`, edit your global
configuration file (`~/.config/glab-cli/config.yml`) to provide connection information:

```yaml
hosts:
    git.your-domain.com:
        api_protocol: https
        api_host: git.your-domain.com
        token: xxxxxxxxxxxxxxxxxxxxxxxxx
        client_cert: /path/to/client.crt
        client_key: /path/to/client.key
        ca_cert: /path/to/ca-chain.pem
```

- `ca_cert` is optional for mTLS support if you use a publicly signed server certificate.
- `token` is not required if you use a different authentication method.

### Configure `glab` to use self-signed certificates

To configure the GitLab CLI to support GitLab Self-Managed and GitLab Dedicated instances with
self-signed certificates, either:

- Disable TLS verification with:

  ```shell
  glab config set skip_tls_verify true --host gitlab.example.com
  ```

- Add the path to the self signed CA:

  ```shell
  glab config set ca_cert /path/to/server.pem --host gitlab.example.com
  ```

### Configure `glab` to use custom HTTP headers

To access a GitLab instance through an authenticating proxy, add custom headers
to the host entry in your global configuration file. Set each header value
directly with `value`, from an environment variable with `valueFromEnv`, or from
the output of a command with `valueFromCommand`. For example:

```yaml
hosts:
  gitlab.example.com:
    custom_headers:
      - name: X-Proxy-Client-ID
        value: client-id
      - name: X-Proxy-Client-Secret
        valueFromEnv: PROXY_CLIENT_SECRET
      - name: Proxy-Authorization
        valueFromCommand: proxy-token-helper
```

The command must print the complete header value, on a single line, to standard
output. `glab`:

- Removes leading and trailing whitespace.
- Fails if the command prints nothing, prints multiple lines or a NUL byte, or
  does not finish in 30 seconds.
- Runs each configured command once per process, then reuses the result for all
  requests, including OAuth token refresh.

The command is split into an executable and arguments, and does not run in a
shell. If you need shell expansion or pipelines, use an explicit shell, such as
`sh -c`. Use `valueFromEnv` instead if another process manages token refresh, or
if command startup time is a concern.

#### Google Identity-Aware Proxy example

For a GitLab instance protected by Google Identity-Aware Proxy (IAP), replace
`SERVICE_ACCOUNT` with the service account email and `IAP_CLIENT_ID` with the
IAP OAuth client ID. Then reference the variable from the host configuration.

Export a complete `Proxy-Authorization` value:

```shell
export GLAB_IAP_TOKEN="Bearer $(gcloud auth print-identity-token \
  --impersonate-service-account=SERVICE_ACCOUNT \
  --audiences=IAP_CLIENT_ID \
  --include-email)"
```

Then reference it from the host configuration:

```yaml
hosts:
  gitlab.example.com:
    custom_headers:
      - name: Proxy-Authorization
        valueFromEnv: GLAB_IAP_TOKEN
```

Alternatively, generate a token whenever a `glab` process starts:

```yaml
hosts:
  gitlab.example.com:
    custom_headers:
      - name: Proxy-Authorization
        valueFromCommand: >-
          sh -c 'printf "Bearer %s" "$(gcloud auth print-identity-token
          --impersonate-service-account=SERVICE_ACCOUNT
          --audiences=IAP_CLIENT_ID --include-email)"'
```

The active Google Cloud credentials must be allowed to impersonate the service
account, and the service account must be allowed to access the IAP-protected
application.

## Environment variables

### GitLab access variables

| Token name             | In `config.yml`                                                                                       | Default value if [not set](#configuration) | Description |
|------------------------|-------------------------------------------------------------------------------------------------------|--------------------------------------------|-------------|
| `GITLAB_ACCESS_TOKEN`  | `hosts.<hostname>.token`                                                                              | -                                          | An authentication token for API requests. Functionally equivalent to `GITLAB_TOKEN`. Checked after `GITLAB_TOKEN` and overrides any previously stored credentials. |
| `GITLAB_API_HOST`      | `hosts.<hostname>.api_host`, or `hosts.<hostname>` if empty                                           | Hostname found in the Git URL              | Specify the host where the API endpoint is found. Useful when there are separate (sub)domains or hosts for Git and the API endpoint. |
| `GITLAB_CLIENT_ID`     | `hosts.<hostname>.client_id`                                                                          | Client-ID for GitLab.com.                  | A custom Client-ID generated by the GitLab OAuth 2.0 application. |
| `GITLAB_GROUP`         | -                                                                                                     | -                                          | Default GitLab group used for listing merge requests, issues and variables. Only used if no `--group` option is given. |
| `GITLAB_HOST`          | `host` (this is the default host `glab` will use when the current directory is not a `git` directory) | `https://gitlab.com`                       | Alias of `GITLAB_URI`. |
| `GITLAB_REPO`          | -                                                                                                     | -                                          | Default GitLab repository used for commands accepting the `--repo` option. Only used if no `--repo` option is given. |
| `GITLAB_TOKEN`         | `hosts.<hostname>.token`                                                                              | -                                          | An authentication token for API requests. Setting this avoids being prompted to authenticate and overrides any previously stored credentials. Can be set in the config with `glab config set token xxxxxx`. |
| `GITLAB_URI`           | not applicable                                                                                        | not applicable                             | Alias of `GITLAB_HOST`. |
| `OAUTH_TOKEN`          | `hosts.<hostname>.token`                                                                              | -                                          | An OAuth 2.0 authentication token for API requests. Checked after `GITLAB_TOKEN` and `GITLAB_ACCESS_TOKEN`, and overrides any previously stored credentials. On a host configured with `is_oauth2: true`, `glab` continues to use the OAuth 2.0 refresh flow. |

### `glab` configuration variables

| Token name                                   | In `config.yml`      | Default value if [not set](#configuration) | Description |
|----------------------------------------------|----------------------|--------------------------------------------|-------------|
| `BROWSER`                                    | `browser`            | system default                             | The web browser to use for opening links. Can be set in the configuration with `glab config set browser mybrowser`. |
| `GITLAB_RELEASE_ASSETS_USE_PACKAGE_REGISTRY` | -                    | -                                          | When `true` or `1`, the `glab release create` command uploads release assets to the generic package registry of the project. Can be overridden with the `--use-package-registry` flag. |
| `GLAB_CHECK_UPDATE`                          | -                    | -                                          | Set to `true` to force an update check. |
| `GLAB_CONFIG_DIR`                            | -                    | `~/.config/glab-cli/`                      | Directory where the `glab` global configuration file is located. Can be set in the config with `glab config set remote_alias origin`. |
| `GLAB_DEBUG`                                 | `debug`              | `false`                                    | Set to `true` to output more information for each command, like Git commands, expanded aliases, and DNS error details. |
| `GLAB_DEBUG_HTTP`                            | -                    | `false`                                    | Set to true to output HTTP transport information (request / response). |
| `GLAB_FORCE_HYPERLINKS`                      | `display_hyperlinks` | `auto`                                     | Set to `true` (or any truthy value) to force hyperlinks even when not outputting to a TTY. A falsy value falls through to `display_hyperlinks`. |
| `GLAB_GLAMOUR_STYLE`                         | `glamour_style`      | `dark`                                     | Environment variable to set your desired Markdown renderer style. Available options are (`dark`, `light`, `notty`) or set a [custom style](https://github.com/charmbracelet/glamour#styles). |
| `GLAB_NO_PROMPT`                             | `no_prompt`          | `false`                                    | Set to `true` to disable prompts. |
| `GLAB_SEND_TELEMETRY`                        | `telemetry`          | `true`                                     | Set to `false` to prevent command usage data from being sent to your GitLab instance. |
| `NO_COLOR`                                   | -                    | `true`                                     | Set to any value to avoid printing ANSI escape sequences for color output. |
| `VISUAL`, `EDITOR`                           | `editor`             | `nano`                                     | (in order of precedence) The editor tool to use for authoring text. Can be set in the config with `glab config set editor vim`. |

### Other variables

| Token name           | In `config.yml` | Default value if [not set](#configuration) | Description |
|----------------------|-----------------|--------------------------------------------|-------------|
| `GIT_REMOTE_URL_VAR` | not applicable  | not applicable                             | Alias of `REMOTE_ALIAS`. |
| `REMOTE_ALIAS`       | `remote_alias`  | -                                          | `git remote` variable or alias that contains the GitLab URL. Alias: `GIT_REMOTE_URL_VAR` |

#### Variable deprecation

In `glab` version 2.0.0 and later, all `glab` environment variables are prefixed with `GLAB_`.
For more information about this deprecation, see [issue 7999](https://gitlab.com/gitlab-org/cli/-/issues/7999).

### Token and environment variable precedence

For every authenticated command, `glab` resolves the token to use in this order:

1. If any of these environment variables are defined, including in a CI/CD environment,
   `glab` uses the first available, in this order:
   - `GITLAB_TOKEN`
   - `GITLAB_ACCESS_TOKEN`
   - `OAUTH_TOKEN`
1. The credentials stored for the host, in the [configuration file](#configuration)
   (`$HOME/.config/glab-cli/config.yml`) or in your operating system's keyring.
    `glab auth login` and `glab config set token` write them there.
1. If CI/CD auto-login is enabled (`GLAB_ENABLE_CI_AUTOLOGIN=true` and `GITLAB_CI=true`),
   then `glab` uses a CI/CD job token (`CI_JOB_TOKEN`). If you need to use a job token,
   make sure the other variables are not set in the CI/CD job.

If no token is found, `glab` sends the request unauthenticated.

> [!note]
> Do not set `GITLAB_TOKEN=$CI_JOB_TOKEN` in a CI/CD job. `glab` makes API calls with
> `GITLAB_TOKEN` as an access token in the `PRIVATE-TOKEN` header, which does not accept CI/CD job tokens.
> Requests fail with 401 Unauthorized or 404 Not Found on endpoints that support job tokens.

### Debugging

When the `GLAB_DEBUG` environment variable is set to `true`, `glab` outputs more logging information, including:

- Underlying Git commands.
- Expanded aliases.
- DNS error details.

## Troubleshooting

For help with common authentication issues, see
[Troubleshooting](https://docs.gitlab.com/cli/authentication/#troubleshooting) in the GitLab CLI documentation.

## Issues

If you have an issue: report it on the [issue tracker](https://gitlab.com/gitlab-org/cli/-/issues)

## Security

To report a security vulnerability in `glab`, follow the process in
the [security policy](SECURITY.md). Please do not open a public issue.

To learn how the project handles dependency updates and container-scanner CVE
reports, see [dependency management](CONTRIBUTING.md#dependency-management)
and [reporting a vulnerability](CONTRIBUTING.md#reporting-a-vulnerability)
in `CONTRIBUTING.md`.

## Contributing

Feel like contributing? That's awesome! We have a [contributing guide](https://gitlab.com/gitlab-org/cli/-/blob/main/CONTRIBUTING.md) and [Code of conduct](https://gitlab.com/gitlab-org/cli/-/blob/main/CODE_OF_CONDUCT.md) to help guide you.

When updating command help text or documentation, follow the
[GitLab CLI documentation style guide](https://docs.gitlab.com/development/documentation/cli_styleguide/).

### Versioning

This project follows the [SemVer](https://github.com/semver/semver) specification.

### Classify version changes

- If deleting a command, changing how it behaves, or adding a new **required** flag, the release must use a new `MAJOR` revision.
- If adding a new command or **optional** flag, the release must use a new `MINOR` revision.
- If fixing a bug, the release must use a new `PATCH` revision.

### Compatibility

We do our best to introduce breaking changes only when releasing a new `MAJOR` version.
Unfortunately, there are situations where this is not possible, and we may introduce
a breaking change in a `MINOR` or `PATCH` version. Some of situations where we may do so:

- If a security issue is discovered, and the solution requires a breaking change,
  we may introduce such a change to resolve the issue and protect our users.
- If a feature was not working as intended, and the bug fix requires a breaking change,
  the bug fix may be introduced to ensure the functionality works as intended.
- When feature behavior is overwhelmingly confusing due to a vague specification
  on how it should work. In such cases, we may refine the specification
  to remove the ambiguity, and introduce a breaking change that aligns with the
  refined specification. For an example of this, see
  [merge request 1382](https://gitlab.com/gitlab-org/cli/-/merge_requests/1382#note_1686888887).
- Experimental features are not guaranteed to be stable, and can be modified or
  removed without a breaking change.

Breaking changes are a last resort, and we try our best to only introduce them when absolutely necessary.

## Inspiration

The GitLab CLI was adopted from [Clement Sam](https://gitlab.com/profclems) in 2022 to serve as the official CLI of GitLab. Over the years the project has been inspired by both the [GitHub CLI](https://github.com/cli/cli) and [Zaq? Wiedmann's](https://gitlab.com/zaquestion) [lab](https://github.com/zaquestion/lab).

Lab has served as the foundation for many of the GitLab CI/CD commands including `ci view` and `ci trace`.
