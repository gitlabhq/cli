<!--
  Update the title of this issue to: Maintainer Onboarding - [full name]
-->

## Basic setup

- [ ] Create a merge request updating [your team member entry](https://gitlab.com/gitlab-com/www-gitlab-com/blob/master/doc/team_database.md) adding yourself as a [`reviewer` or `trainee_maintainer`](https://handbook.gitlab.com/handbook/engineering/workflow/code-review/#learning-to-be-a-maintainer) of the `gitlab-cli` project.
- [ ] Join the `#f_cli` Slack channel.
- [ ] Read and understand the [maintainer responsibilities](https://gitlab.com/gitlab-org/cli/-/blob/main/docs/maintainer.md) for this project.
  - [ ] Get the required access mentioned in the above document.
- [ ] Browse through the [development resources](https://gitlab.com/gitlab-org/cli/-/blob/main/docs/development_process.md) to get an idea of how the CLI works.
- [ ] Open a merge request to improve the [documentation](https://gitlab.com/gitlab-org/cli/-/tree/main/docs) or [Maintainer Onboarding template](https://gitlab.com/gitlab-org/cli/-/blob/main/.gitlab/issue_templates/Maintainer%20Onboarding.md).
- [ ] _Optional_: [Pair](#code-review-pairing) with a maintainer to review a merge request or implement a small change.
- [ ] _Optional_: Read the [code review page in the handbook](https://handbook.gitlab.com/handbook/engineering/workflow/code-review/) and the [code review guidelines](https://docs.gitlab.com/development/code_review/).
- [ ] _Optional_: Read and understand [how to become a maintainer](https://handbook.gitlab.com/handbook/engineering/workflow/code-review/#how-to-become-a-project-maintainer).

### Code Review Pairing

Much like pair programming, pairing on code review is a great way to knowledge share and collaborate on merge request. This is a great activity for trainee maintainers to participate with maintainers for learning their process of code review.

A **private code review session** (unrecorded) involves one primary reviewer, and a shadow. If more than one shadow wishes to observe a private session, please consider obtaining consent from the merge request author.

A **public code review session** involves a primary reviewer and one or more shadows in a recorded session that is released publicly, for example to GitLab Unfiltered.

- If the merge request author is a GitLab team member, please consider obtaining consent from them.
- If the merge request author is a community contributor, you **must** obtain consent from them.
- Do **not** release reviews of security merge requests publicly.

## When you're ready to make it official

When reviews have accumulated, you can confidently address the majority of the MR's assigned to you,
and recent reviews consistently fulfill maintainer responsibilities, then you can propose yourself as a new maintainer
for the relevant application.

Remember that even when you are a maintainer, you can still request help from other maintainers if you come across an MR
that you feel is too complex or requires a second opinion.

If you need assistance with any of the following steps, ask in either the `#f_cli` Slack channel.

- [ ] Create a merge request updating [your team member entry](https://gitlab.com/gitlab-com/www-gitlab-com/blob/master/doc/team_database.md) proposing yourself as a maintainer for the `gitlab-cli` project, assigned to your manager.
- [ ] Ask an existing maintainer to give you the Maintainer role in this GitLab project.
- [ ] Keep reviewing, start merging :metal:
