# Documentation

## Generated command documentation

> [!note]
> Do not edit `docs/navigation-glab.yaml`, the command list in the root [`README.md`](../README.md), or the generated pages in `docs/source/`. `make gen-docs` generates them from the Go source files. In `docs/source/`, `authentication.md`, `connection.md`, `proxies.md`, and `configuration.md` (except its environment variable table) are hand-written.

If you add a new command or update existing commands, update the `cobra.Command` definitions in the Go source code, then regenerate the documentation by running:

```shell
make gen-docs
```

## Hand-written guides and runbooks

The following guides and runbooks are maintained manually:

- [Reviewer and maintainer guide](maintainer.md)
- [Path ownership and merge access](path_ownership.md)
- [Development process](development_process.md)
- [Release process](release_process.md)
- [Security releases](security_releases.md)
- [Triaging bugs](triaging_bugs.md)
- [Testing against staging](testing_against_staging.md)
- [Slack notifications](slack_notifications.md)
- [Other installation options](installation_options.md)
