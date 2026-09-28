---
stage: Create
group: Code Review
info: To determine the technical writer assigned to the Stage/Group associated with this page, see <https://handbook.gitlab.com/handbook/product/ux/technical-writing/#assignments>
---

# Path ownership and merge access

This document explains how a team gets approval and merge rights over a
subtree of this repository without being granted the Maintainer role on the
whole project.

## The three layers

Approval, merging, and administration are separate controls. Keeping them
separate is what lets a domain team own its own paths while the CLI
maintainers keep the project settings.

| Layer | Where it is configured | Scope | What it grants |
| --- | --- | --- | --- |
| Approval | [`.gitlab/CODEOWNERS`](../.gitlab/CODEOWNERS) | Per path | Satisfies the required code owner approval on matching files |
| Merge | Protected branch **Allowed to merge** on `main` | Whole branch | Selects the merge button after approvals are satisfied |
| Administration | Project or group role | Whole project | Settings, CI/CD variables, protected branches, membership |

The Maintainer role is only needed for the third layer. A team that wants to
review and ship changes to its own paths needs the Developer role, a
CODEOWNERS entry, and an **Allowed to merge** entry.

## Behavior to understand before you change anything

- **A merge access entry is not path-scoped.** Anyone in **Allowed to merge**
  can merge any merge request that targets `main`, not only the paths they
  own. The approval requirement is what keeps changes scoped, because an
  unapproved merge request cannot be merged by anyone.
- **`.gitlab/CODEOWNERS` is a single section**, so a file matches exactly one
  pattern, and one approval from any owner listed on that pattern satisfies
  the requirement. Listing a domain team next to the maintainers means either
  can approve, not both.
- **A later pattern replaces the owners of an earlier one** rather than adding
  to them. Every pattern repeats the maintainers, and every documentation
  pattern also repeats `@gl-docsteam`.
- **An inherited role cannot be lowered at the project level.** Members who
  hold a role through the `gitlab-org` group keep it regardless of what is
  configured here.
- **An invited group grants each member the lower of their role in that group
  and the level the group was invited at.** Inviting a group at Developer does
  not promote anyone. When auditing, remember that the `members/all` endpoint
  for a group also lists people who inherit into it from a parent group, so it
  overstates who the invite actually covers.
- **Approvals are recomputed when owned files change**, because
  `selective_code_owner_removals` is enabled. Pushing a change to an owned
  file drops that owner's approval and keeps the others.

## Current ownership

| Path | Approvers in addition to the maintainers |
| --- | --- |
| `/README.md`, `/CONTRIBUTING.md`, `/docs/` | `@gl-docsteam` |
| `/internal/dependencyfirewall/`, `/internal/commands/df/` | Dependency firewall reviewers |
| `/internal/commands/govern/` | Govern reviewers |
| `/internal/commands/orbit/` | `@gitlab-org/orbit/team` |

Documentation subtrees for a delegated area list `@gl-docsteam` as well as the
owning team, so a documentation-only change does not need the owning team.

## Delegate a path to a team

Prefer a group over a list of usernames. A group keeps CODEOWNERS short, and
membership is then managed by the team instead of by a CLI maintainer.

### Prerequisites

- The Maintainer role on `gitlab-org/cli`.
- The Maintainer or Owner role in the group you are inviting. This is a
  property of the invited group, not of `gitlab-org`. Without it, the invite
  returns `404 Not Found`.

### Steps

1. **Invite the group to the project with the Developer role.** A group can
   only be used in CODEOWNERS or in a protected branch rule after it has been
   invited, unless it is an ancestor of the project. A sibling subgroup under
   `gitlab-org` is not an ancestor.

   ```shell
   glab api --method POST "projects/gitlab-org%2Fcli/share" \
     -f group_id=<GROUP_ID> -f group_access=30
   ```

1. **Add the CODEOWNERS entries.** Repeat the maintainers on every line, and
   add `@gl-docsteam` on documentation lines.

   ```plaintext
   /internal/commands/<area>/ @gitlab-com/ai-engineering/ai-coding/teams/gitlab-cli-maintainers @<group>
   /docs/source/<area>/ @gitlab-com/ai-engineering/ai-coding/teams/gitlab-cli-maintainers @gl-docsteam @<group>
   ```

1. **Grant merge access on `main`.** The `PATCH` is additive, so existing
   entries are preserved and do not need to be repeated.

   ```shell
   echo '{"allowed_to_merge":[{"group_id":<GROUP_ID>}]}' \
     | glab api --method PATCH "projects/gitlab-org%2Fcli/protected_branches/main" \
       --input - -H "Content-Type: application/json"
   ```

   For an individual instead of a group, use `{"user_id":<USER_ID>}`. The user
   must already be a project member.

1. **Verify.**

   ```shell
   glab api "projects/gitlab-org%2Fcli/protected_branches/main"
   ```

1. **Do not grant the Maintainer role.** If the team asks for it to get the
   merge button, the **Allowed to merge** entry from step 3 is what they
   actually need.

## Remove a delegation

Remove the CODEOWNERS lines, then remove the merge access entry by its `id`:

```shell
echo '{"allowed_to_merge":[{"id":<ENTRY_ID>,"_destroy":true}]}' \
  | glab api --method PATCH "projects/gitlab-org%2Fcli/protected_branches/main" \
    --input - -H "Content-Type: application/json"
```

Removing the project share is a separate step, and is only needed if the team
should lose read and write access altogether.
