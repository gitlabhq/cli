# Slack Release Notifications

This project is configured to send automated Slack notifications when a new release is published using GoReleaser.

## Setup

### 1. Create a Slack Incoming Webhook

1. Go to your Slack workspace's [Incoming Webhooks](https://api.slack.com/messaging/webhooks) page
1. Click "Create New App" or use an existing app
1. Enable "Incoming Webhooks"
1. Click "Add New Webhook to Workspace"
1. Select the channel where you want release notifications
1. Copy the webhook URL (format: `https://hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXXXXXXXXXX`)

### 2. Configure CI/CD Variables

Add the following variables to your GitLab CI/CD settings:

**Settings → CI/CD → Variables**

| Variable        | Value                            | Protected | Masked |
|-----------------|----------------------------------|-----------|--------|
| `SLACK_WEBHOOK` | Your webhook URL                 | ✓         | ✓      |
| `SLACK_CHANNEL` | Channel name (e.g., `#releases`) | ✓         | ✗      |

**Important:**

- Mark `SLACK_WEBHOOK` as **Protected** and **Masked** for security
- Only protected tags will trigger the webhook
- The `SLACK_CHANNEL` variable is optional; if not set, it uses the default channel from the webhook

### 3. Update CI/CD Configuration

The `.gitlab-ci.yml` release job needs to pass the Slack webhook environment variables to the GoReleaser Docker container.

Add the following lines to the `docker run` command in the `release` job:

```diff
+      -e SLACK_WEBHOOK=$SLACK_WEBHOOK \
+      -e SLACK_CHANNEL=$SLACK_CHANNEL \
```

## Notification Format

The Slack notification is a single [`markdown` block](https://docs.slack.dev/reference/block-kit/blocks/markdown-block/),
so headings, bold text, and links render in Slack. It includes:

- **Release version**
- **Changelog** grouped by conventional commit types, in this order:
  - **Features** - New functionality (`feat:`)
  - **Bug Fixes** - Bug fixes (`fix:`)
  - **Documentation** - Documentation updates (`docs:`)
  - **Maintenance** - CI changes, refactoring, other chores (`chore:`, `ci:`, `refactor:`)
  - **Dependencies** - Dependency updates (`chore(deps):`, `chore(dependencies):`)
  - **Others** - Everything else
- **Go module bumps** - When a release has two or more `update module` commits, Slack shows
  them as a single `* N Go module updates` line under **Dependencies**. The Go version bump,
  a single module bump, and other dependency lines stay listed.
- **Commit information** - Each entry has a bold `type(scope):` prefix, a linked
  commit hash, and the author name. Author emails are not included.
- **Author emoji** - 🤖 for `gitlab-bot@gitlab.com`, `:tanuki:` for other
  `@gitlab.com` authors, and `:community:` for everyone else.
- **Link** to the GitLab release page

Release notes longer than 10,000 characters are cut and end with `…`, to stay under
Slack's 12,000-character limit for a `markdown` block.

### Example Notification

```markdown
🚀 **glab v1.120.0** has been released!

### ✨ Features
* **feat(df):** add package command to check a single PURL ([c81a40c8](https://gitlab.com/gitlab-org/cli/-/commit/c81a40c8)) by Michael Eddington :tanuki:
* **feat(stack):** add a command to delete a stack ([6d7a7473](https://gitlab.com/gitlab-org/cli/-/commit/6d7a7473)) by Maksym Tymoshyk :community:

### 🐛 Bug Fixes
* **fix(mr):** fast-forward a checked-out branch on repeat checkout ([cb7c3eb5](https://gitlab.com/gitlab-org/cli/-/commit/cb7c3eb5)) by Jeston Singh :community:

### 🔧 Maintenance
* **refactor:** remove the custom --project and --hostname flag pair ([729129ac](https://gitlab.com/gitlab-org/cli/-/commit/729129ac)) by Jean van der Walt :tanuki:

### 📦 Dependencies
* **chore(deps):** update module gitlab.com/gitlab-org/api/client-go/v3 to v3.15.0 ([ad728cd0](https://gitlab.com/gitlab-org/cli/-/commit/ad728cd0)) by GitLab Renovate Bot 🤖

[View release on GitLab](https://gitlab.com/gitlab-org/cli/-/releases/v1.120.0)
```

The GitLab release page uses the same changelog lines, without the author emoji, and lists
every Go module bump individually.

## Customization

### Change Notification Format

In `.goreleaser.yml`:

- Edit the `text` of the `markdown` block under `announce.slack.blocks` to change the Slack
  post. `message_template` is only the plain-text fallback shown in notifications.
- Edit `changelog.format` to change each changelog line. This also changes the GitLab
  release page.

The changelog line writes the author type as a hidden HTML comment (`<!--bot-->`,
`<!--team-->`, or `<!--community-->`). GitLab does not display it, and the Slack template
replaces it with the author emoji.

Common template variables:

- `{{ .Tag }}` - Release tag
- `{{ .Version }}` - Version without 'v' prefix
- `{{ .ReleaseURL }}` - GitLab release URL
- `{{ .ReleaseNotes }}` - Generated changelog
- `{{ .ProjectName }}` - Project name

### Change Commit Grouping

Modify the `changelog.groups` section in `.goreleaser.yml` to adjust how commits are categorized.
GoReleaser assigns each commit to the first group whose `regexp` matches, and `order` sets the
display order. Keep **Dependencies** before **Maintenance** in the list, because the
**Maintenance** pattern also matches `chore(deps):`.

## Testing

Testing Slack notifications locally is challenging because:

1. **Snapshot mode skips announcements**: Running `goreleaser release --snapshot --clean` will not
   send Slack notifications, as snapshot mode automatically skips the announce step. A passing
   CI pipeline does not exercise the Slack templates.
1. **GoReleaser Pro required**: The `goreleaser announce` command (which would allow testing announcements separately) is only available in GoReleaser Pro
1. **Full release needed**: To test announcements with the free version, you would need to do a full release (not recommended for testing)

The release jobs pin the GoReleaser image to v2.18.3. Earlier versions fail with
`slack blocks: failed to unmarshal` when the configuration uses `blocks`.

### Alternative: Test the Slack Webhook Directly

You can verify your Slack webhook works, and preview a `markdown` block, by sending a test
message with curl:

```shell
curl -X POST -H 'Content-type: application/json' \
  --data '{
    "text": "glab v1.0.0-test has been released",
    "blocks": [
      {
        "type": "markdown",
        "text": "🚀 **glab v1.0.0-test** has been released!\n\n### ✨ Features\n* **feat(mr):** test change ([abcd1234](https://gitlab.com/gitlab-org/cli/-/commit/abcd1234)) by Test User :tanuki:\n\n[View release on GitLab](https://gitlab.com/gitlab-org/cli/-/releases)"
      }
    ]
  }' \
  YOUR_SLACK_WEBHOOK_URL
```

To preview a block without posting it, paste the `blocks` JSON into the
[Block Kit Builder](https://app.slack.com/block-kit-builder).

### Recommended Approach

The most reliable way to verify Slack notifications work correctly is to:

1. Test the webhook URL directly using curl (as shown above)
1. Run `goreleaser check` to validate `.goreleaser.yml`
1. Let the notification run during an actual release

## Troubleshooting

### Notifications not sending

1. Verify `SLACK_WEBHOOK` is set and not expired
1. Check that the webhook URL is correct
1. Ensure the release job has access to protected variables
1. Review GoReleaser logs in CI/CD pipeline

### Wrong channel

- Update the `SLACK_CHANNEL` variable
- Or modify the webhook's default channel in Slack settings

### Missing changelog entries

- Ensure commits follow [conventional commit format](https://www.conventionalcommits.org/)
- Check `changelog.filters.exclude` in `.goreleaser.yml` for excluded patterns
- Verify commits aren't filtered out by the exclude rules

## References

- [GoReleaser Slack Announce Documentation](https://goreleaser.com/customization/announce/slack/)
- [GoReleaser Changelog Documentation](https://goreleaser.com/customization/changelog/)
- [Conventional Commits Specification](https://www.conventionalcommits.org/)
