#!/bin/sh
go run github.com/tailscale/depaware --update gopkg.in/DataDog/dd-trace-go.v1/profiler
go run github.com/tailscale/depaware --update gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer
env GOFLAGS='-tags=appsec' go run github.com/tailscale/depaware --update gopkg.in/DataDog/dd-trace-go.v1/appsec
