#!/usr/bin/env bash

## Start these now so they'll be ready by the time we run integration tests.
docker-compose up -d

## CORE
echo testing core
PACKAGE_NAMES=$(go list ./... | grep -v /contrib/)
gotestsum --junitfile ./gotestsum-report.xml -- $PACKAGE_NAMES -v  -coverprofile=coverage.txt -covermode=atomic

## wait extra long for all the docker containers to be "ready"
echo Waiting for docker for 120 seconds
sleep 120

## CONTRIB
echo testing contrib
PACKAGE_NAMES=$(go list ./contrib/... | grep -v -e grpc.v12 -e google.golang.org/api)
export INTEGRATION=true
gotestsum --junitfile ./gotestsum-report.xml -- $PACKAGE_NAMES -v  -coverprofile=coverage.txt -covermode=atomic 
docker-compose down

