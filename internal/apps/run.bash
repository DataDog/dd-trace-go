#!/usr/bin/env bash
set -eux

# Without this we get errors due to the replace directive in go.mod always
# pointing to the latest dd-trace-go. Maybe there is a better way to do this?
go mod tidy

# Run internal test apps and also enable non-stop execution tracing.
DD_TEST_APPS_ENABLED=true \
	DD_PROFILING_EXECUTION_TRACE_PERIOD="${DD_PROFILING_EXECUTION_TRACE_PERIOD:-1s}" \
	go test -timeout 12h -v ./
