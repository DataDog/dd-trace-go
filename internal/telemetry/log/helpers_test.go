// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internallog "github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

func TestReportError_BasicFlow(t *testing.T) {
	var captured telemetry.Record

	orig := sendLog
	defer func() { sendLog = orig }()
	sendLog = func(r telemetry.Record, _ ...telemetry.LogOption) { captured = r }

	ReportError("sdk error: initialization failed", errors.New("sensitive detail"))

	assert.Equal(t, "sdk error: initialization failed", captured.Message)
	assert.Equal(t, slog.LevelError, captured.Level)

	// Error attribute must exist but must not leak the message.
	found := false
	captured.Attrs(func(a slog.Attr) bool {
		if a.Key == "error" {
			found = true
			assert.NotContains(t, a.Value.String(), "sensitive detail")
		}
		return true
	})
	assert.True(t, found, "error attribute must be present")
}

func TestReportError_NilError(t *testing.T) {
	var captured telemetry.Record

	orig := sendLog
	defer func() { sendLog = orig }()
	sendLog = func(r telemetry.Record, _ ...telemetry.LogOption) { captured = r }

	ReportError("sdk defect with no error", nil)

	assert.Equal(t, "sdk defect with no error", captured.Message)
	hasAttr := false
	captured.Attrs(func(_ slog.Attr) bool { hasAttr = true; return false })
	assert.False(t, hasAttr, "no attributes expected when error is nil")
}

func TestReportPanic_ErrorRecovered(t *testing.T) {
	var captured telemetry.Record

	orig := sendLog
	defer func() { sendLog = orig }()
	sendLog = func(r telemetry.Record, _ ...telemetry.LogOption) { captured = r }

	panicErr := errors.New("nil pointer deref in secret handler")
	ReportPanic("unexpected panic in goroutine", panicErr)

	assert.Equal(t, "unexpected panic in goroutine", captured.Message)
	assert.Equal(t, slog.LevelError, captured.Level)

	found := false
	captured.Attrs(func(a slog.Attr) bool {
		if a.Key == "error" {
			found = true
			assert.NotContains(t, a.Value.String(), "secret")
		}
		return true
	})
	assert.True(t, found)
}

func TestReportPanic_NonErrorRecovered(t *testing.T) {
	var captured telemetry.Record

	orig := sendLog
	defer func() { sendLog = orig }()
	sendLog = func(r telemetry.Record, _ ...telemetry.LogOption) { captured = r }

	ReportPanic("unexpected panic in goroutine", "a string panic value")

	assert.Equal(t, "unexpected panic in goroutine", captured.Message)
	// Non-error recovered values attach a type only, never their content.
	found := false
	captured.Attrs(func(a slog.Attr) bool {
		if a.Key == "recovered_type" {
			found = true
			assert.Equal(t, "string", a.Value.String())
		}
		return true
	})
	assert.True(t, found, "recovered_type attribute must be present")
}

func TestReportPanic_UnnamedTypeRecovered(t *testing.T) {
	var captured telemetry.Record

	orig := sendLog
	defer func() { sendLog = orig }()
	sendLog = func(r telemetry.Record, _ ...telemetry.LogOption) { captured = r }

	ReportPanic("unexpected panic in goroutine", map[string]string{"secret": "leak me not"})

	assert.Equal(t, "unexpected panic in goroutine", captured.Message)
	found := false
	captured.Attrs(func(a slog.Attr) bool {
		if a.Key == "recovered_type" {
			found = true
			assert.NotEmpty(t, a.Value.String())
			assert.NotContains(t, a.Value.String(), "leak me not")
		}
		return true
	})
	assert.True(t, found, "recovered_type attribute must be present")
}

func TestReportPanic_WithTagsOption(t *testing.T) {
	var capturedOpts []telemetry.LogOption

	orig := sendLog
	defer func() { sendLog = orig }()
	sendLog = func(_ telemetry.Record, opts ...telemetry.LogOption) { capturedOpts = opts }

	ReportPanic("unexpected panic in goroutine", "a string panic value")
	assert.Len(t, capturedOpts, 1, "WithStacktraceNow only, no extra opts passed")

	ReportPanic("unexpected panic in goroutine", "a string panic value", telemetry.WithTags([]string{"env:prod"}))
	assert.Len(t, capturedOpts, 2, "caller-supplied opts must reach sendLog alongside WithStacktraceNow")
}

func TestLogAndReportError_BasicFlow(t *testing.T) {
	var captured telemetry.Record

	origSend := sendLog
	defer func() { sendLog = origSend }()
	sendLog = func(r telemetry.Record, _ ...telemetry.LogOption) { captured = r }

	recorder := &internallog.RecordLogger{}
	undo := internallog.UseLogger(recorder)
	defer undo()

	LogAndReportError("sdk error: initialization failed", errors.New("sensitive detail"))
	internallog.Flush()

	// Telemetry side matches ReportError's existing behavior.
	assert.Equal(t, "sdk error: initialization failed", captured.Message)
	found := false
	captured.Attrs(func(a slog.Attr) bool {
		if a.Key == "error" {
			found = true
			assert.NotContains(t, a.Value.String(), "sensitive detail")
		}
		return true
	})
	assert.True(t, found, "error attribute must be present")

	// Local log side got the same constant message, with full error detail.
	logs := recorder.Logs()
	assert.Len(t, logs, 1)
	assert.Contains(t, logs[0], "sdk error: initialization failed: sensitive detail")
}

func TestLogAndReportError_NilError(t *testing.T) {
	var captured telemetry.Record

	origSend := sendLog
	defer func() { sendLog = origSend }()
	sendLog = func(r telemetry.Record, _ ...telemetry.LogOption) { captured = r }

	recorder := &internallog.RecordLogger{}
	undo := internallog.UseLogger(recorder)
	defer undo()

	LogAndReportError("sdk defect with no error", nil)
	internallog.Flush()

	assert.Equal(t, "sdk defect with no error", captured.Message)

	logs := recorder.Logs()
	assert.Len(t, logs, 1)
	// The format string stays msg+": %s" regardless of err's nil-ness, so the
	// local dedup key doesn't fragment based on a runtime nil check.
	assert.Contains(t, logs[0], "sdk defect with no error: <nil>")
}

func TestLogAndReportPanic_ErrorRecovered(t *testing.T) {
	var captured telemetry.Record

	origSend := sendLog
	defer func() { sendLog = origSend }()
	sendLog = func(r telemetry.Record, _ ...telemetry.LogOption) { captured = r }

	recorder := &internallog.RecordLogger{}
	undo := internallog.UseLogger(recorder)
	defer undo()

	panicErr := errors.New("nil pointer deref in secret handler")
	LogAndReportPanic("unexpected panic in goroutine", panicErr)
	internallog.Flush()

	assert.Equal(t, "unexpected panic in goroutine", captured.Message)
	found := false
	captured.Attrs(func(a slog.Attr) bool {
		if a.Key == "error" {
			found = true
			assert.NotContains(t, a.Value.String(), "secret")
		}
		return true
	})
	assert.True(t, found)

	logs := recorder.Logs()
	assert.Len(t, logs, 1)
	assert.Contains(t, logs[0], "unexpected panic in goroutine: nil pointer deref in secret handler")
}

func TestLogAndReportPanic_NonErrorRecovered(t *testing.T) {
	var captured telemetry.Record

	origSend := sendLog
	defer func() { sendLog = origSend }()
	sendLog = func(r telemetry.Record, _ ...telemetry.LogOption) { captured = r }

	recorder := &internallog.RecordLogger{}
	undo := internallog.UseLogger(recorder)
	defer undo()

	LogAndReportPanic("unexpected panic in goroutine", "a string panic value")
	internallog.Flush()

	assert.Equal(t, "unexpected panic in goroutine", captured.Message)
	found := false
	captured.Attrs(func(a slog.Attr) bool {
		if a.Key == "recovered_type" {
			found = true
			assert.Equal(t, "string", a.Value.String())
		}
		return true
	})
	assert.True(t, found, "recovered_type attribute must be present")

	logs := recorder.Logs()
	assert.Len(t, logs, 1)
	assert.Contains(t, logs[0], "unexpected panic in goroutine: a string panic value")
}

// nilDerefError's Error method dereferences its receiver, mirroring a common
// real-world bug: a typed-nil *nilDerefError stored in an error interface is
// non-nil (err != nil), but calling Error() panics.
type nilDerefError struct{ msg *string }

func (e *nilDerefError) Error() string { return *e.msg }

// panickyError's Error method panics outright, regardless of receiver state.
type panickyError struct{}

func (panickyError) Error() string { panic("boom") }

func TestLogAndReportError_TypedNilError(t *testing.T) {
	var captured telemetry.Record

	origSend := sendLog
	defer func() { sendLog = origSend }()
	sendLog = func(r telemetry.Record, _ ...telemetry.LogOption) { captured = r }

	recorder := &internallog.RecordLogger{}
	undo := internallog.UseLogger(recorder)
	defer undo()

	var typedNil *nilDerefError
	var err error = typedNil
	assert.NotPanics(t, func() {
		LogAndReportError("sdk defect with typed-nil error", err)
	})
	internallog.Flush()

	assert.Equal(t, "sdk defect with typed-nil error", captured.Message)
	logs := recorder.Logs()
	assert.Len(t, logs, 1)
	assert.Contains(t, logs[0], "sdk defect with typed-nil error: <nil>")
}

func TestLogAndReportError_PanickyError(t *testing.T) {
	var captured telemetry.Record

	origSend := sendLog
	defer func() { sendLog = origSend }()
	sendLog = func(r telemetry.Record, _ ...telemetry.LogOption) { captured = r }

	recorder := &internallog.RecordLogger{}
	undo := internallog.UseLogger(recorder)
	defer undo()

	assert.NotPanics(t, func() {
		LogAndReportError("sdk defect with panicky error", panickyError{})
	})
	internallog.Flush()

	assert.Equal(t, "sdk defect with panicky error", captured.Message)
	logs := recorder.Logs()
	assert.Len(t, logs, 1)
	assert.Contains(t, logs[0], "sdk defect with panicky error: ")
}

func TestLogAndReportPanic_TypedNilError(t *testing.T) {
	var captured telemetry.Record

	origSend := sendLog
	defer func() { sendLog = origSend }()
	sendLog = func(r telemetry.Record, _ ...telemetry.LogOption) { captured = r }

	recorder := &internallog.RecordLogger{}
	undo := internallog.UseLogger(recorder)
	defer undo()

	var typedNil *nilDerefError
	assert.NotPanics(t, func() {
		LogAndReportPanic("unexpected panic in goroutine", typedNil)
	})
	internallog.Flush()

	assert.Equal(t, "unexpected panic in goroutine", captured.Message)
	logs := recorder.Logs()
	assert.Len(t, logs, 1)
	assert.Contains(t, logs[0], "unexpected panic in goroutine: <nil>")
}

func TestLogAndReportPanic_PanickyError(t *testing.T) {
	var captured telemetry.Record

	origSend := sendLog
	defer func() { sendLog = origSend }()
	sendLog = func(r telemetry.Record, _ ...telemetry.LogOption) { captured = r }

	recorder := &internallog.RecordLogger{}
	undo := internallog.UseLogger(recorder)
	defer undo()

	assert.NotPanics(t, func() {
		LogAndReportPanic("unexpected panic in goroutine", panickyError{})
	})
	internallog.Flush()

	assert.Equal(t, "unexpected panic in goroutine", captured.Message)
	logs := recorder.Logs()
	assert.Len(t, logs, 1)
	assert.Contains(t, logs[0], "unexpected panic in goroutine: ")
}

func TestLogAndReportError_PercentInMessageLoggedVerbatim(t *testing.T) {
	// Regression test: LogAndReportError used to pass msg unescaped into
	// internal/log.Error's format string, so a constant message containing a
	// percent was parsed as a format verb instead of logged literally —
	// LogAndReportError("operation %s failed", err) consumed errStr at the
	// embedded verb and left the appended one MISSING, and "reached 100%
	// capacity" tripped go vet's printf check.
	origSend := sendLog
	defer func() { sendLog = origSend }()
	sendLog = func(telemetry.Record, ...telemetry.LogOption) {}

	recorder := &internallog.RecordLogger{}
	undo := internallog.UseLogger(recorder)
	defer undo()

	LogAndReportError("sdk error: reached 100% capacity", errors.New("boom"))
	LogAndReportError("sdk error: operation %s failed", errors.New("boom"))
	internallog.Flush()

	logs := recorder.Logs()
	require.Len(t, logs, 2)
	assert.Contains(t, logs[0], "sdk error: reached 100% capacity: boom")
	assert.Contains(t, logs[1], "sdk error: operation %s failed: boom")
	assert.NotContains(t, logs[0], "%!")
	assert.NotContains(t, logs[1], "%!")
	assert.NotContains(t, logs[1], "MISSING")
}

func TestLogAndReportPanic_PercentInMessageLoggedVerbatim(t *testing.T) {
	origSend := sendLog
	defer func() { sendLog = origSend }()
	sendLog = func(telemetry.Record, ...telemetry.LogOption) {}

	recorder := &internallog.RecordLogger{}
	undo := internallog.UseLogger(recorder)
	defer undo()

	LogAndReportPanic("sdk error: writer at 100% capacity", "a string panic value")
	internallog.Flush()

	logs := recorder.Logs()
	require.Len(t, logs, 1)
	assert.Contains(t, logs[0], "sdk error: writer at 100% capacity: a string panic value")
	assert.NotContains(t, logs[0], "%!")
}

func TestLogAndReportPanic_WithTagsOption(t *testing.T) {
	var capturedOpts []telemetry.LogOption

	origSend := sendLog
	defer func() { sendLog = origSend }()
	sendLog = func(_ telemetry.Record, opts ...telemetry.LogOption) { capturedOpts = opts }

	recorder := &internallog.RecordLogger{}
	undo := internallog.UseLogger(recorder)
	defer undo()

	LogAndReportPanic("unexpected panic in goroutine", "a string panic value", telemetry.WithTags([]string{"env:prod"}))
	internallog.Flush()

	assert.Len(t, capturedOpts, 2, "caller-supplied opts must reach sendLog alongside WithStacktraceNow")
}

// BenchmarkReportError measures the cost of the explicit ReportError helper,
// used at swallowed-error call sites that opt into Error Tracking.
func BenchmarkReportError(b *testing.B) {
	orig := sendLog
	defer func() { sendLog = orig }()
	sendLog = func(telemetry.Record, ...telemetry.LogOption) {}

	sentinel := errors.New("benchmark sentinel")
	b.ReportAllocs()
	for b.Loop() {
		ReportError("benchmark: reported error", sentinel)
	}
}

// BenchmarkReportPanic measures the cost of the recover()-site helper.
func BenchmarkReportPanic(b *testing.B) {
	orig := sendLog
	defer func() { sendLog = orig }()
	sendLog = func(telemetry.Record, ...telemetry.LogOption) {}

	panicErr := errors.New("benchmark panic")
	b.ReportAllocs()
	for b.Loop() {
		ReportPanic("benchmark: recovered panic", panicErr)
	}
}
