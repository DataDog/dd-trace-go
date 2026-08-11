#!/usr/bin/env bash
# Unless explicitly stated otherwise all files in this repository are licensed
# under the Apache License Version 2.0.
# This product includes software developed at Datadog (https://www.datadoghq.com/).
# Copyright 2026-present Datadog, Inc.

# Builds internal/apps/otelc-external-app with otelc.
#
# Every other otelc suite here is an application under
# github.com/DataDog/dd-trace-go/v2/..., so none of them catch what only breaks
# outside it, such as otelc pulling an internal package into the file it
# generates in the application's main package. This app's module path is
# third-party.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_DIR="${REPO_ROOT}/internal/apps/otelc-external-app"

# Workspace mode resolves modules differently from the module graph otelc
# analyzes, and it hides the very thing this check looks for.
export GOWORK=off

echo "app:   ${APP_DIR}"
echo "otelc: $(otelc version 2>&1 | head -1)"

cd "${APP_DIR}"

# otelc leaves its build tree behind; a stale one makes the build plan wrong.
rm -rf .otelc-build

echo "==> otelc go build"
if ! otelc go build -o /dev/null .; then
  cat >&2 << 'EOF'

==> FAILED: an application outside this repository cannot be built with otelc.

Read the build output above for the cause. A common one is "use of internal
package ... not allowed", which means the otelc rules are read from a package
under internal/; move them to one any application can import.
EOF
  exit 1
fi

# A build that instruments nothing also succeeds, so without this the check
# would pass vacuously. matched.json is [] when matching ran and matched
# nothing, and null when it never got that far.
MATCHED=".otelc-build/matched.json"
if [[ ! -s "${MATCHED}" ]]; then
  echo "==> FAILED: ${MATCHED} is missing or empty, so nothing was instrumented" >&2
  exit 1
fi
CONTENT="$(tr -d '[:space:]' < "${MATCHED}")"
if [[ "${CONTENT}" == "null" || "${CONTENT}" == "[]" ]]; then
  echo "==> FAILED: the build succeeded but instrumented nothing:" >&2
  cat "${MATCHED}" >&2
  exit 1
fi

# Best effort, and only when otelc left its debug dump behind: the generated
# file in the application's main package must not name an internal package.
if [[ -d ".otelc-build/debug/main" ]]; then
  if grep -rn 'github.com/DataDog/dd-trace-go/v2/internal/' ".otelc-build/debug/main" >&2; then
    echo "==> FAILED: the generated main-package file imports an internal package" >&2
    exit 1
  fi
fi

rm -rf .otelc-build

echo "==> OK: external application builds with otelc and is instrumented"
