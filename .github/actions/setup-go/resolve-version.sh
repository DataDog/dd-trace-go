#!/usr/bin/env bash
# Derives the cache-key version from the toolchain that actually ran, and
# writes `go-version-key=<value>` to GITHUB_OUTPUT.
#
# Stable toolchains key on the resolved MINOR: `stable`, `oldstable`,
# `1.27` and `1.27.4` all key on `1.27`. The minor rotates only when the
# toolchain minor changes, and patch releases change nothing. Reading
# `go env GOVERSION` (not the input string) makes the key follow the
# toolchain that compiled: with GOTOOLCHAIN=auto, a go.mod `toolchain`
# directive can switch the real compiler away from the requested version,
# and a key derived from the input would then describe a toolchain that
# never ran.
#
# Prerelease toolchains key on their exact semver (go1.28rc1 -> 1.28.0-rc.1):
# the cloudx provider's nested installer resolves the go-version input as a
# semver range, and a range cannot match a prerelease, so only the exact
# spec finds the rc binary the select-go step installed. Prerelease legs
# therefore hold their own keys — no prerelease workload seeds, so this
# costs the pool nothing.
#
# Tested by scripts/actiontest.
set -euo pipefail

goversion="$(go env GOVERSION)"

case "$goversion" in
  go1.*) ;;
  *)
    echo "::error::cannot derive a cache-key version from GOVERSION '${goversion}'"
    exit 1
    ;;
esac

version="${goversion#go}" # 1.27.4, 1.27, 1.28rc1 or 1.28beta2
if [[ "$version" =~ ^1\.([0-9]+)(\.[0-9]+)?((rc|beta)([0-9]+))?$ ]]; then
  minor="${BASH_REMATCH[1]}"
  pre_name="${BASH_REMATCH[4]:-}"
  pre_serial="${BASH_REMATCH[5]:-}"
  key="1.${minor}"
  if [[ -n "$pre_name" ]]; then
    key="1.${minor}.0-${pre_name}.${pre_serial}"
  fi
else
  echo "::error::GOVERSION '${goversion}' does not resolve to a Go version class"
  exit 1
fi

printf 'go-version-key=%s\n' "$key" >> "$GITHUB_OUTPUT"
printf 'Cache-key version: %s (from %s)\n' "$key" "$goversion"
