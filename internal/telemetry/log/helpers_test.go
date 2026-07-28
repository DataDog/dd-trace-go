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
