---
title: Configure the CLI
stage: AI Coding
group: Code Review
info: To determine the technical writer assigned to the Stage/Group associated with this page, see <https://handbook.gitlab.com/handbook/product/ux/technical-writing/#assignments>
---

{{< details >}}

- Tier: Free, Premium, Ultimate
- Offering: GitLab.com, GitLab Self-Managed, GitLab Dedicated

{{< /details >}}

By default, `glab` follows the
[XDG Base Directory Spec](https://specifications.freedesktop.org/basedir-spec/basedir-spec-latest.html),
which means it searches for configuration files in multiple locations with proper precedence.

## Configuration levels

Configure `glab` at different levels: system-wide, globally (per-user), locally (per-repository), or per host:

- **System-wide** (for all users): Place configuration at `/etc/xdg/glab-cli/config.yml` (or `$XDG_CONFIG_DIRS/glab-cli/config.yml`).
  - Use this level for Linux distributions and system administrators to provide default configurations.
  - User configurations override system-wide settings.
- **Globally** (per-user): run `glab config set --global editor vim`.
  - The global configuration file is available at `~/.config/glab-cli/config.yml` (or `$XDG_CONFIG_HOME/glab-cli/config.yml`).
  - To override this location, set the `GLAB_CONFIG_DIR` environment variable.
- **The current repository**: run `glab config set editor vim` in any folder in a Git repository.
  - The local configuration file is available at `.git/glab-cli/config.yml` in the current working Git directory.
- **Per host**: run `glab config set editor vim --host gitlab.example.org`, changing
  the `--host` parameter to meet your needs.
  - Per-host configuration information is always stored in the global configuration file, with or without the `global` flag.

## Configuration search order

When `glab` looks for configuration files, it searches in this order (highest priority first):

1. `$GLAB_CONFIG_DIR/config.yml` (if `GLAB_CONFIG_DIR` is set)
1. `~/.config/glab-cli/config.yml` (legacy location, for backward compatibility)
1. `$XDG_CONFIG_HOME/glab-cli/config.yml` (platform-specific `XDG_CONFIG_HOME` location)
1. `$XDG_CONFIG_DIRS/glab-cli/config.yml` (system-wide configuration files, default: `/etc/xdg/glab-cli/config.yml`)

The first configuration file found is used.

### Configuration file locations

**For backward compatibility**, `glab` checks `~/.config/glab-cli/config.yml` first on all platforms.
If no legacy configuration file exists, `glab` uses these platform-specific
`XDG_CONFIG_HOME` locations:

- **Linux**: `~/.config/glab-cli/config.yml`
- **macOS**: `~/Library/Application Support/glab-cli/config.yml`
- **Windows**: `%LOCALAPPDATA%\glab-cli\config.yml`

> [!note]
> If you have configuration files in both the legacy location (`~/.config/glab-cli/config.yml`)
> and the platform-specific `XDG_CONFIG_HOME` location, `glab` uses the legacy location and
> displays a warning. Consider consolidating to one location to avoid confusion.
