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

func badShadowedNilIsNotExempt() {
	// Shadows the predeclared nil identifier — Go permits this. The value is
	// not the nil literal and must still be checked like any other type.
	nil := plainStruct{Field: "shadowed value must still be flagged"}
	telemetrylog.Error("event", slog.Any("data", nil)) // want "does not implement slog.LogValuer"
}

// ── Good: hoisted attrs that are safe or out of scope ───────────────────────

func goodHoistedSafeError(err error) {
	errAttr := slog.Any("error", telemetrylog.NewSafeError(err))
	telemetrylog.Error("operation failed", errAttr)
}

func goodHoistedNolint() {
	// The directive belongs on the slog.Any line: that's where the diagnostic
	// and the fix are, not on the log call below.
	dataAttr := slog.Any("data", plainStruct{Field: "x"}) //nolint:telemetrysafety
	telemetrylog.Debug("event", dataAttr)
}

func goodHoistedAttrNeverLogged() {
	// Scope check: attrs are only inspected once they reach a telemetry log
	// call. This one never does.
	dataAttr := slog.Any("data", plainStruct{Field: "x"})
	_ = dataAttr
}

func goodAttrFromParamNotResolved(attr slog.Attr) {
	// Limitation: built by the caller, no assignment in this package to
	// inspect. Must not be flagged and must not crash.
	telemetrylog.Debug("event", attr)
}

func unsafeAttrHelper() slog.Attr { return slog.Any("data", plainStruct{Field: "x"}) }

func goodAttrFromHelperNotResolved() {
	// Limitation: the RHS is a call to a helper, not a slog constructor, so
	// the returned attr is not followed.
	attr := unsafeAttrHelper()
	telemetrylog.Debug("event", attr)
}

// ── Bad: hoisted attrs that are unsafe ──────────────────────────────────────

func badHoistedRawError(err error) {
	errAttr := slog.Any("error", err) // want "raw error value"
	telemetrylog.Error("operation failed", errAttr)
}

func badHoistedVarSpec() {
	var dataAttr = slog.Any("data", plainStruct{Field: "x"}) // want "does not implement slog.LogValuer"
	telemetrylog.Debug("event", dataAttr)
}

func badHoistedStringWithErrorCall(err error) {
	msgAttr := slog.String("error", err.Error()) // want "slog.String with err.Error"
	telemetrylog.Warn("failed", msgAttr)
}

// badHoistedBranches mirrors the openfeature panic-recovery pattern that
// motivated following identifiers: every assignment is checked, so the
// unsafe branch is flagged even though the other branch is safe.
func badHoistedBranches(r any) {
	var errAttr slog.Attr
	if err, ok := r.(error); ok {
		errAttr = slog.Any("panic", telemetrylog.NewSafeError(err))
	} else {
		errAttr = slog.Any("panic", r) // want "does not implement slog.LogValuer"
	}
	telemetrylog.Error("recovered panic", errAttr)
}

// badHoistedReusedAttr guards the dedupe: one unsafe assignment reaching two
// log calls must produce exactly one diagnostic, not two.
func badHoistedReusedAttr() {
	dataAttr := slog.Any("data", plainStruct{Field: "x"}) // want "does not implement slog.LogValuer"
	telemetrylog.Debug("first event", dataAttr)
	telemetrylog.Warn("second event", dataAttr)
}

func badHoistedViaLoggerMethod(err error) {
	logger := telemetrylog.With()
	errAttr := slog.Any("error", err) // want "raw error value"
	logger.Error("operation failed", errAttr)
}
