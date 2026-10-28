#!/usr/bin/env bash
#
# Checks that .tool-versions and .gitlab-ci.yml use the same Go toolchain,
# and that go.mod uses patch 0 and is not newer than that toolchain.

set -euo pipefail

errors=0

go_mod_version=$(awk '/^go / { print $2 }' go.mod)
tool_versions_version=$(awk '/^golang / { print $2 }' .tool-versions)
ci_version=$(awk -F'"' '/GO_VERSION:/ { print $2; exit }' .gitlab-ci.yml)

echo "Go versions found:"
echo "  go.mod:          ${go_mod_version:-<missing>}"
echo "  .tool-versions:  ${tool_versions_version:-<missing>}"
echo "  .gitlab-ci.yml:  ${ci_version:-<missing>}"
echo

if [ -z "$go_mod_version" ]; then
  echo "✖ ERROR: Could not parse Go version from go.mod" >&2
  errors=1
fi

if [ -z "$tool_versions_version" ]; then
  echo "✖ ERROR: Could not parse golang version from .tool-versions" >&2
  errors=1
fi

if [ -z "$ci_version" ]; then
  echo "✖ ERROR: Could not parse GO_VERSION from .gitlab-ci.yml" >&2
  errors=1
fi

if [ "$errors" -eq 1 ]; then
  exit 1
fi

if [ "$ci_version" != "$tool_versions_version" ]; then
  echo "✖ ERROR: .gitlab-ci.yml GO_VERSION (${ci_version}) does not match .tool-versions (${tool_versions_version})" >&2
  errors=1
fi

# https://docs.gitlab.com/development/go_guide/go_upgrade/#version-in-gomod
if [[ "$go_mod_version" != *.0 ]]; then
  echo "✖ ERROR: go.mod (${go_mod_version}) must use patch version 0" >&2
  errors=1
fi

if [ "$(printf '%s\n%s\n' "$go_mod_version" "$tool_versions_version" | sort -V | tail -1)" != "$tool_versions_version" ]; then
  echo "✖ ERROR: go.mod (${go_mod_version}) is newer than .tool-versions (${tool_versions_version})" >&2
  errors=1
fi

if [ "$errors" -eq 0 ]; then
  echo "✔ All Go versions are consistent."
else
  echo >&2
  echo "Please fix the Go versions above." >&2
  exit 1
fi
