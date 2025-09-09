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
	"slices"
	"sync/atomic"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

// Logger represents a contextual logger with pre-configured telemetry options
type Logger struct {
	handler slog.Handler
	writer  *handlerWriter
	opts    []telemetry.LogOption
}

// defaultLogger is the global logger instance with no pre-configured options
var defaultLogger atomic.Pointer[Logger]

func init() {
	handler := NewTelemetryHandler()
	defaultLogger.Store(&Logger{
		handler: handler,
		writer:  NewHandlerWriter(handler, true),
	})
}

func SetDefaultLogger(logger *Logger) {
	defaultLogger.CompareAndSwap(defaultLogger.Load(), logger)
}

// With creates a new contextual logger with the given telemetry options
func With(opts ...telemetry.LogOption) *Logger {
	handler := NewTelemetryHandler(opts...)
	return &Logger{
		handler: handler,
		writer:  NewHandlerWriter(handler, true),
		opts:    opts,
	}
}

// With creates a new logger that extends the current logger's options with additional options
func (l *Logger) With(opts ...telemetry.LogOption) *Logger {
	combinedOpts := slices.Concat(l.opts, opts)
	handler := NewTelemetryHandler(combinedOpts...)
	return &Logger{
		handler: handler,
		writer:  NewHandlerWriter(handler, true),
		opts:    combinedOpts,
	}
}


// Debug sends a telemetry payload with a debug log message to the backend using the default logger
func Debug(message string, attrs ...slog.Attr) {
	defaultLogger.Load().Debug(message, attrs...) //nolint:gocritic // Telemetry plumbing - message parameter delegation is safe
}

// Warn sends a telemetry payload with a warning log message to the backend using the default logger
func Warn(message string, attrs ...slog.Attr) {
	defaultLogger.Load().Warn(message, attrs...) //nolint:gocritic // Telemetry plumbing - message parameter delegation is safe
}

// Error sends a telemetry payload with an error log message to the backend using the default logger.
// SECURITY: Only accepts constant message strings. Use SafeError for error details.
func Error(message string, attrs ...slog.Attr) {
	defaultLogger.Load().Error(message, attrs...)
}

// Debug sends a telemetry payload with a debug log message to the backend
func (l *Logger) Debug(message string, attrs ...slog.Attr) {
	record := slog.NewRecord(time.Now(), slog.LevelDebug, message, 0)
	record.AddAttrs(attrs...)
	l.writer.LogRecord(record)
}

// Warn sends a telemetry payload with a warning log message to the backend and the console as a debug log
func (l *Logger) Warn(message string, attrs ...slog.Attr) {
	record := slog.NewRecord(time.Now(), slog.LevelWarn, message, 0)
	record.AddAttrs(attrs...)
	l.writer.LogRecord(record)
}

// Error sends a telemetry payload with an error log message to the backend and the console as a debug log.
// SECURITY: Only accepts constant message strings. Use SafeError with slog.Any() for error details.
// Example: logger.Error("operation failed", slog.Any("error", SafeError(err)))
func (l *Logger) Error(message string, attrs ...slog.Attr) {
	record := slog.NewRecord(time.Now(), slog.LevelError, message, 0)
	record.AddAttrs(attrs...)
	l.writer.LogRecord(record)
}
