#!/usr/bin/env bash
set -eux

# escape_test_name escapes the test name so that it doesn't match other tests
# when passed to go test -run.
escape_test_name() {
    sed 's/\//$\/^/g' <<< "^${1}\$"
}

# Without this we get errors due to the replace directive in go.mod always
# pointing to the latest dd-trace-go. Maybe there is a better way to do this?
go mod tidy

# Run the selected test scenario. Timeout is 2h as test apps are not expected to
# run for more than 1h under normal circumstances.
go test -timeout 2h -v -run "TestScenario/$(escape_test_name "${1}")"