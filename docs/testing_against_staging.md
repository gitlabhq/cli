---
stage: AI Clients
group: Developer Clients
info: To determine the technical writer assigned to the Stage/Group associated with this page, see <https://handbook.gitlab.com/handbook/marketing/product-and-technical-marketing/technical-writing/#assignments>
---

# Test GitLab.com changes on a staging environment with `glab`

To test GitLab.com changes that affect `glab` before they reach production, configure `glab` to
access the GitLab staging environment (`staging.gitlab.com`).

## Configure `glab` to use `staging.gitlab.com`

Configure `glab` to use `staging.gitlab.com` in the same way you configure a GitLab Self-Managed instance.

Prerequisites:

- GitLab CLI (`glab`) installed on your system.
- A user with access to `staging.gitlab.com`.
- A personal access token (PAT) with the `api` scope.
- Optional. An SSH key configured for `staging.gitlab.com`.
- For GitLab Duo features: see the [Test GitLab Duo features with `glab`](#test-gitlab-duo-features-with-glab) section

To configure `glab` for staging, use either the interactive login method, or the manual configuration method:

### Interactive login method (recommended)

1. Run this command:

   ```shell
   glab auth login --hostname staging.gitlab.com
   ```

1. When prompted, enter your personal access token.
1. Choose your preferred settings.

> [!note]
> When using `glab auth login` (without specifying `--hostname staging.gitlab.com`) in interactive mode,
> to use `staging.gitlab.com` you must select `GitLab Self-Managed or GitLab Dedicated instance`.

### Manual configuration method

1. Edit the `glab` configuration file located at `~/.config/glab-cli/config.yml`:

   ```yaml
   hosts:
       staging.gitlab.com:
           token: glpat-xxxxxxxxxxxxxxxxxxxx  # Replace with your staging PAT
           container_registry_domains: staging.gitlab.com,staging.gitlab.com:443,registry.staging.gitlab.com
           api_host: staging.gitlab.com
           git_protocol: ssh
           api_protocol: https
           user: your_username  # Replace with your GitLab username
   ```

1. Verify authentication with this command:

   ```shell
   glab auth status --hostname staging.gitlab.com
   ```

You should see confirmation that you're authenticated to `staging.gitlab.com`.

### Clone a repository from staging

`glab` uses the GitLab instance of the current directory's Git repository. To clone a repository
from the staging environment, run this command:

```shell
git clone git@staging.gitlab.com:[group]/[project].git
cd [project]
```

From this directory, `glab` uses `staging.gitlab.com` as the default instance.

## Test GitLab Duo features with `glab`

Prerequisites:

- Group membership: You must be a member of the top-level group under test. To verify this:
  1. On `staging.gitlab.com`, go to the group.
  1. Verify your membership status and role.
  1. If you are not already a member, request access.

  > [!important]
  > Ensure that the test user is not in any other top-level groups, to avoid inheriting access from other group memberships.

- Seat assignment: Depending on your test scenario, ensure your user account has the correct roles:
  - For testing licensed features: has a seat assigned.
  - For testing unlicensed or restricted behavior: does not have a seat assigned.
  - To check the GitLab Duo seat assignment, sign in as a group administrator, and go to the group's **Settings** > **GitLab Duo** page.

To test `glab` GitLab Duo AI features, run this command:

```shell
glab duo ask --git "how to create a branch"
```

If you receive an error about feature availability, verify:

- Your group membership status.
- Your seat assignment matches test requirements.
- The group has GitLab Duo features enabled in staging.
