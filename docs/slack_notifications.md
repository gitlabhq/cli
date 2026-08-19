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

The Slack notification includes:

- **Release version** with pre-release indicator
- **Changelog** grouped by conventional commit types:
  - **Features** - New functionality (`feat:`)
  - **Bug Fixes** - Bug fixes (`fix:`)
  - **Documentation** - Documentation updates (`docs:`)
  - **Maintenance** - CI changes, refactoring, other chores (`chore:`, `ci:`, `refactor:`)
  - **Dependencies** - Dependency updates (`chore(deps):`, `chore(dependencies):`)
  - **Others** - Everything else
- **Commit information** - Each entry includes commit hash (linked) and full author name/email
- **Download links** to GitLab release page
- **Installation instructions** for Homebrew and Docker

### Example Notification

```plaintext
🚀 glab v1.35.0 has been released!

What's Changed

Features
• Add support for stacked diffs - a1b2c3d4 by Kai Armstrong <karmstrong@gitlab.com>
• Implement MCP server for AI assistants - e5f6g7h8 by Shekhar Patnaik <spatnaik@gitlab.com>

Bug Fixes
• Fix pipeline status display - i9j0k1l2 by Timo Furrer <tfurrer@gitlab.com>
• Resolve authentication issue with tokens - m3n4o5p6 by Kai Armstrong <karmstrong@gitlab.com>

Documentation
• Update installation instructions - q7r8s9t0 by Achilleas Pipinellis <axil@gitlab.com>

Maintenance
• Refactor make bootstrap to separate script - c9d0e1f2 by Kai Armstrong <karmstrong@gitlab.com>

Dependencies
• Update module github.com/mark3labs/mcp-go to v0.43.1 - u1v2w3x4 by GitLab Renovate Bot <gitlab-bot@gitlab.com>
• Update module golang.org/x/crypto to v0.45.0 - y5z6a7b8 by GitLab Renovate Bot <gitlab-bot@gitlab.com>

Get the Release
• View on GitLab
• Download Assets
```

> [!note]
> Commit hashes are clickable links to the actual commits on GitLab.

## Customization

### Change Notification Format

Edit the `message_template` in `.goreleaser.yml` under the `announce.slack` section.

Available template variables:

- `{{ .Tag }}` - Release tag
- `{{ .Version }}` - Version without 'v' prefix
- `{{ .IsPrerelease }}` - Boolean for pre-release status
- `{{ .ReleaseURL }}` - GitLab release URL
- `{{ .Changelog.Groups }}` - Grouped changelog entries
- `{{ .ProjectName }}` - Project name

### Change Commit Grouping

Modify the `changelog.groups` section in `.goreleaser.yml` to adjust how commits are categorized.

## Testing

Testing Slack notifications locally is challenging because:

1. **Snapshot mode skips announcements**: Running `goreleaser release --snapshot --clean` will not send Slack notifications, as snapshot mode automatically skips the announce step
1. **GoReleaser Pro required**: The `goreleaser announce` command (which would allow testing announcements separately) is only available in GoReleaser Pro
1. **Full release needed**: To test announcements with the free version, you would need to do a full release (not recommended for testing)

### Alternative: Test the Slack Webhook Directly

You can verify your Slack webhook works by sending a test message with curl:

```shell
curl -X POST -H 'Content-type: application/json' \
  --data '{
    "channel": "#test-channel",
    "username": "glab Release Bot",
    "icon_emoji": ":rocket:",
    "text": ":rocket: *glab v1.0.0-test* has been released!\n\n*What'\''s Changed*\n• Test change\n\n*Get the Release*\n• <https://gitlab.com/gitlab-org/cli/-/releases/v1.0.0-test|View on GitLab>"
  }' \
  YOUR_SLACK_WEBHOOK_URL
```

### Recommended Approach

The most reliable way to verify Slack notifications work correctly is to:

1. Test the webhook URL directly using curl (as shown above)
1. Review the notification template in `.goreleaser.yml` for syntax errors
1. Let the notification run during an actual release (consider using a pre-release tag like `v1.0.0-rc1` for your first test)

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
