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

func goodNonConstantFormat(format string, a any) {
	// Non-constant formats are constantlogmsg's problem, not this analyzer's.
	internallog.Debug(format, a)
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

// ── Suggestion: allowed, but flagged as a style nudge ───────────────────────

func suggestRawErrorAtEnd(err *customError) {
	internallog.Debug("operation failed: %v", err) // want "prefer err.Error"
}
