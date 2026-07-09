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

func TestReportError_ExcludedTemplate(t *testing.T) {
	called := false

	orig := sendLog
	defer func() { sendLog = orig }()
	sendLog = func(_ telemetry.Record, _ ...telemetry.LogOption) { called = true }

	ReportError("failure sending traces (attempt %d of %d): %v", nil)
	assert.False(t, called, "excluded template must not reach telemetry")
}

func TestReportError_DowngradedTemplate(t *testing.T) {
	var captured telemetry.Record

	orig := sendLog
	defer func() { sendLog = orig }()
	sendLog = func(r telemetry.Record, _ ...telemetry.LogOption) { captured = r }

	ReportError("config: usage of a unlisted environment variable: %s", nil)
	assert.Equal(t, slog.LevelWarn, captured.Level)
}

func TestReportPanic_ErrorRecovered(t *testing.T) {
	var captured telemetry.Record

	orig := sendLog
	defer func() { sendLog = orig }()
	sendLog = func(r telemetry.Record, _ ...telemetry.LogOption) { captured = r }

	panicErr := errors.New("nil pointer deref in secret handler")
	ReportPanic(panicErr, "unexpected panic in goroutine")

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

	ReportPanic("a string panic value", "unexpected panic in goroutine")

	assert.Equal(t, "unexpected panic in goroutine", captured.Message)
	// Non-error recovered values are not attached.
	hasAttr := false
	captured.Attrs(func(_ slog.Attr) bool { hasAttr = true; return false })
	assert.False(t, hasAttr)
}

func TestReportPanic_ExcludedMessage(t *testing.T) {
	called := false

	orig := sendLog
	defer func() { sendLog = orig }()
	sendLog = func(_ telemetry.Record, _ ...telemetry.LogOption) { called = true }

	// Seed policy table with a test-only exclusion.
	orig2 := policyTable["test-excluded-panic-msg"]
	policyTable["test-excluded-panic-msg"] = policyExclude
	defer func() {
		if orig2 == 0 {
			delete(policyTable, "test-excluded-panic-msg")
		} else {
			policyTable["test-excluded-panic-msg"] = orig2
		}
	}()

	ReportPanic(nil, "test-excluded-panic-msg")
	assert.False(t, called)
}

// BenchmarkReportError measures the cost of the explicit ReportError helper,
// used at swallowed-error call sites migrated by Prompt 2.
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
		ReportPanic(panicErr, "benchmark: recovered panic")
	}
}
