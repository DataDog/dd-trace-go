// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package app

import (
	. "example.com/fixture/internal/log"
	weird "example.com/fixture/internal/log"
)

const msgConst = "const message: %s"

var msgVar = "var message: %s"

// notError is a same-package function that must NOT be picked up as a log
// call, even though it shares Error's name.
func notError(format string, a ...any) {}

// T is a local type whose Error method must NOT be picked up.
type T struct{}

func (T) Error(format string, a ...any) {}

// S is a local struct whose Error field must NOT be picked up.
type S struct {
	Error func(format string, a ...any)
}

// pkgInitValue mirrors ddtrace/tracer/time_windows.go's `var now = func() ...`:
// a log call in a package-level initializer's function literal, outside any
// function declaration, that the scan must still pick up.
var pkgInitValue = func() int {
	weird.Error("in package initializer: %s", "p")
	return 0
}()

func Reads() {
	weird.Error("failed to marshal agent payload: %s", "x")
	weird.Warn("recovered panic in poll loop: %v", 1)
	weird.Error("invalid value for DD_FOO: %s", "y")
	weird.Warn("unsupported mode, ignoring it")
	Error("dot-imported error: %s", "z")
	weird.Debug("debug level is not audited: %s", "d")
	weird.Info("info level is not audited")
	weird.Error(msgConst, "m")
	weird.Error(msgVar, "v")
	weird.Error("suppressed by directive: %s", "s") //errtrack:ignore — reviewed, stays on plain log.Error
	weird.Error(                                    //errtrack:ignore inside the call span
		"suppressed multi-line: %s", "s2")
	weird.Error("kept", "k")
}

func FalsePositives(t T, s S) {
	t.Error("method with the same name: %s", "m")
	s.Error("struct field with the same name: %s", "f")
	notError("same-package function: %s", "l")
}
