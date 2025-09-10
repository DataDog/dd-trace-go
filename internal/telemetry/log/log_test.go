// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

func TestLogger_PCCapture(t *testing.T) {
	tests := []struct {
		name             string
		capturePC        bool
		recordPC         uintptr
		expectPCCaptured bool
	}{
		{
			name:             "capture PC when enabled and record has no PC",
			capturePC:        true,
			recordPC:         0,
			expectPCCaptured: true,
		},
		{
			name:             "don't capture PC when disabled",
			capturePC:        false,
			recordPC:         0,
			expectPCCaptured: false,
		},
		{
			name:             "don't override existing PC",
			capturePC:        true,
			recordPC:         123,
			expectPCCaptured: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedRecord slog.Record

			originalSendLog := sendLog
			defer func() { sendLog = originalSendLog }()

			sendLog = func(r slog.Record, opts ...telemetry.LogOption) {
				capturedRecord = r
			}

			logger := &Logger{capturePC: tt.capturePC}

			record := slog.NewRecord(time.Now(), slog.LevelInfo, "test message", tt.recordPC)
			logger.sendLogRecord(record)

			assert.Equal(t, "test message", capturedRecord.Message)
			assert.Equal(t, slog.LevelInfo, capturedRecord.Level)

			if tt.expectPCCaptured {
				assert.NotZero(t, capturedRecord.PC)
			} else if tt.recordPC == 0 {
				assert.Zero(t, capturedRecord.PC)
			} else {
				assert.Equal(t, tt.recordPC, capturedRecord.PC)
			}
		})
	}
}

func TestLogger_Debug(t *testing.T) {
	var capturedRecord slog.Record

	originalSendLog := sendLog
	defer func() { sendLog = originalSendLog }()

	sendLog = func(r slog.Record, opts ...telemetry.LogOption) {
		capturedRecord = r
	}

	logger := &Logger{capturePC: true}
	logger.Debug("debug message", slog.String("key", "value"))

	assert.Equal(t, "debug message", capturedRecord.Message)
	assert.Equal(t, slog.LevelDebug, capturedRecord.Level)
	assert.NotZero(t, capturedRecord.PC)
}

func TestLogger_Warn(t *testing.T) {
	var capturedRecord slog.Record

	originalSendLog := sendLog
	defer func() { sendLog = originalSendLog }()

	sendLog = func(r slog.Record, opts ...telemetry.LogOption) {
		capturedRecord = r
	}

	logger := &Logger{capturePC: true}
	logger.Warn("warn message", slog.String("key", "value"))

	assert.Equal(t, "warn message", capturedRecord.Message)
	assert.Equal(t, slog.LevelWarn, capturedRecord.Level)
	assert.NotZero(t, capturedRecord.PC)
}

func TestLogger_Error(t *testing.T) {
	var capturedRecord slog.Record

	originalSendLog := sendLog
	defer func() { sendLog = originalSendLog }()

	sendLog = func(r slog.Record, opts ...telemetry.LogOption) {
		capturedRecord = r
	}

	logger := &Logger{capturePC: true}
	logger.Error("error message", slog.String("key", "value"))

	assert.Equal(t, "error message", capturedRecord.Message)
	assert.Equal(t, slog.LevelError, capturedRecord.Level)
	assert.NotZero(t, capturedRecord.PC)
}

func TestLogger_With(t *testing.T) {
	logger1 := &Logger{capturePC: true, opts: []telemetry.LogOption{telemetry.WithTags([]string{"tag1"})}}
	logger2 := logger1.With(telemetry.WithTags([]string{"tag2"}))

	assert.True(t, logger2.capturePC)
	assert.Len(t, logger2.opts, 2)
	assert.NotSame(t, logger1, logger2)
}

func TestWith(t *testing.T) {
	logger := With(telemetry.WithTags([]string{"tag1"}))

	assert.True(t, logger.capturePC)
	assert.Len(t, logger.opts, 1)
}

func TestGlobalFunctions(t *testing.T) {
	var capturedRecord slog.Record

	originalSendLog := sendLog
	defer func() { sendLog = originalSendLog }()

	sendLog = func(r slog.Record, opts ...telemetry.LogOption) {
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

func TestLogger_WithStacktrace_CapturesPC(t *testing.T) {
	var capturedRecord slog.Record

	originalSendLog := sendLog
	defer func() { sendLog = originalSendLog }()

	sendLog = func(r slog.Record, opts ...telemetry.LogOption) {
		capturedRecord = r
	}

	t.Run("captures PC when WithStacktrace is used even if capturePC is false", func(t *testing.T) {
		logger := &Logger{capturePC: false, opts: []telemetry.LogOption{telemetry.WithStacktrace()}}
		logger.Error("error with stacktrace")

		assert.Equal(t, "error with stacktrace", capturedRecord.Message)
		assert.Equal(t, slog.LevelError, capturedRecord.Level)
		// This should capture PC even though capturePC is false
		assert.NotZero(t, capturedRecord.PC, "Should capture PC when WithStacktrace option is present")
	})

	t.Run("still captures PC when capturePC is true", func(t *testing.T) {
		logger := &Logger{capturePC: true, opts: []telemetry.LogOption{telemetry.WithStacktrace()}}
		logger.Error("error with stacktrace")

		assert.Equal(t, "error with stacktrace", capturedRecord.Message)
		assert.NotZero(t, capturedRecord.PC, "Should capture PC when capturePC is true")
	})

	t.Run("no PC capture when capturePC is false and no options", func(t *testing.T) {
		logger := &Logger{capturePC: false, opts: nil}
		logger.Error("error without stacktrace")

		assert.Equal(t, "error without stacktrace", capturedRecord.Message)
		assert.Zero(t, capturedRecord.PC, "Should not capture PC when capturePC is false and no options")
	})

	t.Run("captures PC when capturePC is false but has non-stacktrace options", func(t *testing.T) {
		// This is the documented behavior: we capture PC whenever options are present
		// since we can't inspect their contents without exposing internal types
		logger := &Logger{capturePC: false, opts: []telemetry.LogOption{telemetry.WithTags([]string{"tag1"})}}
		logger.Error("error with tags")

		assert.Equal(t, "error with tags", capturedRecord.Message)
		assert.NotZero(t, capturedRecord.PC, "Should capture PC when options are present (conservative approach)")
	})
}
