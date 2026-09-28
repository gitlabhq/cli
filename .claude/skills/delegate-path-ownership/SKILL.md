---
name: delegate-path-ownership
description: Give a team approval and merge rights over a subtree of the GitLab CLI repository without granting the Maintainer role. Use when someone asks to add a group or user to CODEOWNERS, to let a team review and merge its own paths, or when a team asks for Maintainer so they can press the merge button. Takes a group (or usernames) and one or more paths.
---

# Delegate path ownership in the GitLab CLI

Grant a team approval rights over its paths and the ability to merge, while
the CLI maintainers keep project administration.

Background and the reasoning behind the model:
[`docs/path_ownership.md`](../../../docs/path_ownership.md).

## Inputs

Collect these before starting. Ask only for what is missing.

- **Group path** such as `gitlab-org/orbit/team`, or a list of usernames when
  the team has no group. Prefer a group.
- **Paths** to delegate, such as `internal/commands/orbit`. Ask whether the
  matching `docs/source/<area>/` subtree is included. It usually is.

## The model

Three independent controls. Only the third needs the Maintainer role, and
delegation never needs it.

1. `.gitlab/CODEOWNERS` grants **approval**, scoped per path.
1. Protected branch **Allowed to merge** on `main` grants the **merge button**,
   scoped to the whole branch.
1. The project role grants **administration**.

If someone asks for Maintainer so they can merge, they need control 2.

## Steps

### 1. Resolve IDs and check access

```shell
glab api "groups/<GROUP_PATH_URLENCODED>" | jq '{id, full_path}'
glab api "projects/gitlab-org%2Fcli" | jq '.shared_with_groups'
glab api "projects/gitlab-org%2Fcli/protected_branches/main"
```

Save the protected branch response before changing it. It is the rollback
reference.

For a username list, resolve each and confirm the person is already a project
member:

```shell
glab api "users?username=<USERNAME>" | jq '.[0].id'
glab api "projects/gitlab-org%2Fcli/members/all/<USER_ID>" | jq '.access_level'
```

### 2. Invite the group at Developer

Skip if `shared_with_groups` already lists it.

```shell
glab api --method POST "projects/gitlab-org%2Fcli/share" \
  -f group_id=<GROUP_ID> -f group_access=30
```

Developer is the right level. It is enough to approve, and the merge button
comes from step 4, not from the role.

**On `404 Not Found`:** the caller lacks the Maintainer or Owner role **in the
invited group**. This is a property of the invited group, not of `gitlab-org`.
Do not retry and do not escalate to a `gitlab-org` Owner. Instead, find who can
run it:

```shell
glab api "groups/<GROUP_ID>/members/all?per_page=100" \
  | jq -r '.[] | select(.access_level >= 40) | .username'
```

Report those usernames and stop this step. The remaining steps still work, so
continue to them and flag the invite as outstanding.

### 3. Edit CODEOWNERS

Read [`.gitlab/CODEOWNERS`](../../../.gitlab/CODEOWNERS) and add one line per
path, placed with the other specific patterns.

Repeat the maintainers on every line, because a later pattern replaces the
owners of an earlier one rather than adding to them. Add `@gl-docsteam` on any
line under `docs/`.

```plaintext
/internal/commands/<area>/ @gitlab-com/ai-engineering/ai-coding/teams/gitlab-cli-maintainers @<group>
/docs/source/<area>/ @gitlab-com/ai-engineering/ai-coding/teams/gitlab-cli-maintainers @gl-docsteam @<group>
```

The file is one section, so one approval from any owner on the matching line
satisfies the requirement. Listing the maintainers alongside a team means
either can approve, not both. Say this plainly when reporting, because people
expect it to mean both.

### 4. Grant merge access on `main`

Additive. Do not repeat existing entries.

```shell
echo '{"allowed_to_merge":[{"group_id":<GROUP_ID>}]}' \
  | glab api --method PATCH "projects/gitlab-org%2Fcli/protected_branches/main" \
    --input - -H "Content-Type: application/json"
```

Use `{"user_id":<USER_ID>}` for individuals. Several entries can go in one
call.

Both flags matter: `--input -` and the explicit `Content-Type` header. Without
the header the request fails with `415`.

### 5. Verify and open the merge request

```shell
glab api "projects/gitlab-org%2Fcli/protected_branches/main" \
  | jq '.merge_access_levels[] | {access_level_description, user_id, group_id}'
```

The CODEOWNERS edit is the only file change, so it goes in a merge request.
The membership and protected branch changes take effect immediately and are
not part of the diff. Say which is which in the description.

## Report back

State separately:

- Which changes are already live, meaning the invite and the merge access.
- Which are pending review, meaning the CODEOWNERS merge request.
- Anything blocked, such as an invite that needs a group Maintainer, and who
  can unblock it.

## Rollback

```shell
echo '{"allowed_to_merge":[{"id":<ENTRY_ID>,"_destroy":true}]}' \
  | glab api --method PATCH "projects/gitlab-org%2Fcli/protected_branches/main" \
    --input - -H "Content-Type: application/json"
```

Entry IDs come from the saved response in step 1.

## Do not

- Grant the Maintainer role to give someone the merge button.
- Assume a merge access entry is path-scoped. It covers the whole branch, and
  the approval requirement is what scopes a change.
- Try to lower an inherited role at the project level. It is not possible;
  inherited roles are changed in the `gitlab-org` group.
- Delete and recreate the protected branch. Use `PATCH`, which leaves `main`
  protected throughout.
