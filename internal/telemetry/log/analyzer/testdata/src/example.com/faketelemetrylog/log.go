// Package faketelemetrylog is a stub used by the constantlogmsg analyzer tests.
// It mirrors the public surface of internal/telemetry/log so that analysistest
// can resolve function calls without hitting internal-package restrictions.
package faketelemetrylog

import "log/slog"

// Logger mirrors internal/telemetry/log.Logger.
type Logger struct{}

// With returns a Logger with no-op options (stub).
func With() *Logger { return &Logger{} }

// Package-level functions.
func Debug(message string, attrs ...any) {}
func Warn(message string, attrs ...any)  {}
func Error(message string, attrs ...any) {}

// Logger methods — same names, so the same FuncSpec covers both call styles.
func (l *Logger) Debug(message string, attrs ...any) {}
func (l *Logger) Warn(message string, attrs ...any)  {}
func (l *Logger) Error(message string, attrs ...any) {}

// Helpers.
func ReportError(msg string, err error, opts ...any) {}
func ReportPanic(recovered any, msg string)           {}

// SafeError mirrors internal/telemetry/log.SafeError: it implements
// slog.LogValuer so telemetrysafety's slog.Any checks treat it as safe.
type SafeError struct{ errType string }

func NewSafeError(err error) SafeError { return SafeError{} }

func (e SafeError) LogValue() slog.Value {
	return slog.GroupValue(slog.String("error_type", e.errType))
}
