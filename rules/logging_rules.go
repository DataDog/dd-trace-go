// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build ruleguard

// Package gorules contains security-focused linting rules for logging in dd-trace-go.
//
// SECURITY LOGGING MODEL:
//
// This package implements differentiated security policies for different types of logging:
//
// INTERNAL/STANDARD LOGGING (internal/log, log):
//   - PERMISSIVE POLICY: Allow %v for error types and err.Error() calls
//   - Rationale: Internal logs stay within the application but still need data protection
//   - ALLOWED: err.Error() with %v (returns controlled string)
//   - SUGGESTED: Raw error variables with %v (suggest err.Error() for explicitness)
//
// ERROR TYPE HANDLING:
//   - err.Error() calls with %v are ALLOWED (returns controlled string)
//   - Raw error variables with %v get SUGGESTIONS (recommend err.Error())
//   - Position requirement: %v must be the last format verb (allows trailing chars like \n)
//
// RECOMMENDED PRACTICE:
//   - Prefer err.Error() for explicitness even though raw errors are allowed
//   - This makes the intent clear and follows defensive programming principles
//
// EXAMPLES:
//
// ❌ Forbidden (internal):
//
//	log.Error("value: %v", someString)             // Non-error type with %v
//	log.Error("error %v at line %d", err, 123)     // %v not at end
//
// ✅ Allowed (internal):
//
//	log.Error("operation failed: %s", err.Error()) // err.Error() with %s is fine
//	log.Error("failed with %v\n", err.Error())     // %v at end, trailing chars OK
//
// 🔍 Suggested improvement (internal):
//
//	log.Error("operation failed: %s", err.Error())         // Raw error - suggest err.Error()
//	log.Error("failed with %v\n", err)             // Raw error - suggest err.Error()
package gorules

import (
	"github.com/quasilyte/go-ruleguard/dsl"
	"github.com/quasilyte/go-ruleguard/dsl/types"
)

const (
	internalLogPackage     = "github.com/DataDog/dd-trace-go/v2/internal/log"
	formatVerbRegexPattern = `.*%[+#]?v.*` // %v, %+v, %#v

	// Pattern for %v as the last format verb (allows other non-format chars like \n after)
	endVerbPattern = `.*%[+#]?v[^%]*$` // %v as last format verb, no more % after it

	// Base security messages explaining the rationale
	logMessageFormat  = "format verbs %v, %+v, or %#v prevents controlled data exposure. Use specific format verbs like %s, %d, %q and sanitize data before logging."
	logMessageDynamic = "logging with variable format string. Use compile-time constant format strings to ensure controlled data exposure and prevent format string injection."

	// Best practice suggestion for error types
	logMessageErrorSuggestion = "prefer err.Error() over %v for explicit error formatting. While %v with error types is allowed, err.Error() makes the intent clearer and follows defensive programming practices."

	// Predefined complete messages for violations:
	internalLogPrefix         = "Forbidden: (internal log) "
	internalLogFormatMessage  = internalLogPrefix + logMessageFormat
	internalLogDynamicMessage = internalLogPrefix + logMessageDynamic
	stdLogPrefix              = "Forbidden: (standard log) "
	stdLogFormatMessage       = stdLogPrefix + logMessageFormat
	stdLogDynamicMessage      = stdLogPrefix + logMessageDynamic

	// Best practice suggestions for error types:
	internalLogSuggestionPrefix = "Suggestion: (internal log) "
	internalLogErrorSuggestion  = internalLogSuggestionPrefix + logMessageErrorSuggestion
	stdLogSuggestionPrefix      = "Suggestion: (standard log) "
	stdLogErrorSuggestion       = stdLogSuggestionPrefix + logMessageErrorSuggestion
)

//doc:summary INTERNAL SECURITY: detects unsafe %v usage in internal logging (non-error types or wrong position)
//doc:before  log.Error("user value: %v", someString)
//doc:after   log.Error("user value: %s", someString)
//doc:tags    security internal-log data-leak format-verbs
func internalLogFormatVerbs(m dsl.Matcher) {
	// SECURITY POLICY: Internal logging has PERMISSIVE policy with error handling
	// - ALLOWED: %v with err.Error() calls (returns controlled string)
	// - SUGGESTED: %v with raw error types (suggest err.Error() for explicitness)
	// - FORBIDDEN: %v with non-error types or %v not at end
	// - Position requirement: %v must be the last format verb (trailing chars like \n are OK)
	m.Import(internalLogPackage)

	// Match internal log calls that violate the error handling rules:
	// 1. %v with non-error types (any position) - VIOLATION
	// 2. %v not at end of format string (even with error types) - VIOLATION
	// 3. %v with err.Error() calls - ALLOWED (no violation)
	// 4. %v at end with raw error type - SUGGESTION (handled by suggestion rule)
	m.Match(
		`log.Debug($format, $*_, $lastArg)`,
		`log.Info($format, $*_, $lastArg)`,
		`log.Warn($format, $*_, $lastArg)`,
		`log.Error($format, $*_, $lastArg)`,
		`log.Debug($format, $lastArg)`,
		`log.Info($format, $lastArg)`,
		`log.Warn($format, $lastArg)`,
		`log.Error($format, $lastArg)`,
	).
		Where(
			!m.File().Name.Matches(`.*_test\.go$`) &&
				m["format"].Text.Matches(formatVerbRegexPattern) &&
				!m["lastArg"].Text.Matches(`.*\.Error\(\).*`) && // Allow err.Error() calls
				(!m["format"].Text.Matches(endVerbPattern) ||
					(!m["lastArg"].Type.Is(`error`) && !m["lastArg"].Filter(implementsError))),
		).
		Report(internalLogFormatMessage)
}

//doc:summary STANDARD LOG SECURITY: detects unsafe %v usage in standard logging (depguard-allowed files only)
//doc:before  log.Printf("user value: %v", someString)
//doc:after   log.Printf("user value: %s", someString)
//doc:tags    security standard-log data-leak format-verbs depguard
func stdLogFormatVerbs(m dsl.Matcher) {
	// SECURITY POLICY: Standard logging follows same policy as internal logging
	// - ALLOWED: %v with err.Error() calls (returns controlled string)
	// - SUGGESTED: %v with raw error types (suggest err.Error() for explicitness)
	// - FORBIDDEN: %v with non-error types or %v not at end
	// - Only applies to files where depguard allows standard log usage
	m.Import(`log`)

	// Match standard log calls in depguard-allowed files that violate error handling rules
	// Same rules as internal logging: allow err.Error(), suggest for raw errors
	m.Match(
		`log.Printf($format, $lastArg)`,
		`log.Fatalf($format, $lastArg)`,
		`log.Panicf($format, $lastArg)`,
		`log.Printf($format, $*_, $lastArg)`,
		`log.Fatalf($format, $*_, $lastArg)`,
		`log.Panicf($format, $*_, $lastArg)`,
	).
		Where(
			(m.File().PkgPath.Matches(`.*/scripts/.*`) ||
				m.File().PkgPath.Matches(`.*/tools/.*`) ||
				m.File().PkgPath.Matches(`.*/internal/log/log\.go$`) ||
				m.File().PkgPath.Matches(`.*/internal/orchestrion/.*`) ||
				m.File().PkgPath.Matches(`.*/instrumentation/testutils/sql/sql\.go$`)) &&
				!m.File().Name.Matches(`.*_test\.go$`) &&
				m["format"].Text.Matches(formatVerbRegexPattern) &&
				!m["lastArg"].Text.Matches(`.*\.Error\(\).*`) && // Allow err.Error() calls
				(!m["format"].Text.Matches(endVerbPattern) ||
					(!m["lastArg"].Type.Is(`error`) && !m["lastArg"].Filter(implementsError))),
		).
		Report(stdLogFormatMessage)
}

//doc:summary INTERNAL SECURITY: detects usage of variable format strings in internal DD log functions
//doc:before  log.Error(msg, err)
//doc:after   log.Error("specific error message: %s", err.Error())
//doc:tags    security internal-log compile-time-safety injection
func internalLogVariableFormat(m dsl.Matcher) {
	// SECURITY POLICY: Internal logging requires compile-time constant format strings
	// Rationale: Variable format strings can lead to format string injection attacks
	// and make security auditing difficult
	m.Import(internalLogPackage)

	// Match internal log calls with non-constant format strings
	// All format strings should be compile-time constants for security and auditing
	m.Match(
		`log.Debug($format, $*args)`,
		`log.Info($format, $*args)`,
		`log.Warn($format, $*args)`,
		`log.Error($format, $*args)`,
	).
		Where(
			!m.File().Name.Matches(`.*_test\.go$`) &&
				!m["format"].Const,
		).
		Report(internalLogDynamicMessage)
}

//doc:summary STANDARD LOG SECURITY: detects usage of variable format strings in standard log functions (depguard-allowed files only)
//doc:before  log.Printf(msg, err)
//doc:after   log.Printf("specific error message: %s", err.Error())
//doc:tags    security standard-log compile-time-safety injection depguard
func stdLogVariableFormat(m dsl.Matcher) {
	// SECURITY POLICY: Standard logging requires compile-time constant format strings
	// Same rationale as internal logging - prevent format string injection attacks
	// Only applies to files where depguard allows standard log usage
	m.Import(`log`)

	// Match standard log calls with non-constant format strings in allowed files
	// Enforce same security requirements as internal logging
	m.Match(
		`log.Printf($format, $*args)`,
		`log.Fatalf($format, $*args)`,
		`log.Panicf($format, $*args)`,
	).
		Where(
			(m.File().PkgPath.Matches(`.*/scripts/.*`) ||
				m.File().PkgPath.Matches(`.*/tools/.*`) ||
				m.File().PkgPath.Matches(`.*/internal/log/log\.go$`) ||
				m.File().PkgPath.Matches(`.*/internal/orchestrion/.*`) ||
				m.File().PkgPath.Matches(`.*/instrumentation/testutils/sql/sql\.go$`)) &&
				!m.File().Name.Matches(`.*_test\.go$`) &&
				!m["format"].Const,
		).
		Report(stdLogDynamicMessage)
}

//doc:summary AUTO-FIX: suggests err.Error() with %s for error types in internal logging
//doc:before  log.Error("operation failed: %s", err.Error())
//doc:after   log.Error("operation failed: %s", err.Error())
//doc:tags    best-practice internal-log error-handling auto-fix
func internalLogSuggestErrorString(m dsl.Matcher) {
	m.Import(internalLogPackage)

	// AUTO-FIX: Debug single error patterns (generic alias matching)
	m.Match(`$pkg.Debug($format, $err)`).
		Where(
			!m.File().Name.Matches(`.*_test\.go$`) &&
				m["format"].Text.Matches(formatVerbRegexPattern) &&
				m["format"].Text.Matches(endVerbPattern) &&
				(m["err"].Type.Is(`error`) || m["err"].Filter(implementsError)) &&
				!m["err"].Text.Matches(`.*\.Error\(\)`),
		).
		Suggest(`$pkg.Debug($format, $err.Error())`)

	// AUTO-FIX: Info single error patterns (generic alias matching)
	m.Match(`$pkg.Info($format, $err)`).
		Where(
			!m.File().Name.Matches(`.*_test\.go$`) &&
				m["format"].Text.Matches(formatVerbRegexPattern) &&
				m["format"].Text.Matches(endVerbPattern) &&
				(m["err"].Type.Is(`error`) || m["err"].Filter(implementsError)) &&
				!m["err"].Text.Matches(`.*\.Error\(\)`),
		).
		Suggest(`$pkg.Info($format, $err.Error())`)

	// AUTO-FIX: Warn single error patterns (generic alias matching)
	m.Match(`$pkg.Warn($format, $err)`).
		Where(
			!m.File().Name.Matches(`.*_test\.go$`) &&
				m["format"].Text.Matches(formatVerbRegexPattern) &&
				m["format"].Text.Matches(endVerbPattern) &&
				(m["err"].Type.Is(`error`) || m["err"].Filter(implementsError)) &&
				!m["err"].Text.Matches(`.*\.Error\(\)`),
		).
		Suggest(`$pkg.Warn($format, $err.Error())`)

	// AUTO-FIX: Error single error patterns (generic alias matching)
	m.Match(`$pkg.Error($format, $err)`).
		Where(
			!m.File().Name.Matches(`.*_test\.go$`) &&
				m["format"].Text.Matches(formatVerbRegexPattern) &&
				m["format"].Text.Matches(endVerbPattern) &&
				(m["err"].Type.Is(`error`) || m["err"].Filter(implementsError)) &&
				!m["err"].Text.Matches(`.*\.Error\(\)`),
		).
		Suggest(`$pkg.Error($format, $err.Error())`)
}

// AUTO-FIX: Multi-argument error patterns with %v at end (raw error variables only)
func internalLogSuggestErrorStringMulti(m dsl.Matcher) {
	m.Import(internalLogPackage)

	// AUTO-FIX: Two-argument error patterns (format, arg1, err)
	m.Match(`$pkg.Debug($format, $arg1, $err)`).
		Where(
			!m.File().Name.Matches(`.*_test\.go$`) &&
				m["format"].Text.Matches(formatVerbRegexPattern) &&
				m["format"].Text.Matches(endVerbPattern) &&
				(m["err"].Type.Is(`error`) || m["err"].Filter(implementsError)) &&
				!m["err"].Text.Matches(`.*\.Error\(\)`),
		).
		Suggest(`$pkg.Debug($format, $arg1, $err.Error())`)

	m.Match(`$pkg.Info($format, $arg1, $err)`).
		Where(
			!m.File().Name.Matches(`.*_test\.go$`) &&
				m["format"].Text.Matches(formatVerbRegexPattern) &&
				m["format"].Text.Matches(endVerbPattern) &&
				(m["err"].Type.Is(`error`) || m["err"].Filter(implementsError)) &&
				!m["err"].Text.Matches(`.*\.Error\(\)`),
		).
		Suggest(`$pkg.Info($format, $arg1, $err.Error())`)

	m.Match(`$pkg.Warn($format, $arg1, $err)`).
		Where(
			!m.File().Name.Matches(`.*_test\.go$`) &&
				m["format"].Text.Matches(formatVerbRegexPattern) &&
				m["format"].Text.Matches(endVerbPattern) &&
				(m["err"].Type.Is(`error`) || m["err"].Filter(implementsError)) &&
				!m["err"].Text.Matches(`.*\.Error\(\)`),
		).
		Suggest(`$pkg.Warn($format, $arg1, $err.Error())`)

	m.Match(`$pkg.Error($format, $arg1, $err)`).
		Where(
			!m.File().Name.Matches(`.*_test\.go$`) &&
				m["format"].Text.Matches(formatVerbRegexPattern) &&
				m["format"].Text.Matches(endVerbPattern) &&
				(m["err"].Type.Is(`error`) || m["err"].Filter(implementsError)) &&
				!m["err"].Text.Matches(`.*\.Error\(\)`),
		).
		Suggest(`$pkg.Error($format, $arg1, $err.Error())`)

	// AUTO-FIX: Three-argument error patterns (format, arg1, arg2, err)
	m.Match(`$pkg.Debug($format, $arg1, $arg2, $err)`).
		Where(
			!m.File().Name.Matches(`.*_test\.go$`) &&
				m["format"].Text.Matches(formatVerbRegexPattern) &&
				m["format"].Text.Matches(endVerbPattern) &&
				(m["err"].Type.Is(`error`) || m["err"].Filter(implementsError)) &&
				!m["err"].Text.Matches(`.*\.Error\(\)`),
		).
		Suggest(`$pkg.Debug($format, $arg1, $arg2, $err.Error())`)

	m.Match(`$pkg.Info($format, $arg1, $arg2, $err)`).
		Where(
			!m.File().Name.Matches(`.*_test\.go$`) &&
				m["format"].Text.Matches(formatVerbRegexPattern) &&
				m["format"].Text.Matches(endVerbPattern) &&
				(m["err"].Type.Is(`error`) || m["err"].Filter(implementsError)) &&
				!m["err"].Text.Matches(`.*\.Error\(\)`),
		).
		Suggest(`$pkg.Info($format, $arg1, $arg2, $err.Error())`)

	m.Match(`$pkg.Warn($format, $arg1, $arg2, $err)`).
		Where(
			!m.File().Name.Matches(`.*_test\.go$`) &&
				m["format"].Text.Matches(formatVerbRegexPattern) &&
				m["format"].Text.Matches(endVerbPattern) &&
				(m["err"].Type.Is(`error`) || m["err"].Filter(implementsError)) &&
				!m["err"].Text.Matches(`.*\.Error\(\)`),
		).
		Suggest(`$pkg.Warn($format, $arg1, $arg2, $err.Error())`)

	m.Match(`$pkg.Error($format, $arg1, $arg2, $err)`).
		Where(
			!m.File().Name.Matches(`.*_test\.go$`) &&
				m["format"].Text.Matches(formatVerbRegexPattern) &&
				m["format"].Text.Matches(endVerbPattern) &&
				(m["err"].Type.Is(`error`) || m["err"].Filter(implementsError)) &&
				!m["err"].Text.Matches(`.*\.Error\(\)`),
		).
		Suggest(`$pkg.Error($format, $arg1, $arg2, $err.Error())`)
}

// implementsError checks if a type implements the error interface.
// This includes both direct implementations and pointer receivers.
// Used to identify when %v usage is safe in internal/standard logging contexts.
func implementsError(ctx *dsl.VarFilterContext) bool {
	iface := ctx.GetInterface(`error`)
	return types.Implements(ctx.Type, iface) || types.Implements(types.NewPointer(ctx.Type), iface)
}
