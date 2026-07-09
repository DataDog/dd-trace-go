// Package logerrors contains test cases for the constantlogmsg analyzer.
package logerrors

import (
	internallog "example.com/fakelog"
	telemetrylog "example.com/faketelemetrylog"
)

const constMsg = "constant message template"

var dynMsg = "dynamic message"

type myError struct{ msg string }

func (e *myError) Error() string { return e.msg }

// ── Good: all constant messages ──────────────────────────────────────────────

func goodInternalLog() {
	internallog.Error("operation failed: %s", &myError{})
	internallog.Warn("configuration issue: %v", 42)
	internallog.Error(constMsg) // named constant is fine
}

func goodTelemetryLogPkgLevel() {
	telemetrylog.Debug("debug event")
	telemetrylog.Warn("warn event")
	telemetrylog.Error("error event")
	telemetrylog.Debug(constMsg)
}

func goodTelemetryLogMethod() {
	logger := telemetrylog.With()
	logger.Debug("debug via method")
	logger.Warn("warn via method")
	logger.Error("error via method")
	logger.Error(constMsg)
}

func goodHelpers(err *myError) {
	telemetrylog.ReportError("sdk error occurred", err)
	telemetrylog.ReportPanic(recover(), "panic in goroutine")
}

// ── Bad: non-constant message arguments ──────────────────────────────────────

func badInternalLog(err *myError) {
	internallog.Error(err.Error())         // want "message argument"
	internallog.Error("prefix: " + dynMsg) // want "message argument"
	internallog.Warn(dynMsg)               // want "message argument"
}

func badTelemetryLogPkgLevel(err *myError) {
	telemetrylog.Debug(err.Error())         // want "message argument"
	telemetrylog.Warn("prefix: " + dynMsg) // want "message argument"
	telemetrylog.Error(dynMsg)              // want "message argument"
}

func badTelemetryLogMethod(err *myError) {
	logger := telemetrylog.With()
	logger.Debug(err.Error())         // want "message argument"
	logger.Warn("prefix: " + dynMsg) // want "message argument"
	logger.Error(dynMsg)              // want "message argument"
}

func badHelpers(err *myError) {
	telemetrylog.ReportError(err.Error(), err) // want "message argument"

	// ReportPanic: second arg (index 1) is the message.
	telemetrylog.ReportPanic(recover(), dynMsg)                  // want "message argument"
	telemetrylog.ReportPanic(recover(), "prefix: "+err.Error()) // want "message argument"
}
