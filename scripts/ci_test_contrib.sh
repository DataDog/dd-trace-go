#!/bin/bash

set +e

# This script is used to test the contrib submodules in the apps directory.
# It is run by the GitHub Actions CI workflow defined in
# .github/workflows/unit-integration-tests.yml.

[[ -d ./contrib ]] || exit 0

BUILD_TAGS="${BUILD_TAGS:-}"

if [ $# -eq 2 ]; then
  CONTRIBS="$2"
  INSTRUMENTATION_SUBMODULES=""
else
  CONTRIBS=$(find ./contrib -mindepth 2 -type f -name go.mod -exec dirname {} \;)
  INSTRUMENTATION_SUBMODULES=$(find ./instrumentation -mindepth 2 -type f -name go.mod -exec dirname {} \;)
fi

export GOEXPERIMENT=synctest # TODO: remove once go1.25 is the minimum supported version

export DD_APPSEC_ENABLED=1
export DD_APPSEC_WAF_TIMEOUT=1m

report_error=0

# Build the tags argument if BUILD_TAGS is set
TAGS_ARG="-tags="
if [[ -n "$BUILD_TAGS" ]]; then
  TAGS_ARG="-tags=$BUILD_TAGS"
  echo "Running contrib tests with build tags: $BUILD_TAGS"
else
  echo "Running standard contrib tests"
fi

for contrib in $CONTRIBS; do
  echo "Testing contrib module: $contrib"
  contrib_id=$(echo "$contrib" | sed 's/^\.\///g;s/[\/\.]/_/g')
  cd "$contrib" || exit 1
  if [[ "$1" = "smoke" ]]; then
    go get -u -t ./...
  fi
  if [[ "$1" = "smoke" && "$contrib" = "./contrib/k8s.io/client-go/" ]]; then
    # This is a temporary workaround due to this issue in apimachinery: https://github.com/kubernetes/apimachinery/issues/190
    # When the issue is resolved, this line can be removed.
    go get k8s.io/kube-openapi@v0.0.0-20250628140032-d90c4fd18f59
    # Another temporary workaround caused by the upgrade introduced by this commit: https://github.com/kubernetes/client-go/commit/f4d210639bbc61f2f2a8596662d7ad50abaa6544
    go get k8s.io/client-go@v0.35.0
  fi
  if [[ "$1" = "smoke" && "$contrib" = "./contrib/gin-gonic/gin/" ]]; then
    # Temporary workaround, see: https://github.com/gin-gonic/gin/issues/4441
    go get github.com/quic-go/qpack@v0.5.1
  fi
  go mod tidy
  gotestsum --junitfile "${TEST_RESULTS}/gotestsum-report-$contrib_id.xml" -- ./... -v -race "$TAGS_ARG" -coverprofile="coverage-$contrib_id.txt" -covermode=atomic
  test_exit=$?
  [[ $test_exit -ne 0 ]] && report_error=1
  cd - > /dev/null || exit 1
done

for mod in $INSTRUMENTATION_SUBMODULES; do
  echo "Testing instrumentation submodule: $mod"
  mod_id=$(echo "$mod" | sed 's/^\.\///g;s/[\/\.]/_/g')
  cd "$mod" || exit 1
  [[ "$1" = "smoke" ]] && go get -u -t ./...
  if [[ "$1" = "smoke" && "$contrib" = "./contrib/k8s.io/client-go/" ]]; then
    # This is a temporary workaround due to this issue in apimachinery: https://github.com/kubernetes/apimachinery/issues/190
    # When the issue is resolved, this line can be removed.
    go get k8s.io/kube-openapi@v0.0.0-20250628140032-d90c4fd18f59
  fi
  gotestsum --junitfile "${TEST_RESULTS}/gotestsum-report-$mod_id.xml" -- ./... -v -race "$TAGS_ARG" -coverprofile="coverage-$mod_id.txt" -covermode=atomic
  test_exit=$?
  [[ $test_exit -ne 0 ]] && report_error=1
  cd - > /dev/null || exit 1
done

exit $report_error
