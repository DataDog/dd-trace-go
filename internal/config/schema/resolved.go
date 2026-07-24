// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package schema

import "github.com/DataDog/dd-trace-go/v2/internal/telemetry"

// Origin identifies the source of a configuration value.
type Origin = telemetry.Origin

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
}
