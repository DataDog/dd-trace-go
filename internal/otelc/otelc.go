// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc reports whether the current binary was instrumented at compile
// time by otelc, the OpenTelemetry Go compile-time instrumentation tool
// (https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation).
//
// It is the otelc counterpart of internal/orchestrion's Enabled.
package otelc

// enabled is flipped to true at build time by the assign_value rule in
// ddtrace/tracer/otelc.yaml. The rule matches this variable by name, so renaming
// it silently disables every otelc build.
var enabled = false

// Enabled reports whether the current build was compiled with otelc.
func Enabled() bool {
	return enabled
}
