#!/usr/bin/env bash
# shellcheck shell=bash

# select-orchestrion-suites.sh — narrow the orchestrion lane's package list.
#
# Stdin: the full package list, one Go package per line (`go list ./...`
# minus the /civisibility packages).
# Environment: SUITES — the classifier's selection, space-separated suite
# names or the tokens ALL / NONE.
# Stdout: two lines. Line 1 is the gotestsum package list; line 2 is the
# civisibility target ("./civisibility", or empty when the civisibility
# suite is not selected).
#
# A narrowed run FILTERS the platform-aware input list instead of naming
# packages: go list skips build-constraint-excluded directories (mongo and
# mongo.v2 are linux-only under -tags=githubci), while an explicit ./mongo
# path fails on macOS and Windows. The _integration root package and
# everything under _integration/internal/ always run. civisibility_failnow*
# and civisibilitytest never appear in the gotestsum list (they fail on
# purpose and must not mark real failures in CI App).

set -uo pipefail

full=$(cat)

# The classifier output can carry stray whitespace around the tokens; trim
# it or the exact comparisons below miss.
SUITES="${SUITES:-}"
SUITES="${SUITES#"${SUITES%%[![:space:]]*}"}"
SUITES="${SUITES%"${SUITES##*[![:space:]]}"}"

case "${SUITES}" in
  "" | "NONE" | "ALL")
    # Full lane: pass the platform-aware list through, joined on single
    # spaces like every narrowed output below.
    for pkg in ${full}; do
      packages+="${packages:+ }${pkg}"
    done
    echo "${packages}"
    echo "./civisibility"
    exit 0
    ;;
esac

packages=""
civisibility=""
# Whether any selected suite matched a package; the module's own packages
# do not count, or the fallback below could never fire.
matched=false
for suite in ${SUITES}; do
  if [[ "${suite}" == "civisibility" ]]; then
    civisibility="./civisibility"
  fi
done

for pkg in ${full}; do
  last="${pkg##*/}"
  # The civisibility suite runs separately; grep -v /civisibility already
  # excluded it (and civisibility_failnow*) from the input.
  if [[ "${last}" == "civisibility" ]]; then
    continue
  fi
  case "${pkg}" in
    */_integration | */_integration/internal/*)
      # The module's own packages always run: the root package holds
      # TestOrchestrionPresent, and internal/ holds the shared helpers.
      packages+="${packages:+ }${pkg}"
      continue
      ;;
  esac
  for suite in ${SUITES}; do
    # Shell equality keeps '.' literal, so chi.v5 matches only chi.v5.
    if [[ "${last}" == "${suite}" ]]; then
      packages+="${packages:+ }${pkg}"
      matched=true
      break
    fi
  done
done

if [[ "${matched}" == "false" && -z "${civisibility}" ]]; then
  # The selection matched nothing (a civisibility_failnow-only selection,
  # for example -- grep -v /civisibility excludes those suites). A no-op
  # run would go green without testing anything; fail open instead.
  echo "::warning::No selected suite matched a package;" \
    "running the full lane" >&2
  packages=""
  for pkg in ${full}; do
    packages+="${packages:+ }${pkg}"
  done
  echo "${packages}"
  echo "./civisibility"
  exit 0
fi

echo "${packages}"
echo "${civisibility}"
