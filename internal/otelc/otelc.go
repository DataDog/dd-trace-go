// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc provides a way to detect, at runtime, whether the current
// binary was instrumented at compile-time by otelc
// (https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation).
package otelc

// enabled is flipped to true at compile time by the assign_value rule in
// otelc.yaml when the binary is built via the otelc toolchain.
var enabled = false

// Enabled returns whether the current build was compiled with otelc or not.
func Enabled() bool {
	return enabled
}
