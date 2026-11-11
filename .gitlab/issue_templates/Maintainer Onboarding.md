<!--
  Update the title of this issue to: Maintainer Onboarding - [full name]
-->

Follow the [reviewer and maintainer guide](https://gitlab.com/gitlab-org/cli/-/blob/main/docs/maintainer.md). Stop after the reviewer
steps if you only want to review.

## Become a reviewer

- [ ] Create a merge request adding `cli: reviewer` under `projects:` to [your team member entry](https://gitlab.com/gitlab-com/www-gitlab-com/-/blob/master/doc/team_database.md) ([example](https://gitlab.com/gitlab-com/www-gitlab-com/-/blob/3284a59d69e2b2ae9bb1fb8bc03b6be2d29aa6cb/data/team_members/person/d/dbickford.yml#L13-14)).
- [ ] Join the `#f_cli` Slack channel.
- [ ] Set up your local environment with [`CONTRIBUTING.md`](https://gitlab.com/gitlab-org/cli/-/blob/main/CONTRIBUTING.md).
- [ ] Read [how to review merge requests](https://gitlab.com/gitlab-org/cli/-/blob/main/docs/maintainer.md#review-merge-requests).
- [ ] Study the [preferred domain knowledge](https://gitlab.com/gitlab-org/cli/-/blob/main/docs/maintainer.md#preferred-domain-knowledge).
- [ ] Open a merge request that improves the [documentation](https://gitlab.com/gitlab-org/cli/-/tree/main/docs) or this template, fixes a bug, or adds a small enhancement such as a new flag.
- [ ] _Optional_: Pair with a current maintainer on a review.
- [ ] _Optional_: Read the [code review page in the handbook](https://handbook.gitlab.com/handbook/engineering/workflow/code-review/) and the [code review guidelines](https://docs.gitlab.com/development/code_review/).

## Become a maintainer

The CLI follows the handbook's
[accelerated maintainer onboarding for smaller projects](https://handbook.gitlab.com/handbook/engineering/workflow/code-review/#accelerated-maintainer-onboarding-for-smaller-projects).
Apply when you feel confident in the codebase, for example after you've authored and
reviewed a few merge requests. If you have strong Go experience, you may need fewer.

Remember that even when you are a maintainer, you can still request help from other maintainers if you come across an MR
that you feel is too complex or requires a second opinion.

If you need assistance with any of the following steps, ask in the `#f_cli` Slack channel.

- [ ] Create a merge request changing your team member entry to `cli: maintainer` ([example](https://gitlab.com/gitlab-com/www-gitlab-com/-/blob/3284a59d69e2b2ae9bb1fb8bc03b6be2d29aa6cb/data/team_members/person/j/jay_mccure.yml#L20-23)), link this issue, assign it to your manager, and ask a CLI maintainer to approve.
- [ ] Ask an Owner of the [`gitlab-cli-maintainers`](https://gitlab.com/groups/gitlab-com/ai-engineering/ai-coding/teams/gitlab-cli-maintainers/-/group_members) group to add you with the Maintainer role.
- [ ] Request the [access you need](https://gitlab.com/gitlab-org/cli/-/blob/main/docs/maintainer.md#access-you-need):
  - [ ] "GitLab CLI Vault" 1Password vault: contacts for community package maintainers
  - [ ] "GitLab.com Service - Platform Engineering - Code Review - gl" 1Password vault: service account for automated releases
  - [ ] `gitlab-cli@gitlab.com` email group: contact address for the Homebrew and Snap packages
- [ ] Keep reviewing, start merging :metal:
