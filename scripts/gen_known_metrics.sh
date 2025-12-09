#!/bin/bash

# This script is used to update the json files under ./internal/telemetry/internal/knownmetrics.
# It requires proper Github access to download these files.

go run github.com/DataDog/dd-trace-go/v2/internal/telemetry/internal/knownmetrics/generator
