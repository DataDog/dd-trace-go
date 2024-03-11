// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

// Package telemetry implements a client for sending telemetry information to
// Datadog regarding usage of an APM library such as tracing or profiling.
// This package is not intended for use by external consumers, no API stability is guaranteed.
package telemetry

import (
	"github.com/DataDog/dd-trace-go/v2/v1internal/telemetry"
)

// Disabled returns whether instrumentation telemetry is disabled
// according to the DD_INSTRUMENTATION_TELEMETRY_ENABLED env var
// This function is not intended for use by external consumers, no API stability is guaranteed.
func Disabled() bool {
	return telemetry.Disabled()
}
