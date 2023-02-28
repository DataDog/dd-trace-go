#!/usr/bin/bash
set -eux

# run unit of work, also activate execution tracing to run for every profile.
DD_TEST_APPS_ENABLED=true \
	DD_PROFILING_EXECUTION_TRACE_ENABLED=true \ 
	DD_PROFILING_EXECUTION_TRACE_PERIOD=0s \
	go test -v
