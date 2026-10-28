# Documentation

## Generated Command Documentation

> **Note:** Do not edit files in `docs/source/` directly. The documentation in `docs/source/` is automatically generated from Go source files.

If you add a new command or update existing commands, update the `cobra.Command` definitions in the Go source code, then regenerate the documentation by running:

```shell
make gen-docs
```

## Hand-written Guides and Runbooks

The following guides and runbooks are maintained manually:

- [Maintainer Guide](maintainer.md)
- [Development Process](development_process.md)
- [Release Process](release_process.md)
- [Security Releases](security_releases.md)
- [Triaging Bugs](triaging_bugs.md)
- [Testing Against Staging](testing_against_staging.md)
- [Slack Notifications](slack_notifications.md)
