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
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

// Logger represents a contextual logger with pre-configured telemetry options
type Logger struct {
	opts []telemetry.LogOption
}

// defaultLogger is the global logger instance with no pre-configured options
var defaultLogger = &Logger{}

// With creates a new contextual logger with the given telemetry options
func With(opts ...telemetry.LogOption) *Logger {
	return &Logger{opts: opts}
}

// With creates a new logger that extends the current logger's options with additional options
func (l *Logger) With(opts ...telemetry.LogOption) *Logger {
	return &Logger{opts: slices.Concat(l.opts, opts)}
}

func formatMessageWithAttrs(message string, attrs []slog.Attr) string {
	if len(attrs) == 0 {
		return message
	}

	parts := make([]string, len(attrs))
	for i, attr := range attrs {
		parts[i] = "<" + attr.Key + ":" + attr.Value.String() + ">"
	}
	return message + ": " + strings.Join(parts, ", ")
}

// Debug sends a telemetry payload with a debug log message to the backend using the default logger
func Debug(message string, attrs ...slog.Attr) {
	defaultLogger.Debug(message, attrs...) //nolint:gocritic // Telemetry plumbing - message parameter delegation is safe
}

// Warn sends a telemetry payload with a warning log message to the backend using the default logger
func Warn(message string, attrs ...slog.Attr) {
	defaultLogger.Warn(message, attrs...) //nolint:gocritic // Telemetry plumbing - message parameter delegation is safe
}

// Error sends a telemetry payload with an error log message to the backend using the default logger.
// SECURITY: Only accepts constant message strings. Use SafeError for error details.
func Error(message string, attrs ...slog.Attr) {
	defaultLogger.Error(message, attrs...)
}

// Debug sends a telemetry payload with a debug log message to the backend
func (l *Logger) Debug(message string, attrs ...slog.Attr) {
	text := formatMessageWithAttrs(message, attrs)
	telemetry.Log(telemetry.LogDebug, text, l.opts...)
}

// Warn sends a telemetry payload with a warning log message to the backend and the console as a debug log
func (l *Logger) Warn(message string, attrs ...slog.Attr) {
	text := formatMessageWithAttrs(message, attrs)
	telemetry.Log(telemetry.LogWarn, text, l.opts...)
}

// Error sends a telemetry payload with an error log message to the backend and the console as a debug log.
// SECURITY: Only accepts constant message strings. Use SafeError with slog.Any() for error details.
// Example: logger.Error("operation failed", slog.Any("error", SafeError(err)))
func (l *Logger) Error(message string, attrs ...slog.Attr) {
	text := formatMessageWithAttrs(message, attrs)
	telemetry.Log(telemetry.LogError, text, l.opts...)
}
