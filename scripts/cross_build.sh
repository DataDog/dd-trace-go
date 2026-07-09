#!/usr/bin/env bash
# Cross-compile dd-trace-go for every first-class Go port to catch
# architecture-specific compile regressions (e.g. 32-bit size assertions,
# issue #4984). Runs on a normal amd64 runner — portability is a compile-time
# property, so no native 32-bit machine is needed.
#
# CGO is disabled: this is a pure-Go portability smoke test. cgo paths are
# covered by the native multi-OS test matrix (multios-unit-tests.yml).
#
# Packages importing go-libddwaf are skipped: it does not yet build on 32-bit
# ports (upstream fix: DataDog/go-libddwaf#227). They are still
# built and tested on 64-bit by the native matrix. Test-only packages are
# skipped (nothing to build, and they error when passed explicitly to go build).
set -euo pipefail

# First-class ports: https://go.dev/wiki/PortingPolicy
platforms=(
  linux/386 linux/amd64 linux/arm linux/arm64
  darwin/amd64 darwin/arm64
  windows/386 windows/amd64
)

mapfile -t pkgs < <(
  go list -f '{{.ImportPath}}|{{if .GoFiles}}Y{{end}}|{{join .Deps " "}}' ./... |
    awk -F'|' '$2 == "Y" && $3 !~ /go-libddwaf/ { print $1 }'
)
echo "Cross-compiling ${#pkgs[@]} package(s) across ${#platforms[@]} port(s)"

rc=0
for p in "${platforms[@]}"; do
  if GOOS="${p%/*}" GOARCH="${p#*/}" CGO_ENABLED=0 go build "${pkgs[@]}"; then
    echo "ok   $p"
  else
    echo "FAIL $p"
    rc=1
  fi
done
exit "$rc"
