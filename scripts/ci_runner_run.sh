#!/bin/bash
set -euo pipefail

usage() {
  cat << EOF
Usage: $(basename "${BASH_SOURCE[0]}") [-e KEY=VALUE ...] -- IMAGE COMMAND [ARG...]

Runs COMMAND inside IMAGE, the way make ci/core and make ci/contrib do. The script copies
the working tree into the container instead of bind-mounting it, then copies changed files
back to the host when COMMAND exits.

A bind mount is not used because Docker Desktop's bind-mount filesystem on macOS does not
enforce Unix file permissions. A permission-enforcing test then fails for the wrong reason.
For example, a 0000-permission file stays readable through a bind mount, but not through a
container-native path.

The container receives a copy of .git, because some tests read git metadata from the
working tree. The script deletes that copy right before the container exits. The final
copy-out therefore never overwrites the host's live .git with a stale snapshot from a long
run.

The script reads CI_RUNNER_PLATFORM, CI_RUNNER_GOMODCACHE, CI_RUNNER_GOCACHE,
CI_TEST_RESULTS, and BUILD_TAGS from the environment.
EOF
  exit 0
}

EXTRA_ENV=()
while [[ "${1:-}" != "--" ]]; do
  case "${1:-}" in
    -e)
      EXTRA_ENV+=(-e "$2")
      shift 2
      ;;
    -h | --help)
      usage
      ;;
    *)
      echo "unexpected argument: ${1:-<none>}, expected -e KEY=VALUE or --" >&2
      exit 1
      ;;
  esac
done
shift # drop the --

IMAGE="$1"
shift

# Non-root user for permission-enforcing tests
CID=$(docker create --platform "$CI_RUNNER_PLATFORM" --network host \
  -v "$CI_RUNNER_GOMODCACHE:/go/pkg/mod" \
  -v "$CI_RUNNER_GOCACHE:/go-build" \
  -v "$CI_TEST_RESULTS:/tmp/test-results" \
  -e INTEGRATION=true -e GOTOOLCHAIN=local -e GODEBUG=x509negativeserial=1 \
  -e TEST_RESULTS=/tmp/test-results -e "BUILD_TAGS=$BUILD_TAGS" \
  "${EXTRA_ENV[@]+"${EXTRA_ENV[@]}"}" \
  "$IMAGE" sh -c '
    chown -R 1000:1000 /workspace &&
    setpriv --reuid=1000 --regid=1000 --init-groups -- "$@"
    status=$?
    rm -rf /workspace/.git /workspace/bin
    exit "$status"
  ' -- "$@")

cleanup() { docker rm -f "$CID" > /dev/null 2>&1 || true; }
trap cleanup EXIT

docker cp . "$CID":/workspace
docker start -a "$CID"
STATUS=$(docker inspect "$CID" --format '{{.State.ExitCode}}')

docker cp "$CID":/workspace/. .

exit "$STATUS"
