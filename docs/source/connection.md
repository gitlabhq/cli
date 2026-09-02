---
title: Connect to your GitLab instance
stage: AI Coding
group: Code Review
info: To determine the technical writer assigned to the Stage/Group associated with this page, see <https://handbook.gitlab.com/handbook/product/ux/technical-writing/#assignments>
---

{{< details >}}

- Tier: Free, Premium, Ultimate
- Offering: GitLab.com, GitLab Self-Managed, GitLab Dedicated

{{< /details >}}

By default, `glab` connects to GitLab.com. To use a GitLab Self-Managed or GitLab Dedicated
instance instead, change your configuration. You can also configure `glab` for instances that
present a client or self-signed certificate.

## Set a default instance

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

When inside a Git repository, `glab` uses that repository's GitLab host by default. For example,
`glab issue list` lists all issues of the current directory's Git repository.

## Use mTLS certificates

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

## Use self-signed certificates

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
