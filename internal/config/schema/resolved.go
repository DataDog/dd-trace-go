// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package schema

import "github.com/DataDog/dd-trace-go/v2/internal/telemetry"

// Origin identifies the source of a configuration value.
type Origin = telemetry.Origin

// EventKind identifies either a configuration value state or a provider
// compatibility diagnostic.
type EventKind uint8

const (
	// EventConfiguration describes one source attempt or the hard-coded default.
	EventConfiguration EventKind = iota
	// EventOTelEnvHiding records that an OTel variable has a Datadog equivalent.
	EventOTelEnvHiding
	// EventOTelEnvInvalid records an invalid mapped OTel value.
	EventOTelEnvInvalid
)

// ReportCadence controls deduplication by the configuration reporter.
type ReportCadence uint8

const (
	// ReportNever suppresses an event.
	ReportNever ReportCadence = iota
	// ReportOncePerGeneration reports an event once for a resolved generation.
	ReportOncePerGeneration
	// ReportOnChange reports only when the transformed state changes.
	ReportOnChange
)

// ConfigEvent is a local description of configuration state or a provider
// diagnostic. Resolution never submits it to configuration telemetry.
type ConfigEvent struct {
	Kind        EventKind
	BindingID   string
	Name        string
	Value       any
	Present     bool
	Valid       bool
	Err         error
	Origin      Origin
	ConfigID    string
	Policy      TelemetryPolicy
	Cadence     ReportCadence
	ReportValue bool
	// CompatibilityReport preserves whether transitional getters emitted a
	// provider diagnostic before the bounded reporter existed.
	CompatibilityReport bool
	OTelName            string
}

// SourceAttempt records one attempted configuration source.
type SourceAttempt struct {
	Raw      string
	Present  bool
	Valid    bool
	Err      error
	Origin   Origin
	ConfigID string
}

// Winner records the value selected by configuration resolution.
type Winner[T any] struct {
	Value       T
	Origin      Origin
	ConfigID    string
	DefaultUsed bool
}

// Resolved records a winning value and every source that was attempted.
type Resolved[T any] struct {
	Winner   Winner[T]
	Attempts []SourceAttempt
	Events   []ConfigEvent
}
