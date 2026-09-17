---
stage: Create
group: Code Review
info: To determine the technical writer assigned to the Stage/Group associated with this page, see <https://handbook.gitlab.com/handbook/product/ux/technical-writing/#assignments>
---

# Development process

This document describes how we make changes to the CLI. For contributing guidelines,
read [`CONTRIBUTING.md`](../CONTRIBUTING.md).

## Who can make changes?

**Anyone** can make changes. If you are planning larger changes that affect the
CLI architecture or dependencies,
[create an issue first](https://gitlab.com/gitlab-org/cli/-/issues/new?issuable_template=Feature%20Request).

## Who reviews the changes?

The CLI project uses the
[Recommend Reviewers flow](https://docs.gitlab.com/user/project/merge_requests/reviews/automatic_reviewer_assignment/#assign-reviewers-with-the-recommend-reviewers-flow).
When you mark a merge request as ready, the flow assigns the minimum number of
reviewers needed to satisfy each approval rule, accounting for their availability,
workload, and time zone. The assignments are attributed to the flow's service
account.

To pick someone else, edit the **Reviewers** list in the merge request sidebar.

The rules for merging:

- **Merge requests created by maintainers**: One review from a reviewer, then the
  author maintainer may merge the merge request.
- **Merge requests not created by maintainers**: One review from a reviewer, and one from a maintainer.

## Who releases the changes?

Only [project maintainers](https://gitlab.com/gitlab-org/cli/-/project_members?sort=access_level_desc)
can tag a release. Follow the [release process](release_process.md).
