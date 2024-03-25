// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

// Package telemetry implements a client for sending telemetry information to
// Datadog regarding usage of an APM library such as tracing or profiling.
// This package is not intended for use by external consumers, no API stability is guaranteed.
package telemetry

import (
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

// MockGlobalClient replaces the global telemetry client with a custom
// implementation of TelemetryClient. It returns a function that can be deferred
// to reset the global telemetry client to its previous value.
// This function is not intended for use by external consumers, no API stability is guaranteed.
func MockGlobalClient(client telemetry.Client) func() {
	return telemetry.MockGlobalClient(client)
}
