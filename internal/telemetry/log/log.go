// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package log provides secure telemetry logging with strict security controls.
//
// SECURITY MODEL:
//
// This package implements strict security controls for telemetry logging to prevent
// PII and sensitive information from being sent to external telemetry services.
//
// REQUIREMENTS:
//   - Messages MUST be constant templates only - no dynamic parameter replacement
//   - Stack traces MUST be redacted to show only Datadog, runtime, and known 3rd party frames
//   - Errors MUST use SafeError type with message redaction
//   - slog.Any() only allowed with LogValuer implementations
//
// BENEFITS:
//   - Constant messages enable deduplication to reduce redundant log transmission
//
// SECURE USAGE PATTERNS:
//
//	// ✅ Correct - constant message with structured data
//	telemetrylog.Error("operation failed", slog.String("operation", "startup"))
//	telemetrylog.Error("validation error", slog.Any("error", SafeError(err)))
//	telemetrylog.Error("operation failed", slog.Any("error", SafeError(err)), WithStacktrace())
//
//	// ❌ Forbidden - dynamic messages
//	telemetrylog.Error(err.Error()) // Raw error message
//	telemetrylog.Error("failed: " + details) // String concatenation
//	telemetrylog.Error(fmt.Sprintf("error: %s", err)) // Format strings
//
//	// ❌ Forbidden - raw error exposure
//	telemetrylog.Error("failed", slog.Any("error", err)) // Raw error object
//	telemetrylog.Error("failed", slog.String("err", err.Error())) // Raw error message
package log

import (
	"log/slog"
	"runtime"
	"slices"
	"sync/atomic"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

var sendLog func(r slog.Record, opts ...telemetry.LogOption) = telemetry.Log

type Logger struct {
	capturePC bool
	opts      []telemetry.LogOption
}

var (
	// defaultLogger is the global logger instance with no pre-configured options
	defaultLogger atomic.Pointer[Logger]

	// defaultCapturePC is whether to capture the program counter for stack traces
	defaultCapturePC = true
)

func init() {
	defaultLogger.Store(&Logger{
		capturePC: defaultCapturePC,
	})
}

func SetDefaultLogger(logger *Logger) {
	defaultLogger.CompareAndSwap(defaultLogger.Load(), logger)
}

func With(opts ...telemetry.LogOption) *Logger {
	return &Logger{
		capturePC: defaultCapturePC,
		opts:      opts,
	}
}

func (l *Logger) With(opts ...telemetry.LogOption) *Logger {
	combinedOpts := slices.Concat(l.opts, opts)
	return &Logger{
		capturePC: l.capturePC,
		opts:      combinedOpts,
	}
}

func Debug(message string, attrs ...slog.Attr) {
	defaultLogger.Load().Debug(message, attrs...)
}

func Warn(message string, attrs ...slog.Attr) {
	defaultLogger.Load().Warn(message, attrs...)
}

func Error(message string, attrs ...slog.Attr) {
	defaultLogger.Load().Error(message, attrs...)
}

func (l *Logger) Debug(message string, attrs ...slog.Attr) {
	record := slog.NewRecord(time.Now(), slog.LevelDebug, message, 0)
	record.AddAttrs(attrs...)
	l.sendLogRecord(record)
}

func (l *Logger) Warn(message string, attrs ...slog.Attr) {
	record := slog.NewRecord(time.Now(), slog.LevelWarn, message, 0)
	record.AddAttrs(attrs...)
	l.sendLogRecord(record)
}

func (l *Logger) Error(message string, attrs ...slog.Attr) {
	record := slog.NewRecord(time.Now(), slog.LevelError, message, 0)
	record.AddAttrs(attrs...)
	l.sendLogRecord(record)
}

func (l *Logger) sendLogRecord(r slog.Record) {
	// Capture PC if:
	// 1. Logger is configured to always capture PC, OR
	// 2. Logger has options (which might include WithStacktrace)
	//    Since we can't inspect option contents without exposing internal types,
	//    we conservatively capture PC whenever options are present.
	//    This ensures WithStacktrace() works correctly while keeping the overhead minimal.
	// Also capturing a single frame is cheap enough to do always.
	needsPC := l.capturePC || (!l.capturePC && len(l.opts) > 0)

	if needsPC && r.PC == 0 {
		var pcs [1]uintptr
		n := runtime.Callers(4, pcs[:])
		if n > 0 {
			r.PC = pcs[0]
		}
	}
	sendLog(r, l.opts...)
}
