// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

func TestLogger_With(t *testing.T) {
	logger1 := &Logger{opts: []telemetry.LogOption{telemetry.WithTags([]string{"tag1"})}}
	logger2 := logger1.With(telemetry.WithTags([]string{"tag2"}))

	assert.Len(t, logger2.opts, 2)
	assert.NotSame(t, logger1, logger2)
}

func TestWith(t *testing.T) {
	logger := With(telemetry.WithTags([]string{"tag1"}))

	assert.Len(t, logger.opts, 1)
}

func TestGlobalFunctions(t *testing.T) {
	var capturedRecord telemetry.Record

	originalSendLog := sendLog
	defer func() { sendLog = originalSendLog }()

	sendLog = func(r telemetry.Record, opts ...telemetry.LogOption) {
		capturedRecord = r
	}

	t.Run("Debug", func(t *testing.T) {
		Debug("debug message")
		assert.Equal(t, "debug message", capturedRecord.Message)
		assert.Equal(t, slog.LevelDebug, capturedRecord.Level)
	})

	t.Run("Warn", func(t *testing.T) {
		Warn("warn message")
		assert.Equal(t, "warn message", capturedRecord.Message)
		assert.Equal(t, slog.LevelWarn, capturedRecord.Level)
	})

	t.Run("Error", func(t *testing.T) {
		Error("error message")
		assert.Equal(t, "error message", capturedRecord.Message)
		assert.Equal(t, slog.LevelError, capturedRecord.Level)
	})
}

// TestStackTraceIntegration verifies the stack trace works with WithStacktrace
func TestStackTraceIntegration(t *testing.T) {
	// This test verifies that our stacktrace approach integrates properly with stack trace generation
	// We can't easily test the actual stack unwinding here, but we can verify the basic flow works

	var capturedRecord telemetry.Record
	var capturedOpts []telemetry.LogOption

	originalSendLog := sendLog
	defer func() { sendLog = originalSendLog }()

	sendLog = func(r telemetry.Record, opts ...telemetry.LogOption) {
		capturedRecord = r
		capturedOpts = opts
	}

	logger := With(telemetry.WithStacktrace())
	logger.Error("error with stacktrace")

	assert.Equal(t, "error with stacktrace", capturedRecord.Message)
	assert.Len(t, capturedOpts, 1) // Should have WithStacktrace option
	assert.NotZero(t, capturedRecord.PC)
}
