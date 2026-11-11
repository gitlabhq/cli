---
stage: AI Clients
group: Developer Clients
info: To determine the technical writer assigned to the Stage/Group associated with this page, see <https://handbook.gitlab.com/handbook/marketing/product-and-technical-marketing/technical-writing/#assignments>
---

# Reviewer and maintainer guide

This page is for GitLab team members who want to review or maintain the GitLab CLI.
Reviewing is a great way to gain experience in Go.

To track your progress, open an issue with the
[Maintainer Onboarding template](https://gitlab.com/gitlab-org/cli/-/issues/new?issuable_template=Maintainer%20Onboarding).

## Become a reviewer

1. Create a merge request that adds `cli: reviewer` under `projects:` in your
   [team member entry](https://gitlab.com/gitlab-com/www-gitlab-com/-/blob/master/doc/team_database.md)
   in `www-gitlab-com` ([example](https://gitlab.com/gitlab-com/www-gitlab-com/-/blob/3284a59d69e2b2ae9bb1fb8bc03b6be2d29aa6cb/data/team_members/person/d/dbickford.yml#L13-14)). If you already have a `projects:` key, add
   the line to it:

   ```yaml
   projects:
     gitlab:
     - reviewer backend
     cli: reviewer
   ```

1. Join the `#f_cli` Slack channel.
1. Set up your local environment. See [`CONTRIBUTING.md`](../CONTRIBUTING.md).

Community contributions pick reviewers from the reviewer roulette, which is rebuilt
from the team database. You start receiving them after the next roulette build.

### Review merge requests

- Before you review, it helps to have a passing GitLab Duo Code Review. This applies to
  community contributions and all other merge requests. GitLab Duo Code Review checks
  changes against the project conventions in
  [`.gitlab/duo/mr-review-instructions.yaml`](../.gitlab/duo/mr-review-instructions.yaml).
- For risky or complex changes, ask an agent to build `glab` from `main` and from the
  merge request branch. Have it run the changed commands with both binaries and compare
  the results. This finds defects that a review of the diff can miss.
- For the approvals a merge request needs, see
  [the rules for merging](development_process.md#who-reviews-the-changes).

## Become a maintainer

The CLI follows the handbook's
[accelerated maintainer onboarding for smaller projects](https://handbook.gitlab.com/handbook/engineering/workflow/code-review/#accelerated-maintainer-onboarding-for-smaller-projects).
Apply when you feel confident in the codebase, for example after you've authored and
reviewed a few merge requests. If you have strong Go experience, you may need fewer.
Ask in `#f_cli` if you're unsure.

1. Create a merge request that changes your team member entry to
   `cli: maintainer` ([example](https://gitlab.com/gitlab-com/www-gitlab-com/-/blob/3284a59d69e2b2ae9bb1fb8bc03b6be2d29aa6cb/data/team_members/person/j/jay_mccure.yml#L20-23)). Link your onboarding issue, assign it to
   your manager, and ask a CLI maintainer to approve. Once it merges, you're also
   assigned dependency update merge requests from `gitlab-dependency-update-bot` (Renovate).
1. After it merges, ask an Owner of the
   [`gitlab-cli-maintainers`](https://gitlab.com/groups/gitlab-com/ai-engineering/ai-coding/teams/gitlab-cli-maintainers/-/group_members)
   group to add you with the Maintainer role. This gives you the same role in
   [`gitlab-org/cli`](https://gitlab.com/gitlab-org/cli) and
   [`gitlab-org/security/cli`](https://gitlab.com/gitlab-org/security/cli), and makes
   you a Code Owner.
1. Request the rest of the [access you need](#access-you-need).

## Preferred domain knowledge

- [Go](https://go.dev/learn/) and [Cobra](https://cobra.dev/), the command framework.
- [`client-go`](https://gitlab.com/gitlab-org/api/client-go), the GitLab API client.
- The noun-first command grammar in [`CONTRIBUTING.md`](../CONTRIBUTING.md).
- The code conventions in [`.gitlab/duo/mr-review-instructions.yaml`](../.gitlab/duo/mr-review-instructions.yaml).
- [Path ownership and merge access](path_ownership.md).
- The [release process](release_process.md) and [security releases](security_releases.md).

## Access you need

Maintainers need this access, in addition to the `gitlab-cli-maintainers` group:

- The "GitLab CLI Vault" 1Password vault. The [release process](release_process.md#notifying-maintainers)
  uses it for the contacts of community package maintainers.
- The "GitLab.com Service - Platform Engineering - Code Review - gl" 1Password vault.
  It holds the credentials for the release bot service account.
- The `gitlab-cli@gitlab.com` email group. It's the contact address for the Homebrew
  and Snap packages, and its members are the CLI maintainers.

To get each one, create an
[access request](https://handbook.gitlab.com/handbook/eta/corporate-it/end-user-services/access-requests/).

## Responsibilities

Maintainers are responsible for reviewing and merging merge requests, triaging incoming issues, ensuring pipeline stability, and managing releases.

### Merge rules

Before merging a merge request, verify that:

- The pipeline passes.
- Code changes follow the project conventions in [`.gitlab/duo/mr-review-instructions.yaml`](../.gitlab/duo/mr-review-instructions.yaml). GitLab Duo Code Review can check this for you.
- Required approvals from appropriate code owners or domain teams are satisfied (see [Path ownership and merge access](path_ownership.md)).
- The merge request title follows [Conventional Commits](https://www.conventionalcommits.org/). Squash is on by default, so the title becomes the commit message on `main`.

### Runbooks

Familiarize yourself with the following maintainer workflows and runbooks:

- [Release process](release_process.md)
- [Security releases](security_releases.md)
- [Triaging bugs](triaging_bugs.md)
- [Testing against staging](testing_against_staging.md)
- [Slack notifications](slack_notifications.md)

## Granting access to others

Domain teams that own a subtree of this repository do not need the Maintainer
role. Approval, merging, and administration are separate controls. See
[Path ownership and merge access](path_ownership.md).
