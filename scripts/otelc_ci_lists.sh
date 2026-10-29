#!/usr/bin/env bash
# Unless explicitly stated otherwise all files in this repository are licensed
# under the Apache License Version 2.0.
# This product includes software developed at Datadog (https://www.datadoghq.com/).
# Copyright 2026-present Datadog, Inc.

# Prints the lists that .github/workflows/otelc.yml reads, as lines for
# GITHUB_ENV:
#
#   OTELC_MODULES: otelc/all, then every contrib with an otelc module and that
#   module, found from the tracked contrib/**/otelc/go.mod files.
#   OTELC_TEST_PACKAGES: the suites under internal/orchestrion/_integration
#   that hold a .otelc file.
#
# Only tracked files count, so the result is the same on every runner.

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

fail() {
  echo "otelc_ci_lists: $*" >&2
  exit 1
}

modules="otelc/all"
while IFS= read -r gomod; do
  otelc_dir="$(dirname "${gomod}")"
  contrib_dir="$(dirname "${otelc_dir}")"
  [[ -n "$(git ls-files -- "${contrib_dir}/go.mod")" ]] ||
    fail "${otelc_dir} has no contrib module in ${contrib_dir}"
  modules+=" ${contrib_dir} ${otelc_dir}"
done < <(git ls-files -- ':(glob)contrib/**/otelc/go.mod' | sort)
[[ "${modules}" != "otelc/all" ]] || fail "no contrib/**/otelc/go.mod found"

suites=""
while IFS= read -r marker; do
  dir="$(dirname "${marker}")"
  [[ -n "$(git ls-files -- ':(glob)'"${dir}"'/*.go')" ]] ||
    fail "${dir} has a .otelc file but no Go files"
  suites+=" ${dir#internal/orchestrion/_integration/}"
done < <(git ls-files -- ':(glob)internal/orchestrion/_integration/*/.otelc' | sort)
[[ -n "${suites}" ]] || fail "no internal/orchestrion/_integration/*/.otelc found"

echo "OTELC_MODULES=${modules}"
echo "OTELC_TEST_PACKAGES=${suites# }"
