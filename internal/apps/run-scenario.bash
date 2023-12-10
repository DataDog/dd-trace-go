#!/usr/bin/env bash
set -eux

# Without this we get errors due to the replace directive in go.mod always
# pointing to the latest dd-trace-go. Maybe there is a better way to do this?
go mod tidy

# Run the selected test scenario. Timeout is 2h as test apps are not expected to
# run for more than 1h under normal circumstances.
go test -timeout 2h -v -run "TestScenario/${1}"'$'