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

func good() {
	// ✅ Constant string literals are fine — no diagnostics expected.
	internallog.Error("operation failed: %s", &myError{})
	internallog.Warn("configuration issue: %v", 42)
	internallog.Error(constMsg)
	telemetrylog.ReportError("sdk error occurred", &myError{})
	telemetrylog.ReportPanic(recover(), "panic in goroutine")
}

func bad(err *myError) {
	// ❌ Non-constant first arguments — analyzer must report diagnostics.
	internallog.Error(err.Error())              // want "message argument"
	internallog.Error("prefix: " + dynMsg)      // want "message argument"
	internallog.Warn(dynMsg)                    // want "message argument"

	telemetrylog.ReportError(err.Error(), err) // want "message argument"

	// ReportPanic: second arg (index 1) is the message.
	telemetrylog.ReportPanic(recover(), dynMsg)                  // want "message argument"
	telemetrylog.ReportPanic(recover(), "prefix: "+err.Error()) // want "message argument"
}
