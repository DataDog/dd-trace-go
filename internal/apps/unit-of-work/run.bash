#!/usr/bin/env bash
set -eux

# Without this we get errors due to the replace directive in go.mod always
# pointing to the latest dd-trace-go. Maybe there is a better way to do this?
go mod tidy

# run unit of work, also activate execution tracing to run for every profile.
DD_TEST_APPS_ENABLED=true \
	DD_PROFILING_EXECUTION_TRACE_ENABLED="${DD_PROFILING_EXECUTION_TRACE_ENABLED:-true}" \
	DD_PROFILING_EXECUTION_TRACE_PERIOD="${DD_PROFILING_EXECUTION_TRACE_PERIOD:-1s}" \
	go test -timeout 12h -v
