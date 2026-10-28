---
stage: AI Clients
group: Developer Clients
info: To determine the technical writer assigned to the Stage/Group associated with this page, see <https://handbook.gitlab.com/handbook/product/ux/technical-writing/#assignments>
---

# Maintainer documentation

This document is for new CLI maintainers.

## Access you need

- Maintainer role in [`gitlab-org/cli`](https://gitlab.com/gitlab-org/cli) and [`gitlab-org/security/cli`](https://gitlab.com/gitlab-org/security/cli). To get it, ask existing maintainer.
- Access to the "GitLab CLI Vault" and "GitLab.com Service - Platform Engineering - Code Review - gl" 1Password vaults. To get access to these vaults,
  create an [access request](https://handbook.gitlab.com/handbook/eta/corporate-it/end-user-services/access-requests/).
- Create an [Access Request](https://handbook.gitlab.com/handbook/eta/corporate-it/end-user-services/access-requests/) and ask to be added to `gitlab-cli@gitlab.com` email group. EMs and PMs on the extension team also have access to this email group.

## Responsibilities

Maintainers are responsible for reviewing and merging merge requests, triaging incoming issues, ensuring pipeline stability, and managing releases.

### Merge rules

Before merging a merge request, verify that:

- All CI/CD pipeline jobs pass successfully.
- Code changes adhere to the [Go development guidelines](https://docs.gitlab.com/development/go_guide/) and linter checks pass.
- Required approvals from appropriate code owners or domain teams are satisfied (see [Path ownership and merge access](path_ownership.md)).
- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/) standards.

### Runbooks

Familiarize yourself with the following maintainer workflows and runbooks:

- [Release Process](release_process.md)
- [Security Releases](security_releases.md)
- [Triaging Bugs](triaging_bugs.md)
- [Testing Against Staging](testing_against_staging.md)
- [Slack Notifications](slack_notifications.md)

## Granting access to others

Domain teams that own a subtree of this repository do not need the Maintainer
role. Approval, merging, and administration are separate controls. See
[Path ownership and merge access](path_ownership.md).
