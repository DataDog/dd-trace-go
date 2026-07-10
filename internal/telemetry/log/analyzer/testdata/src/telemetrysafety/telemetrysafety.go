// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package telemetrysafety contains test cases for the telemetrysafety analyzer.
package telemetrysafety

import (
	"log/slog"

	telemetrylog "example.com/faketelemetrylog"
)

type plainStruct struct{ Field string }

// ── Good: safe slog.Any / slog.String usage ─────────────────────────────────

func goodSafeError(err error) {
	telemetrylog.Error("operation failed", slog.Any("error", telemetrylog.NewSafeError(err)))
}

func goodSafeErrorViaMethod(err error) {
	logger := telemetrylog.With()
	logger.Warn("operation warned", slog.Any("error", telemetrylog.NewSafeError(err)))
}

func goodNonErrorScalars() {
	telemetrylog.Debug("event", slog.String("operation", "startup"), slog.Int("count", 3))
}

func goodNilValue() {
	telemetrylog.Error("event with nil attr", slog.Any("cause", nil))
}

func goodStringNotFromError() {
	telemetrylog.Error("event", slog.String("key", "a plain constant value"))
}

// ── Bad: unsafe slog.Any / slog.String usage ────────────────────────────────

func badRawErrorViaAny(err error) {
	telemetrylog.Error("operation failed", slog.Any("error", err)) // want "raw error value"
}

func badRawErrorViaAnyMethod(err error) {
	logger := telemetrylog.With()
	logger.Error("operation failed", slog.Any("error", err)) // want "raw error value"
}

func badNonLogValuerStruct() {
	telemetrylog.Debug("event", slog.Any("data", plainStruct{Field: "x"})) // want "does not implement slog.LogValuer"
}

func badStringWithErrorCall(err error) {
	telemetrylog.Warn("failed", slog.String("error", err.Error())) // want "slog.String with err.Error"
}

func badStringWithErrorCallViaMethod(err error) {
	logger := telemetrylog.With()
	logger.Warn("failed", slog.String("error", err.Error())) // want "slog.String with err.Error"
}
