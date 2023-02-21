#!/usr/bin/env bash
set -x

export DD_TRACE_AGENT_URL="unix:///var/run/datadog-agent"

cd ./profiler/internal/apps/unit-of-work && TestUnitOfWork=true go test -v
