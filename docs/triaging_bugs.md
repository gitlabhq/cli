---
stage: AI Clients
group: Developer Clients
info: To determine the technical writer assigned to the Stage/Group associated with this page, see <https://handbook.gitlab.com/handbook/marketing/product-and-technical-marketing/technical-writing/#assignments>
---

# Create a bug in the glab project

Any contributor can raise bugs against the `glab` CLI project. Use the appropriate
issue template for your bug:

- For potential security vulnerabilities, create an issue
  [with the **Vulnerability Disclosure** template](https://gitlab.com/gitlab-org/cli/-/issues/new?issuable_template=Vulnerability%20Disclosure). This template creates a confidential issue.
- For all other bugs, create an issue [with the **Bug** template](https://gitlab.com/gitlab-org/cli/-/issues/new?issuable_template=Bug).

## Add labels to your bug

The issue templates help set the correct labels for your bug. Add any of these
labels that are also appropriate to your bug:

- `~"bug::performance"`: For bugs about the response time of `glab`.
- `~UX`: For user experience problems.

## Triage your bug

Use this guide to assign a severity label to your bug. A severity label helps the
`glab` CLI project team prioritize fixes. This project uses the same severity
guidelines as the wider GitLab organization. Use these `glab`-specific examples
to help you assess the problem:

- `~"severity::1"`: Blocker. A security vulnerability that requires an immediate fix.
- `~"severity::2"`: Critical.
  - All `glab` commands fail, even when the target GitLab instance is working as expected.
  - A security vulnerability with a fix needed for next release.
- `~"severity::3"`: Major.
  - A command fails with no workaround.
  - A command fails with an unexpected error.
  - The command output is incorrect.
  - The documentation is inaccurate.
- `~"severity::4"`: Low.
  - A cosmetic bug.
  - The command output could be improved.
  - The command fails, but a suitable workaround exists.
  - A documentation improvement.

For full details, see
[Severity](https://handbook.gitlab.com/handbook/product-development/how-we-work/issue-triage/#severity)
in the GitLab engineering handbook.
