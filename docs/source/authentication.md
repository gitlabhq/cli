---
title: Authenticate with GitLab
stage: AI Coding
group: Code Review
info: To determine the technical writer assigned to the Stage/Group associated with this page, see <https://handbook.gitlab.com/handbook/product/ux/technical-writing/#assignments>
---

{{< details >}}

- Tier: Free, Premium, Ultimate
- Offering: GitLab.com, GitLab Self-Managed, GitLab Dedicated

{{< /details >}}

The GitLab CLI supports several authentication methods: OAuth, personal access tokens, and
CI job tokens. To get started, run `glab auth login` and follow the interactive setup.

When running `glab auth login` interactively inside a Git repository, `glab` automatically
detects GitLab instances from your Git remotes and presents them as options. This saves you
from having to manually type the hostname.

## OAuth (GitLab.com)

To authenticate your installation of `glab` with an OAuth application connected to GitLab.com:

1. Start interactive setup with `glab auth login`.
1. For the GitLab instance you want to sign in to, select **GitLab.com**.
1. For the login method, select **Web** to authenticate through your local browser, or
   **Device** for headless environments (such as SSH sessions or containers) where no
   local browser is available. The device flow displays a one-time code and a
   verification URL that you open on any other device with a browser.
1. Authorize the application when prompted.
1. Complete the authentication process in your terminal, selecting the appropriate options for your needs.

## OAuth (GitLab Self-Managed, GitLab Dedicated)

Prerequisites:

- You've created an OAuth application at the user, group, or instance level, and you
  have its application ID. For instructions, see how to configure GitLab
  [as an OAuth 2.0 authentication identity provider](https://docs.gitlab.com/integration/oauth_provider/)
  in the GitLab documentation.
- Your OAuth application is configured with these parameters:
  - **Redirect URI** is `http://localhost:7171/auth/redirect`. Not required if you only intend to use `--device`.
  - **Confidential** is not selected. If it's selected, `glab auth login` fails with an
    `invalid_client` error. For more information, see the
    [troubleshooting section](#error-invalid_client-during-oauth-login).
  - **Scopes** are `openid`, `profile`, `read_user`, `write_repository`, and `api`.
- To use the OAuth 2.0 device authorization flow (`glab auth login --device`), the application must
  also have `device_code` in its allowed grant types, and the GitLab instance must be running
  GitLab 17.9 or later. For more information, see the
  [device authorization grant flow](https://docs.gitlab.com/api/oauth2/#device-authorization-grant-flow)
  documentation.

To authenticate your installation of `glab` with an OAuth application connected
to your GitLab Self-Managed or GitLab Dedicated instance:

1. Store the application ID with `glab config set client_id <CLIENT_ID> --host <HOSTNAME>`.
   For `<CLIENT_ID>`, provide your application ID.
1. Start interactive setup with `glab auth login --hostname <HOSTNAME>`.
1. For the login method, select **Web** to authenticate through your local browser, or
   **Device** for headless environments where no local browser is available.
1. Follow the prompts to authorize the application:
   - For **Web**, complete authorization in the browser window that opens.
   - For **Device**, copy the displayed one-time code, open the verification URL on any
     device with a browser, and approve the request.
1. Complete the authentication process in your terminal, selecting the appropriate options for your needs.

## Personal access token

To authenticate your installation of `glab` with a personal access token:

1. Get a GitLab personal access token with at least the `api`
   and `write_repository` scopes. Use the method appropriate for your instance:
   - For GitLab.com, create one at the [personal access tokens](https://gitlab.com/-/user_settings/personal_access_tokens/legacy/new?scopes=api%2Cwrite_repository) page.
   - For GitLab Self-Managed and GitLab Dedicated, visit `https://gitlab.example.com/-/user_settings/personal_access_tokens/legacy/new?scopes=api,write_repository`,
     modifying `gitlab.example.com` to match the domain name of your instance.
     On GitLab 18.8 and earlier that page does not exist; use
     `https://gitlab.example.com/-/user_settings/personal_access_tokens?scopes=api,write_repository` instead.
1. Start interactive setup: `glab auth login`
1. Authenticate with the method appropriate for your GitLab instance:
   - For GitLab SaaS, authenticate against `gitlab.com` by reading the token
     from a file: `glab auth login --stdin < myaccesstoken.txt`
   - For GitLab Self-Managed and GitLab Dedicated, authenticate by reading from a file:
     `glab auth login --hostname gitlab.example.com --stdin < myaccesstoken.txt`. This will allow you to perform
     authenticated `glab` commands against your instance when you are in a Git repository with a remote
     matching your instance's host. Alternatively, set `GITLAB_HOST` to direct your command to your instance.
   - Authenticate with token and hostname: `glab auth login --hostname gitlab.example.org --token xxxxx`
     Not recommended for shared environments.
   - Credentials are stored in the global
     [configuration file](configuration.md#configuration-file-locations).

## CI job token

To authenticate your installation of `glab` with a CI job token, the `glab` command must be run in a GitLab CI job.
The token is automatically provided by the GitLab Runner via the `CI_JOB_TOKEN` environment variable.

Endpoints allowing the use of the CI job token are listed in the
[GitLab documentation](https://docs.gitlab.com/ci/jobs/ci_job_token/#job-token-access).

### Auto-login

To enable CI auto-login, set `GLAB_ENABLE_CI_AUTOLOGIN=true`. When enabled, `glab` automatically
detects if it's running in a GitLab CI job and uses the predefined CI/CD variables to sign in.

```shell
GLAB_ENABLE_CI_AUTOLOGIN=true glab release list -R $CI_PROJECT_PATH
```

The following predefined CI/CD variables are used automatically:

| Predefined CI/CD variable  | Equivalent `glab auth login` flag |
|----------------------------|-----------------------------------|
| `CI_SERVER_FQDN`           | `--hostname`                      |
| `CI_JOB_TOKEN`             | `--job-token`                     |
| `CI_SERVER_PROTOCOL`       | `--api-protocol`                  |
| `CI_SERVER_SHELL_SSH_HOST` | `--ssh-hostname`                  |

### Manual login

Use manual login when the command does not support CI job tokens, or you need a personal access token:

```shell
glab auth login --job-token $CI_JOB_TOKEN --hostname $CI_SERVER_FQDN --api-protocol $CI_SERVER_PROTOCOL
GITLAB_HOST=$CI_SERVER_FQDN glab release list -R $CI_PROJECT_PATH
```

## Credential storage

By default, `glab auth login` stores your credentials in the operating system's
keyring:

- macOS: Keychain
- Windows: Credential Manager
- Linux: Secret Service (GNOME Keyring, KWallet, and other implementations)

If no keyring is available, if you pass `--insecure-storage`, or if you use `glab` in CI/CD
environments where `GITLAB_CI` or `CI` is set to `true`, `glab` stores credentials as plaintext in the
[configuration file](configuration.md#configuration-file-locations) and prints a warning.

To move an existing plaintext token into the keyring later, run
`glab auth login --hostname <HOSTNAME>` again. `glab auth status` reports where
each token is stored and nudges you to migrate when it finds one in plaintext.

### Snap installations

The snap package runs under strict confinement, so it cannot reach the Secret
Service until the `password-manager-service` interface is connected.

To connect the interface:

1. On a fresh install, run:

   ```shell
   sudo snap connect glab:password-manager-service
   ```

1. Run (or re-run) `glab auth login` to store your token in the keyring.

Without this connection, `glab` warns and falls back to plaintext file storage.

## Claude Code sandboxing

By default, the Claude Code sandbox allows writes only to the current working directory.
Because `glab` stores credentials elsewhere, it cannot save refreshed OAuth credentials
and commands fail until you run `glab auth login` again.
Configure one of the following options to avoid this.

### Option 1: Run `glab` outside the sandbox

Add `glab` to `excludedCommands` in the `settings.json` file for Claude Code:

```json
{
  "sandbox": {
    "excludedCommands": ["glab"]
  }
}
```

When you use this option, `glab` clones repositories and connects to the hosts you give it without
the sandbox's filesystem and network restrictions.
Be aware that a compromised or prompt-injected agent could write outside the working directory or
send data to an external host.

### Option 2: Grant write access to credential storage

Update the `settings.json` for Claude Code to allow write access to the configuration directory and, on macOS, the login keychain:

```json
{
  "sandbox": {
    "filesystem": {
      "allowWrite": [
        "~/Library/Keychains/login.keychain-db*",
        "<CONFIG_DIR>"
      ]
    }
  }
}
```

Replace `<CONFIG_DIR>` with the output of `glab config path --dir`.

The keychain entry applies to macOS only. On Linux, `glab` reaches the keyring over D-Bus
instead of the filesystem, so omit the keychain entry.

Grant write access to the directory rather than the configuration file, and keep the trailing `*` on
the keychain path because both `glab` and macOS write a temporary file and rename it into place.

When you use this option, `glab` stays inside the sandbox but gains write access to your whole login
keychain and to every command in the sandbox, not just `glab`.
Be aware that keychain items share a single file, so access cannot be narrowed to the credentials
`glab` manages.

## Troubleshooting

When authenticating with `glab`, you might encounter the following issues.

### Error: `invalid_client` during OAuth login

When authenticating with an OAuth application, `glab auth login` might fail with an error:

```plaintext
Oauth2: "invalid_client" "Client authentication failed due to unknown client, no client authentication included, or unsupported authentication method.".
```

This happens when the OAuth application has the **Confidential** option enabled. `glab` is a
public client and cannot keep a client secret, so it authenticates without one. Confidential
applications require a client secret, which causes GitLab to reject the request.

To resolve this issue, edit your OAuth application and clear the **Confidential** checkbox.

After saving the change, run `glab auth login` again. For the full list of required application
settings, see [OAuth (GitLab Self-Managed, GitLab Dedicated)](#oauth-gitlab-self-managed-gitlab-dedicated).

### Error: `invalid_grant` when refreshing credentials

When running an authenticated command, `glab` might fail with an error:

```plaintext
Oauth2: "invalid_grant" "The provided authorization grant is invalid, expired, revoked, does not match the redirection URI used in the authorization request, or was issued to another client.".
```

This means the stored refresh token is no longer valid. Common causes include:

- You revoked the OAuth application or its authorization.
- The session expired after a long period of inactivity.
- A previous refresh did not save the replacement credentials, so the stored token
  remains the one GitLab already invalidated. This happens when `glab` cannot write to
  its credential storage, most often inside a sandbox such as Claude Code.

To resolve this issue, run `glab auth login` to authenticate again.

If you see this error regularly and you run `glab` inside a sandbox, fix the write restriction.
For details, see
[Claude Code sandboxing](#claude-code-sandboxing).
