#!/usr/bin/env bash
# Computes the cache-key prefix for the setup-go action and writes
# `prefix=<value>` to $GITHUB_OUTPUT.
#
# Environment:
#   FAMILY   logical workload family
#   VARIANT  JSON object describing the workload variant; empty means {}
#
# Canonicalization: sort object keys and the `modules` list, so two callers
# describing the same variant hash identically regardless of the order their
# expressions produced. Matrix-driven callers pass the module selection as a
# space-separated string (GitHub matrix values cannot be arrays); it is turned
# into the same sorted array as the JSON array form, so both hash identically.
# Tested by scripts/actiontest.
set -euo pipefail

# The default is assigned on its own line: bash 3.2 (macOS /bin/bash) keeps
# the backslash of "${VARIANT:-{\}}" inside double quotes and yields `{\}`.
variant_json="${VARIANT:-}"
[[ -n "$variant_json" ]] || variant_json='{}'

canonical="$(printf '{"variant":%s}' "$variant_json" \
  | jq -cS '
    if (.variant | type) != "object"
    then error("variant must be a JSON object")
    else .
    end
    | if (.variant | has("modules"))
      then .variant.modules |=
        (if type == "string" then
           split(" ") | map(select(length > 0)) | sort
         elif type == "array" then
           sort
         else
           .
         end)
      else .
      end
  ')"

# openssl is the one hasher present on the Linux, macOS and Windows
# (Git Bash) runner images alike.
digest="$(printf '%s' "$canonical" \
  | openssl dgst -sha256 | awk '{print $NF}' | cut -c1-16)"

prefix="ddtg-cx1-${FAMILY}-${digest}"
printf 'prefix=%s\n' "$prefix" >> "$GITHUB_OUTPUT"
printf 'Computed cache key prefix: %s\n' "$prefix"
