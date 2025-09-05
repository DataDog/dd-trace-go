// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build ruleguard

// Package gorules contains security-focused telemetry logging rules for dd-trace-go.
//
// TELEMETRY LOGGING SECURITY MODEL:
//
// STRICT POLICY for telemetry logging (internal/telemetry/log):
// - Require constant messages with structured slog.Attr parameters
// - No format strings allowed - use constant messages + slog.Attr key-value pairs
// - Error API requires constant messages with SafeError for error details
// - slog.Any() only allowed with LogValuer implementations
// - Stack traces automatically redacted (customer code replaced with "REDACTED")
//
// RATIONALE:
// Telemetry data is sent to external services and must be carefully controlled
// to prevent PII leakage and ensure security auditing capabilities.
//
// EXAMPLES:
//
// ❌ Forbidden (telemetry):
//
//	telemetrylog.Error(err.Error())                               // Dynamic error message
//	telemetrylog.Error("failed", slog.Any("error", err))          // Raw error exposure
//	telemetrylog.Debug(dynamicMessage, attr)                      // Variable message string
//	telemetrylog.Debug("message", slog.Any("data", userData))     // Non-LogValuer with slog.Any()
//
// ✅ Allowed (telemetry):
//
//	telemetrylog.Error("operation failed", slog.Any("error", SafeError(err)))  // Secure error logging
//	telemetrylog.Debug("operation completed", slog.String("id", id))           // Constant message + structured data
//	logger := telemetrylog.With(tags); logger.Error("failed", attrs...)        // Contextual logger with constants
package gorules

import (
	"github.com/quasilyte/go-ruleguard/dsl"
	"github.com/quasilyte/go-ruleguard/dsl/types"
)

const (
	telemetryLogPackage = "github.com/DataDog/dd-trace-go/v2/internal/telemetry/log"
	telemetryLoggerType = telemetryLogPackage + ".Logger"
)

//doc:summary TELEMETRY SECURITY: detects unsafe slog.Any() usage in telemetry logging
//doc:before  telemetrylog.Debug("message", slog.Any("data", userData))
//doc:after   telemetrylog.Debug("message", slog.String("user_id", userData.ID)) // or implement slog.LogValuer on userData
//doc:tags    security telemetry data-leak slog-any reflection logvaluer
func telemetryLogSmartSlogAny(m dsl.Matcher) {
	// SECURITY POLICY: Allow slog.Any() only with LogValuer types, forbid with arbitrary types
	// Rationale: slog.Any() uses reflection, but LogValuer gives explicit control over logging
	// Force explicit types or controlled LogValuer implementations for security
	m.Import(telemetryLogPackage)

	// Match telemetry log calls that use slog.Any() with non-LogValuer types
	// Allow LogValuer implementations since they control their own representation
	m.Match(
		`$pkg.Debug($msg, $*_, slog.Any($key, $value), $*_)`,
		`$pkg.Warn($msg, $*_, slog.Any($key, $value), $*_)`,
		`$pkg.Error($msg, $*_, slog.Any($key, $value), $*_)`,
		`$logger.Debug($msg, $*_, slog.Any($key, $value), $*_)`,
		`$logger.Warn($msg, $*_, slog.Any($key, $value), $*_)`,
		`$logger.Error($msg, $*_, slog.Any($key, $value), $*_)`,
	).
		Where(!m["value"].Filter(implementsLogValuer) &&
			m.File().Imports(telemetryLogPackage)).
		Report("Forbidden: (telemetry logging) slog.Any() with non-LogValuer types can expose sensitive data via reflection. Use explicit types like slog.String(), slog.Int(), or implement slog.LogValuer interface for controlled logging.")
}

//doc:summary TELEMETRY SECURITY: detects usage of variable message strings in telemetry logging
//doc:before  telemetrylog.Debug(dynamicMsg, slog.String("key", value))
//doc:after   telemetrylog.Debug("constant message", slog.String("key", value))
//doc:tags    security telemetry compile-time-safety message-constants
func telemetryLogConstantMessage(m dsl.Matcher) {
	// SECURITY POLICY: Telemetry logging requires compile-time constant message strings
	// Rationale: Variable messages make it impossible to audit what data might be exposed
	// and can lead to uncontrolled information disclosure in telemetry data
	m.Import(telemetryLogPackage)

	// Match telemetry log calls with non-constant message strings
	// All message strings must be compile-time constants for security auditing
	// Exclude internal delegation within the telemetry/log package itself
	m.Match(
		`$pkg.Debug($msg, $*_)`,
		`$pkg.Warn($msg, $*_)`,
		`$pkg.Error($msg, $*_)`,
		`$logger.Debug($msg, $*_)`,
		`$logger.Warn($msg, $*_)`,
		`$logger.Error($msg, $*_)`,
	).
		Where(!m["msg"].Const &&
			!m.File().PkgPath.Matches(`.*/internal/telemetry/log$`) &&
			m.File().Imports(telemetryLogPackage)).
		Report("Forbidden: (telemetry logging) variable message strings prevent security auditing. Use compile-time constant strings for message parameter: telemetrylog.Debug(\"constant message\", attrs...)")

}

//doc:summary TELEMETRY SECURITY: detects direct error usage without SafeError wrapper
//doc:before  telemetrylog.Error("failed", slog.Any("error", rawError))
//doc:after   telemetrylog.Error("failed", slog.Any("error", SafeError(rawError)))
//doc:tags    security telemetry error-handling safeerror logvaluer
func telemetryLogRawErrorUsage(m dsl.Matcher) {
	// SECURITY POLICY: Raw errors must use SafeError wrapper to prevent PII leakage
	// Rationale: Raw errors can contain sensitive information in error messages
	// SafeError provides secure error logging with stack trace redaction
	m.Import(telemetryLogPackage)

	// Match telemetry log calls that use raw errors with slog.Any() without SafeError wrapper
	m.Match(
		`$pkg.Debug($msg, $*_, slog.Any($key, $value), $*_)`,
		`$pkg.Warn($msg, $*_, slog.Any($key, $value), $*_)`,
		`$pkg.Error($msg, $*_, slog.Any($key, $value), $*_)`,
		`$logger.Debug($msg, $*_, slog.Any($key, $value), $*_)`,
		`$logger.Warn($msg, $*_, slog.Any($key, $value), $*_)`,
		`$logger.Error($msg, $*_, slog.Any($key, $value), $*_)`,
	).
		Where(m["value"].Type.Is("error") &&
			!m["value"].Text.Matches(`.*SafeError\(.*\)`) &&
			m.File().Imports(telemetryLogPackage)).
		Report("Forbidden: (telemetry logging) raw error values can expose sensitive data. Use SafeError wrapper: slog.Any(\"error\", SafeError(err))")
}

// implementsLogValuer checks if a type implements the slog.LogValuer interface.
// This includes both direct implementations and pointer receivers.
// Used to identify when slog.Any() usage is safe in telemetry logging contexts.
func implementsLogValuer(ctx *dsl.VarFilterContext) bool {
	iface := ctx.GetInterface(`log/slog.LogValuer`)
	return types.Implements(ctx.Type, iface) || types.Implements(types.NewPointer(ctx.Type), iface)
}
