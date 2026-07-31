# Release process

## Automated weekly release

A new version is released automatically every Tuesday at 12:00 UTC by a pipeline
schedule. You don't need to do anything for a normal weekly release.

How it works:

1. The [**Weekly automated release schedule**](https://gitlab.com/gitlab-org/cli/-/pipeline_schedules)
   (`0 12 * * 2`, `Etc/UTC`) runs on `main` with the `release_schedule` input set
   to `true`, which sets the `RELEASE_SCHEDULE` CI/CD variable.
1. The `tag-release` job (`.gitlab-ci.yml`) runs `scripts/release/create-release-tag.sh`, which:
   - Finds the latest `vX.Y.Z` tag.
   - Skips the release if there are no releasable commits since that tag (merge
     commits don't count).
   - Bumps the version based on the Conventional Commit types since that tag: a
     `feat` bumps the minor version (`vX.Y.Z` → `vX.(Y+1).0`), otherwise it's a
     patch release (`vX.Y.Z` → `vX.Y.(Z+1)`). Then creates an annotated tag
     through the GitLab API using `glab`.
1. Creating the tag triggers the normal tag-driven release pipeline (the `release`
   job and the distribution jobs), exactly as a manual tag would.

The schedule only runs `tag-release`; the heavy build/test jobs are skipped on it
(see `.skip-on-release-schedule` in `.gitlab-ci.yml`) because the tag pipeline
re-runs and gates the actual release. Other schedules, such as the daily `main`
build, are unaffected.

### Forcing a release off-cycle

To release before the next Tuesday, run the schedule on demand. It takes the same
path as the weekly run, and the normal recurrence is unaffected. Needs the
Maintainer role.

Select the play button on the
[**Pipeline schedules** page](https://gitlab.com/gitlab-org/cli/-/pipeline_schedules),
or run:

```shell
# Verify the schedule ID on the Pipeline schedules page linked above.
glab schedule run 4282239 --repo gitlab-org/cli
```

Caveats:

- Nothing is released if there are no releasable commits since the last tag, or
  if the computed tag already exists. Re-running is safe.
- A breaking change (`!` in a commit subject) fails the job on purpose.
- You can't pick the version number. For that, or for a major release, use the
  manual process below.

## Manual release

To release a new version of the CLI manually, you must:

1. Do a quick test of the CLI in your local development. At this stage, you are only verifying there is no complete failure of the CLI.
1. To preview which commits will be included in a release, run this command:

   ```shell
   git log --oneline --invert-grep --grep="Merge branch" --invert-grep --grep="chore" $(git describe --tags --abbrev=0)..main | sort -k2
   ```

If those changes are appropriate to be released, proceed with the process:

1. Tag the latest commit on `main` (such as `git tag v1.74.1`).
1. Push with these commands: `git push origin main` and `git push origin v1.74.1`

## Notifying maintainers

If the release is time-sensitive, such as for a security release, consider emailing
the maintainers of the community-maintained channels. Contacts for the different maintainers
are available in CLI 1Password Vault in the **Maintainers of Community packages** note.

## Access to distribution channels

### Homebrew

Homebrew releases were [automated by the CI build](https://gitlab.com/gitlab-org/cli/-/merge_requests/1137).
These manual instructions are provided if the automation fails.

Prerequisites:

- An account on GitHub. (Any account is fine.)

To manually update the version available through Homebrew:

1. Generate a new token with the `repo`, `workflow`, and `gist` scopes [using this link](https://github.com/settings/tokens/new?scopes=gist,repo,workflow&description=Homebrew).
   If you have an existing token with the correct scope, you can use it instead.
1. On the [**Releases** page for this project](https://gitlab.com/gitlab-org/cli/-/releases), identify the release version you want to publish.
1. In the **Assets** area for the release, download the packaged source code (`Source code (tar.gz)`) for this release.
1. To compute the SHA256 checksum, run `sha256sum cli-v1.74.1.tar.gz`.
1. Create a pull request with the update with this command, modifying the source code URL and SHA as needed:

   ```shell
   brew bump-formula-pr --strict glab \
   --url="https://gitlab.com/gitlab-org/cli/-/archive/v1.74.1/cli-v1.74.1.tar.gz" \
   --sha256=b1b8ed3f4c7d8839b540d8a264b3a3ef670e78ae369ef0c01ef5d5e502714905
   ```

1. When the pull request is merged, the update is complete.

### Snapcraft

Snapcraft releases are automated:

- The `latest/edge` channel is automatically built on every commit to `main`.
- The `latest/stable` channel is automatically built when a new tag is pushed.

No manual action is required for Snapcraft releases.

### Scoop

No manual action required.

The `glab` project was moved to GitLab in [pull request 4168](https://github.com/ScoopInstaller/Main/pull/4168/files).
Scoop uses the [autoupdate URL](https://github.com/ScoopInstaller/Main/pull/4168/files#diff-f454f19e58d4c978be55818fa3c6ad5e1424e81fbb0b693dca0b76cc879f5457L21) for updating to newer versions.

### WinGet

Prerequisites:

- You must have a GitHub account.
- You must have signed Microsoft's Contributor License Agreement (CLA).
- You have read the GitLab policy for [Contributing to a third-party project on behalf of GitLab](https://handbook.gitlab.com/handbook/engineering/open-source/).
  (The confidential, internal [legal issue](https://gitlab.com/gitlab-com/legal-and-compliance/-/issues/1286) is also available.)

To update the WinGet package:

1. On the [**Releases** page for this project](https://gitlab.com/gitlab-org/cli/-/releases), identify the release version you want to publish.
1. In the **Assets** area for the release, identify the Windows installer package (the filename should end in `_installer.exe`) and download it.
1. Compute the SHA256 checksum for the file by running `sha256sum filename.exe`, replacing `filename` with the name of release installer you downloaded in the previous step. For example:

   ```shell
   $ sha256sum glab_1.23.1_Windows_x86_64_installer.exe

   36f9d45f68ea53cbafdbe96ba4206e4412abb4c2b8f00ba667054523bd7cc89e  glab_1.23.1_Windows_x86_64_installer.exe
   ```

1. Copy the SHA from the result.
1. On GitHub, create a pull request in the `microsoft/winget-pkgs` project. Use the
   [pull request to update to version 1.23.1](https://github.com/microsoft/winget-pkgs/pull/90349) as an example.

## Setting up CI/CD for releasing

For automated testing, you need to [set up credentials](https://gitlab.com/groups/gitlab-org/-/epics/8251) for unit testing.

For releasing, you also need to add a `GITLAB_TOKEN_RELEASE`. To create this token:

1. Go to Settings -> [Access Tokens](https://gitlab.com/gitlab-org/cli/-/settings/access_tokens)
1. Generate a new project token with `api` scope and `Maintainer` role.
1. Add the new token as `GITLAB_TOKEN_RELEASE` **protected** and **masked** CI/CD variables.
