// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc reports whether the current binary was instrumented at compile
// time by otelc, the OpenTelemetry Go compile-time instrumentation tool
// (https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation).
//
// It is the otelc counterpart of internal/orchestrion's Enabled, and exists for
// the same reason: code that only makes sense in an auto-instrumented build has
// to be able to tell, at runtime, whether the weaving actually happened.
package otelc

// enabled is flipped to true at build time by the assign_value rule in
// otelc.yaml. Orchestrion does the equivalent through its //orchestrion:enabled
// directive; otelc has no directive mechanism for this, so the rule matches the
// variable by name instead. Renaming it silently disables every otelc build.
var enabled = false

// Enabled reports whether the current build was compiled with otelc.
func Enabled() bool {
	return enabled
}
