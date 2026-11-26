#!/usr/bin/env bash
# Derives the cache-key version from the toolchain that actually ran, and
# writes `go-version-key=<value>` and `go-version-provider=<value>` to
# GITHUB_OUTPUT.
#
# Stable toolchains key on the resolved MINOR: every spelling that
# resolves to one minor (`stable`, `oldstable`, `1.N`, `1.N.P`) keys on
# `1.N` (an exact patch keys the cloudx cache on that patch; see
# below). The minor rotates only when the
# toolchain minor changes, and patch releases change nothing. Reading
# `go env GOVERSION` (not the input string) reports the installed
# toolchain: setup-go pins GOTOOLCHAIN=local, so GOVERSION is what the
# request resolved to on this runner.
#
# Prerelease toolchains key on their exact semver (go1.28rc1 -> 1.28.0-rc.1):
# the cloudx provider's nested installer resolves the go-version input as a
# semver range, and a range cannot match a prerelease, so only the exact
# spec finds the rc binary the select-go step installed. Prerelease legs
# therefore hold their own keys.
#
# A fully-specified request (`1.N.P`) installs exactly that patch —
# check-latest never upgrades a full version — but a minor key would let
# the provider's nested installer resolve the range to the LATEST patch
# and swap the toolchain out from under the "Verify" step. Exact-patch
# requests therefore pass their exact version to the provider (so their
# cloudx keys carry that patch); every other
# spelling keeps the version class. The provider input and the
# matched-key parsing below MUST stay the same string, so both read the
# go-version-provider output, not go-version-key.
#
# Outputs: go-version-key (the cache-key version class; also the
# tools-cache key) and go-version-provider (what the cloudx provider's
# go-version input and the restore-source parsing use).
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

# The provider version: the version class for the known unconstrained
# spellings (stable, oldstable, 1.N), the exact installed version for
# everything else. actions/setup-go also treats spellings such as
# =1.27.1, v1.27.1 and padded values as exact versions, so the check is
# inverted: anything the resolver does not know to be unconstrained
# passes the exact toolchain that ran, which the nested installer can
# always find. Prereleases keep the class (their key is already the
# exact semver).
provider="$key"
if [[ "$key" =~ ^1\.[0-9]+$ &&
      -n "${REQUESTED_VERSION:-}" &&
      ! "${REQUESTED_VERSION}" =~ ^(stable|oldstable|1\.[0-9]+)$ ]]; then
  provider="$version"
fi

printf 'go-version-key=%s\n' "$key" >> "$GITHUB_OUTPUT"
printf 'go-version-provider=%s\n' "$provider" >> "$GITHUB_OUTPUT"
printf 'Cache-key version: %s (from %s)\n' "$key" "$goversion"
