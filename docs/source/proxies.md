---
title: Connect through a proxy
stage: AI Coding
group: Code Review
info: To determine the technical writer assigned to the Stage/Group associated with this page, see <https://handbook.gitlab.com/handbook/product/ux/technical-writing/#assignments>
---

{{< details >}}

- Tier: Free, Premium, Ultimate
- Offering: GitLab.com, GitLab Self-Managed, GitLab Dedicated

{{< /details >}}

Some GitLab instances sit behind a proxy that requires its own credentials, separate from your
GitLab authentication. To reach these instances, configure `glab` to send custom HTTP headers
with every request.

The examples on this page are not exhaustive. Use them as a starting point for your proxy.

## Configure custom HTTP headers

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
- Fails if the command prints nothing, prints multiple lines or a `NUL` byte, or
  does not finish in 30 seconds.
- Runs each configured command once per process, then reuses the result for all
  requests, including OAuth token refresh.

The command is split into an executable and arguments, and does not run in a
shell. If you need shell expansion or pipelines, use an explicit shell, such as
`sh -c`. Use `valueFromEnv` instead if another process manages token refresh, or
if command startup time is a concern.

## Google Identity-Aware Proxy

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
