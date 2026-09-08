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

// pointerLogValuer implements slog.LogValuer only on the pointer receiver —
// slog.Any boxes whatever type is passed as-is, so a non-pointer value of
// this type is unreachable via the pointer method and must still be flagged.
type pointerLogValuer struct{ Field string }

func (p *pointerLogValuer) LogValue() slog.Value { return slog.StringValue(p.Field) }

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

func goodPointerReceiverLogValuerPassedByPointer() {
	v := pointerLogValuer{Field: "safe"}
	telemetrylog.Debug("event", slog.Any("data", &v))
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

func badPointerReceiverLogValuerPassedByValue() {
	v := pointerLogValuer{Field: "unsafe"}
	telemetrylog.Debug("event", slog.Any("data", v)) // want "does not implement slog.LogValuer"
}

func badShadowedNilIsNotExempt() {
	// Shadows the predeclared nil identifier — Go permits this. The value is
	// not the nil literal and must still be checked like any other type.
	nil := plainStruct{Field: "shadowed value must still be flagged"}
	telemetrylog.Error("event", slog.Any("data", nil)) // want "does not implement slog.LogValuer"
}

func badRawErrorViaAnyParenthesized(err error) {
	// Parentheses make the argument an *ast.ParenExpr, not a call — the attr
	// must still be unwrapped and checked.
	telemetrylog.Error("operation failed", (slog.Any("error", err))) // want "raw error value"
}

func badRawErrorInsideGroup(err error) {
	// Group children resolve to log/slog, so the telemetry-call filter never
	// reaches them — they must be descended into explicitly.
	telemetrylog.Error("operation failed", slog.Group("ctx", slog.Any("error", err))) // want "raw error value"
}

func badRawErrorInsideNestedGroup(err error) {
	telemetrylog.Error("operation failed", slog.Group("a", slog.Group("b", slog.Any("error", err)))) // want "raw error value"
}

func badRawErrorInsideGroupParenthesized(err error) {
	telemetrylog.Error("operation failed", slog.Group("ctx", (slog.Any("error", err)))) // want "raw error value"
}

func badStringWithErrorCallInsideGroup(err error) {
	telemetrylog.Warn("failed", slog.Group("ctx", slog.String("error", err.Error()))) // want "slog.String with err.Error"
}

func goodSafeErrorInsideGroup(err error) {
	telemetrylog.Debug("event", slog.Group("ctx", slog.Any("error", telemetrylog.NewSafeError(err))))
}

func goodGroupOfSafeAttrs() {
	telemetrylog.Debug("event", slog.Group("ctx", slog.String("op", "startup"), slog.Int("n", 1)))
}

// ── nolint suppression across multi-line calls ───────────────────────────────

func nolintAboveMultiLineCall(err error) {
	//nolint:telemetrysafety
	telemetrylog.Error("failed",
		slog.Any("err", err))
}

func nolintInsideMultiLineCall(err error) {
	telemetrylog.Error("failed",
		//nolint:telemetrysafety
		slog.Any("err", err))
}

func nolintTrailingOnUnrelatedLineDoesNotCarryOver(err error) {
	x := err //nolint:gocritic // this exception belongs to this line, not the call below
	_ = x
	telemetrylog.Error("failed",
		slog.Any("err", err)) // want "raw error value"
}
