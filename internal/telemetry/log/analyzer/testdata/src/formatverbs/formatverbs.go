// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package formatverbs contains test cases for the logformatverbs analyzer.
package formatverbs

import (
	internallog "example.com/fakelog"
)

type customError struct{ msg string }

func (e *customError) Error() string { return e.msg }

// ── Good: allowed %v/%+v/%#v usage ───────────────────────────────────────────

func goodNoVerb(name string) {
	internallog.Error("operation failed: %s", name)
}

func goodErrorDotError(err *customError) {
	internallog.Error("operation failed: %s", err.Error())
}

func goodErrorDotErrorTrailingText(err *customError) {
	internallog.Warn("failed with %v\n", err.Error())
}

func goodErrorDotErrorWithEscapedPercent(err *customError) {
	// A literal %% after the sole, final %v must not be misread as a second verb.
	internallog.Warn("failed with %v (100%% complete)", err.Error())
}

func goodIndexedErrorDotErrorSoleVerb(err *customError) {
	// %[1]v explicitly selecting the sole argument is exactly equivalent to
	// a plain %v here — still the sole, final %v-family verb, still backed
	// by err.Error(), still exempt.
	internallog.Warn("failed: %[1]v", err.Error())
}

func goodNonConstantFormatOnConstantLogMsgCoveredFunc(format string, a any) {
	// Error and Warn are constantlogmsg's problem specifically (its
	// DefaultFuncs covers exactly those two internalLogPkg entries) — this
	// analyzer must not also flag them, or every violation would produce two
	// diagnostics.
	internallog.Warn(format, a)
}

// ── Bad: non-constant format on a function constantlogmsg doesn't cover ─────

// badNonConstantFormatDebug is the regression case for a real coverage gap:
// constantlogmsg's DefaultFuncs only enforces a constant format for
// internalLogPkg's Error and Warn, not Debug or Info — this restores the
// check the retired internalLogVariableFormat ruleguard rule had for all
// four.
func badNonConstantFormatDebug(format string, a any) {
	internallog.Debug(format, a) // want "format argument must be a compile-time constant string"
}

func badNonConstantFormatInfo(format string, a any) {
	internallog.Info(format, a) // want "format argument must be a compile-time constant string"
}

// ── Bad: forbidden %v/%+v/%#v usage ──────────────────────────────────────────

func badNonErrorAtEnd(name string) {
	internallog.Error("value: %v", name) // want "exposes uncontrolled data"
}

func badVerbNotAtEnd(err *customError) {
	internallog.Error("error %v at line %d", err, 123) // want "must be the last format verb"
}

func badPlusVNotAtEnd(v any) {
	internallog.Info("value %+v suffix %s", v, "x") // want "must be the last format verb"
}

func badNonErrorAtEndWithEscapedPercent(name string) {
	// A literal %% after %v must not hide that %v is a non-error final verb.
	internallog.Error("value: %v (100%% complete)", name) // want "exposes uncontrolled data"
}

func badWidthFormNonError(name string) {
	// A width modifier (%-10v) must not hide the verb from detection.
	internallog.Error("value: %-10v", name) // want "exposes uncontrolled data"
}

func badMultipleVerbsWithErrorLast(cfg any, err *customError) {
	// err.Error() as the last arg no longer blanket-exempts the whole call:
	// the earlier %v (over cfg) is a non-final verb, always forbidden by
	// position regardless of type — reported as such, not as the milder
	// "exposes uncontrolled data" (which would describe cfg's own type, not
	// the position violation) or the final-verb "prefer err.Error()"
	// suggestion (which wouldn't address the earlier verb at all).
	internallog.Warn("defaulting to %v; error: %v", cfg, err.Error()) // want "must be the last format verb"
}

func badMultipleVerbsFinalOneIsRawError(cfg any, err *customError) {
	// The final verb is safe (%v over a raw error, normally just a style
	// suggestion) but an earlier %v over cfg is still a forbidden non-final
	// verb — that must be reported instead of the "prefer err.Error()"
	// suggestion, which only addresses the final verb and would leave this
	// call re-flagged (with a different message) on the very next run.
	internallog.Warn("config: %v; error: %v", cfg, err) // want "must be the last format verb"
}

func badIndexedVerbBypassingNarrowExemption(cfg any, err *customError) {
	// %[1]v explicitly selects the first argument (cfg) — a %v-family verb
	// just like a plain %v, not a stray '[' to be ignored. Miscounting it
	// would leave vFamilyCount at 1 (only the second, plain %v counted),
	// wrongly satisfying the narrowed err.Error() exemption and hiding the
	// reflection-unsafe %[1]v over cfg entirely.
	internallog.Warn("config %[1]v; error %v", cfg, err.Error()) // want "must be the last format verb"
}

func badSingleVerbNotFinalWithErrorLast(cfg any, err *customError) {
	// Exactly one %v-family verb, but it isn't the final verb — still reported
	// even though the last argument happens to be err.Error().
	internallog.Warn("value %v; msg: %s", cfg, err.Error()) // want "must be the last format verb"
}

// badIndexedVerbResolvesToNonLastArgument is the regression case for a real
// false negative: an explicit index can put the (only, textually final)
// %v-family verb on an argument other than the call's last one. %[2]s
// consumes argument 2 (err.Error()) and %[1]v consumes argument 1 (cfg) —
// checking the literal last call argument for the err.Error() exemption
// looks at the wrong operand entirely and would wrongly exempt this call.
func badIndexedVerbResolvesToNonLastArgument(cfg any, err *customError) {
	internallog.Warn("%[2]s %[1]v", cfg, err.Error()) // want "exposes uncontrolled data"
}

// goodIndexedVerbResolvesToErrorDotError is the mirror case proving the fix
// doesn't over-correct: here the resolved argument for the sole, final
// %v-family verb genuinely is err.Error(), even though the call's last
// argument positionally (cfg, via %[2]s) is not.
func goodIndexedVerbResolvesToErrorDotError(cfg any, err *customError) {
	internallog.Warn("cfg=%[2]s %[1]v", err.Error(), cfg)
}

// ── Suggestion: allowed, but flagged as a style nudge ───────────────────────

func suggestRawErrorAtEnd(err *customError) {
	internallog.Debug("operation failed: %v", err) // want "prefer err.Error"
}
