// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package schema defines dependency-leaf metadata shared by configuration
// registration and resolution.
package schema

// SourcePolicy selects the sources used to resolve a raw configuration key.
type SourcePolicy uint8

const (
	// SourceEnvironment resolves only the environment key named by a binding.
	SourceEnvironment SourcePolicy = iota
	// SourceStable resolves stable configuration, environment, and defaults.
	SourceStable
)

// TelemetryPolicy controls how a raw configuration value may be reported.
type TelemetryPolicy uint8

const (
	// TelemetryReport reports the value unchanged.
	TelemetryReport TelemetryPolicy = iota
	// TelemetryRedact reports a redacted value.
	TelemetryRedact
	// TelemetrySanitizeURL reports a URL after removing credentials.
	TelemetrySanitizeURL
	// TelemetryOmit does not report the value.
	TelemetryOmit
)

// SamplingBoundary identifies when a consumer resolves a binding.
type SamplingBoundary uint8

const (
	// SamplePackageInit resolves during package initialization.
	SamplePackageInit SamplingBoundary = iota
	// SampleTracerConstruction resolves for each tracer construction.
	SampleTracerConstruction
	// SampleProductStart resolves for each product start.
	SampleProductStart
	// SampleConstructor resolves for each consumer constructor.
	SampleConstructor
	// SampleFirstUse resolves once when the consumer is first used.
	SampleFirstUse
	// SamplePerCall resolves for every consumer call.
	SamplePerCall
)

// RawDefinition records the properties shared by every read of one source key.
type RawDefinition struct {
	Key       string
	Sources   SourcePolicy
	Telemetry TelemetryPolicy
}

// ConsumerBinding records how and when a consumer interprets raw definitions.
type ConsumerBinding struct {
	ID       string
	Consumer string
	Keys     []string
	Sampling SamplingBoundary
}
